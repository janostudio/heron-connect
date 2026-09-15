import { describe, it, expect } from 'vitest';
import {
  emptySlice, applyFrame, mergeHistoryIntoSlice, historyToMessages,
  type SessionSlice, type SliceMap,
} from './chatSessionsCore';
import type { BridgeIncoming } from '@/hooks/useBridgeSocket';

// ── Multi-conversation interleaving ──────────────────────────
//
// Regression tests for the two questions this suite was added to answer:
//
//   1. "If I switch between running conversations, is the tool/progress log
//      interrupted?"  → No: each conversation owns a slice, and the frame
//      reducer only ever touches the slice it is handed. The interleaving
//      below is exactly what ChatView's router does (route by the frame's own
//      session_id, fall back to session_key).
//
//   2. "Does the page freeze with several conversations streaming?" → The
//      reducer's work per frame is O(rows in that one conversation), and rows
//      that did not change keep their object identity (so React.memo skips
//      them). The identity assertions below pin that property.

const KEY_A = 'bridge:web-admin:p:conv-aaa';
const KEY_B = 'bridge:web-admin:p:conv-bbb';
const KEY_C = 'bridge:web-admin:p:conv-ccc';

const frame = (f: Record<string, unknown>): BridgeIncoming => f as unknown as BridgeIncoming;

const progress = (items: unknown[]) =>
  '__heron_connect_progress_card_v1__:' + JSON.stringify({ items, state: 'running' });

// Mirror of ChatView's router: id → key index → drop.
function makeRouter(keyToId: Map<string, string>) {
  return (msg: BridgeIncoming): string | undefined => {
    const msgID = (msg as any).session_id as string | undefined;
    const msgKey = (msg as any).session_key as string | undefined;
    if (msgID) return msgID;
    if (msgKey) return keyToId.get(msgKey);
    return undefined;
  };
}

describe('three conversations streaming concurrently', () => {
  it('keeps each conversation\'s tool progress in its own slice', () => {
    const keyToId = new Map([[KEY_A, 's89'], [KEY_B, 's91'], [KEY_C, 's93']]);
    const route = makeRouter(keyToId);
    const store: SliceMap = {};

    const dispatch = (msg: BridgeIncoming) => {
      const id = route(msg);
      if (!id) return;
      store[id] = applyFrame(store[id] ?? emptySlice(), msg);
    };

    // All three start turns and stream tool progress, interleaved.
    dispatch(frame({ type: 'typing_start', session_key: KEY_A }));
    dispatch(frame({ type: 'typing_start', session_key: KEY_B }));
    dispatch(frame({ type: 'typing_start', session_key: KEY_C }));

    dispatch(frame({ type: 'preview_start', session_key: KEY_A, session_id: 's89', ref_id: 'ra', content: progress([{ kind: 'tool_use', tool: 'Read', text: 'A reading' }]) }));
    dispatch(frame({ type: 'preview_start', session_key: KEY_B, session_id: 's91', ref_id: 'rb', content: progress([{ kind: 'tool_use', tool: 'Bash', text: 'B running' }]) }));
    dispatch(frame({ type: 'preview_start', session_key: KEY_C, session_id: 's93', ref_id: 'rc', content: progress([{ kind: 'tool_use', tool: 'Grep', text: 'C searching' }]) }));

    expect(store['s89'].messages[0].progressCard?.items[0]).toMatchObject({ tool: 'Read' });
    expect(store['s91'].messages[0].progressCard?.items[0]).toMatchObject({ tool: 'Bash' });
    expect(store['s93'].messages[0].progressCard?.items[0]).toMatchObject({ tool: 'Grep' });

    // More tool events arrive for A only — B and C must not change at all.
    const bBefore = store['s91'];
    const cBefore = store['s93'];
    dispatch(frame({
      type: 'update_message', session_key: KEY_A, preview_handle: store['s89'].messages[0].previewHandle,
      content: progress([
        { kind: 'tool_use', tool: 'Read', text: 'A reading' },
        { kind: 'tool_result', tool: 'Read', text: 'A done' },
      ]),
    }));

    expect(store['s89'].messages[0].progressCard?.items).toHaveLength(2);
    expect(store['s91']).toBe(bBefore);   // untouched slices keep identity
    expect(store['s93']).toBe(cBefore);
  });

  it('three interleaved turns each get their own answer', () => {
    const keyToId = new Map([[KEY_A, 's89'], [KEY_B, 's91']]);
    const route = makeRouter(keyToId);
    const store: SliceMap = {};

    const dispatch = (msg: BridgeIncoming) => {
      const id = route(msg);
      if (!id) return;
      store[id] = applyFrame(store[id] ?? emptySlice(), msg);
    };

    // Deltas interleave: A, B, A, B, then finals A, B.
    dispatch(frame({ type: 'reply_stream', session_key: KEY_A, session_id: 's89', full_text: 'A1', done: false }));
    dispatch(frame({ type: 'reply_stream', session_key: KEY_B, session_id: 's91', full_text: 'B1', done: false }));
    dispatch(frame({ type: 'reply_stream', session_key: KEY_A, session_id: 's89', full_text: 'A1A2', done: false }));
    dispatch(frame({ type: 'reply_stream', session_key: KEY_B, session_id: 's91', full_text: 'B1B2', done: false }));
    dispatch(frame({ type: 'reply_stream', session_key: KEY_A, session_id: 's89', full_text: 'A final', done: true }));
    dispatch(frame({ type: 'reply_stream', session_key: KEY_B, session_id: 's91', full_text: 'B final', done: true }));

    expect(store['s89'].messages).toHaveLength(1);
    expect(store['s89'].messages[0].content).toBe('A final');
    expect(store['s89'].messages[0].streaming).toBe(false);

    expect(store['s91'].messages).toHaveLength(1);
    expect(store['s91'].messages[0].content).toBe('B final');
  });

  it('one conversation finishing does not stop another\'s indicator', () => {
    // This is the specific failure mode of the old settleAllStreaming().
    let a: SessionSlice = { ...emptySlice(), typing: true };
    let b: SessionSlice = { ...emptySlice(), typing: true };
    let c: SessionSlice = { ...emptySlice(), typing: true };

    // A completes: typing_stop is routed to A's slice only.
    a = applyFrame(a, frame({ type: 'typing_stop', session_key: KEY_A }));

    expect(a.typing).toBe(false);
    expect(b.typing).toBe(true);   // B keeps running
    expect(c.typing).toBe(true);   // C keeps running
  });

  it('switching away and back preserves the tool log built meanwhile', () => {
    // Simulates: view A (seed history) → switch to B → come back to A while A
    // ran a long tool-heavy turn.
    let a: SessionSlice = mergeHistoryIntoSlice(emptySlice(), historyToMessages([
      { role: 'user', content: 'old question', timestamp: 't1' },
    ]));

    // While the user is on B, A streams a progress block + an answer.
    a = applyFrame(a, frame({
      type: 'preview_start', session_key: KEY_A, session_id: 's89', ref_id: 'r1',
      content: progress([{ kind: 'tool_use', tool: 'Read', text: 'reading during absence' }]),
    }));
    a = applyFrame(a, frame({
      type: 'reply_stream', session_key: KEY_A, session_id: 's89', full_text: 'answer while away', done: false,
    }));

    // Returning re-fetches history (which does NOT contain the in-flight turn).
    a = mergeHistoryIntoSlice(a, historyToMessages([{ role: 'user', content: 'old question', timestamp: 't1' }]));

    expect(a.messages.map(m => m.content)).toEqual([
      'old question',
      progress([{ kind: 'tool_use', tool: 'Read', text: 'reading during absence' }]),
      'answer while away',
    ]);
    expect(a.messages[1].progressCard?.items[0]).toMatchObject({ tool: 'Read' });
    expect(a.messages[2].streaming).toBe(true);
  });
});

// ── Cost per frame (why several streams do not freeze the page) ──

describe('per-frame cost', () => {
  it('a long transcript does not get rebuilt on every delta', () => {
    // 200 settled rows + one live answer.
    const settled = Array.from({ length: 200 }, (_, i) => ({
      id: `hist-${i}`, role: 'assistant' as const, content: `row ${i}`, format: 'markdown' as const,
    }));
    let s: SessionSlice = { ...emptySlice(), messages: [...settled, {
      id: 'stream-1', role: 'assistant', content: 'x', format: 'markdown', streaming: true,
    }] };

    const s2 = applyFrame(s, frame({ type: 'reply_stream', session_key: KEY_A, session_id: 'sa', full_text: 'xy', done: false }));

    // Only the streaming row is a new object; the 200 settled rows are reused,
    // so React.memo skips re-rendering them.
    let changed = 0;
    for (let i = 0; i < settled.length; i++) if (s2.messages[i] !== s.messages[i]) changed++;
    expect(changed).toBe(0);
    expect(s2.messages[200]).not.toBe(s.messages[200]);
    expect(s2.messages[200].content).toBe('xy');
  });

  it('a frame for one conversation never walks another conversation\'s rows', () => {
    // The reducer is handed a single slice, so cost is bounded by THAT
    // conversation. A huge background transcript cannot slow a delta on the
    // conversation being viewed.
    const huge: SessionSlice = {
      ...emptySlice(),
      messages: Array.from({ length: 1000 }, (_, i) => ({
        id: `m${i}`, role: 'assistant' as const, content: `${i}`, format: 'markdown' as const,
        previewHandle: `web-preview-${i}`,
      })),
    };
    const small: SessionSlice = { ...emptySlice(), messages: [
      { id: 'stream-1', role: 'assistant', content: '', format: 'markdown', streaming: true },
    ] };

    const out = applyFrame(small, frame({ type: 'reply_stream', session_key: KEY_A, session_id: 'sa', full_text: 'hi', done: false }));

    expect(out).not.toBe(small);
    expect(huge.messages).toHaveLength(1000);   // untouched
    expect(out.messages).toHaveLength(1);
  });
});
