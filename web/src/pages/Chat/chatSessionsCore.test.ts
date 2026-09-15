import { describe, it, expect } from 'vitest';
import {
  emptySlice, applyFrame, mergeHistoryIntoSlice, historyToMessages,
  isHistoryMessage, settledMessages, type SessionSlice,
} from './chatSessionsCore';
import type { ChatMsg } from './chatMessage';
import type { BridgeIncoming } from '@/hooks/useBridgeSocket';

// ── Helpers ──────────────────────────────────────────────────

const sliceWith = (...messages: ChatMsg[]): SessionSlice => ({ ...emptySlice(), messages });

const assistant = (id: string, content: string, extra: Partial<ChatMsg> = {}): ChatMsg =>
  ({ id, role: 'assistant', content, format: 'markdown', ...extra });

const user = (id: string, content: string): ChatMsg =>
  ({ id, role: 'user', content });

const frame = (f: Partial<BridgeIncoming> & { type: string }): BridgeIncoming =>
  f as BridgeIncoming;

const KEY_A = 'bridge:web-admin:p:conv-aaa';
const KEY_B = 'bridge:web-admin:p:conv-bbb';

// ── Frame routing / per-conversation isolation ───────────────
//
// These are the regression tests for the reported bug: "only the currently
// open conversation receives messages". The reducer works on ONE slice, so
// isolation is a property of the caller (ChatView routes by the frame's own
// session id/key). What we CAN prove here is that applying a frame never
// touches any state outside the slice it is given, and that the router's
// resolution rules hold.

describe('per-conversation isolation', () => {
  it('applying a frame to B does not mutate A', () => {
    const a = sliceWith(assistant('s1', 'partial', { streaming: true }));
    const aSnapshot = JSON.stringify(a);

    // A frame for B, applied only to B's slice.
    const b = applyFrame(emptySlice(), frame({
      type: 'card', session_key: KEY_B, session_id: 'sb',
      reply_ctx: KEY_B, card: { elements: [] },
    } as any));

    expect(JSON.stringify(a)).toBe(aSnapshot);   // A untouched
    expect(b.messages).toHaveLength(1);          // B received
    expect(a.messages).toHaveLength(1);
    expect(a.messages[0].content).toBe('partial');
  });

  it('a terminal event settles only the slice it is applied to', () => {
    // Both conversations mid-turn.
    const a = { ...sliceWith(assistant('sa', 'a-streaming', { streaming: true })), typing: true };
    const b = { ...sliceWith(assistant('sb', 'b-streaming', { streaming: true })), typing: true };

    // A finishes; only A's slice is passed through the reducer.
    const aDone = applyFrame(a, frame({
      type: 'typing_stop', session_key: KEY_A,
    } as any));

    expect(aDone.typing).toBe(false);
    expect(aDone.messages[0].streaming).toBe(false);

    // B is untouched — this is the "A finishing must not kill B's indicator" case.
    expect(b.typing).toBe(true);
    expect(b.messages[0].streaming).toBe(true);
  });

  it('typing_start/typing_stop carry no session_id (backend contract)', () => {
    // Guards the assumption the router relies on: these frames only carry a
    // session_key, so routing MUST have a key→id index as a fallback.
    const t = applyFrame({ ...emptySlice(), typing: false }, frame({
      type: 'typing_start', session_key: KEY_A,
    } as any));
    expect(t.typing).toBe(true);

    const stopped = applyFrame(t, frame({ type: 'typing_stop', session_key: KEY_A } as any));
    expect(stopped.typing).toBe(false);
  });
});

// ── Streaming lifecycle ──────────────────────────────────────

describe('reply_stream lifecycle', () => {
  it('accumulates deltas into one placeholder, then finalizes on done', () => {
    let s = emptySlice();
    s = applyFrame(s, frame({ type: 'reply_stream', session_key: KEY_A, session_id: 'sa', delta: 'He', full_text: 'He', done: false } as any));
    s = applyFrame(s, frame({ type: 'reply_stream', session_key: KEY_A, session_id: 'sa', delta: 'llo', full_text: 'Hello', done: false } as any));

    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].content).toBe('Hello');
    expect(s.messages[0].streaming).toBe(true);

    s = applyFrame(s, frame({ type: 'reply_stream', session_key: KEY_A, session_id: 'sa', delta: '', full_text: 'Hello world', done: true } as any));
    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].content).toBe('Hello world');
    expect(s.messages[0].streaming).toBe(false);
  });

  it('a delta arriving with no placeholder appends a new streaming message', () => {
    // This is the background-conversation case: we only ever saw typing_start
    // for a conversation we were not viewing.
    const s = applyFrame(emptySlice(), frame({
      type: 'reply_stream', session_key: KEY_B, session_id: 'sb', full_text: 'mid-stream', done: false,
    } as any));
    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].streaming).toBe(true);
    expect(s.messages[0].content).toBe('mid-stream');
  });

  it('done with no placeholder still records the final answer', () => {
    const s = applyFrame(emptySlice(), frame({
      type: 'reply_stream', session_key: KEY_B, session_id: 'sb', full_text: 'final', done: true,
    } as any));
    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].content).toBe('final');
    expect(s.messages[0].streaming).toBe(false);
  });

  it('a stream delta never hijacks a thinking/tool progress preview', () => {
    // Progress previews carry a previewHandle; the answer placeholder must not
    // be confused with them.
    const withPreview: SessionSlice = sliceWith(
      assistant('stream-web-preview-1', 'running a tool...', { streaming: true, previewHandle: 'web-preview-1' }),
    );
    const s = applyFrame(withPreview, frame({
      type: 'reply_stream', session_key: KEY_A, session_id: 'sa', full_text: 'answer', done: false,
    } as any));

    expect(s.messages).toHaveLength(2);
    expect(s.messages[0].content).toBe('running a tool...');   // preview intact
    expect(s.messages[1].content).toBe('answer');              // answer appended
  });

  it('a final reply settles lingering progress previews', () => {
    const withPreview: SessionSlice = sliceWith(
      assistant('stream-web-preview-1', 'tool', { streaming: true, previewHandle: 'web-preview-1' }),
      assistant('a1', 'partial', { streaming: true }),
    );
    const s = applyFrame(withPreview, frame({
      type: 'reply', session_key: KEY_A, session_id: 'sa', content: 'done', format: 'markdown',
    } as any));

    expect(s.messages.every(m => !m.streaming)).toBe(true);
    expect(s.messages[1].content).toBe('done');
  });
});

// ── Progress card (tool / thinking) live updates ─────────────

describe('progress card streaming', () => {
  const payload = (items: unknown[]) => '__heron_connect_progress_card_v1__:' + JSON.stringify({ items, state: 'running' });

  it('preview_start creates a progress block using the acked handle', () => {
    const s = applyFrame(emptySlice(), frame({
      type: 'preview_start', session_key: KEY_A, session_id: 'sa', ref_id: 'r1',
      content: payload([{ kind: 'tool_use', tool: 'Read', text: 'reading' }]),
    } as any), 'web-preview-7');

    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].previewHandle).toBe('web-preview-7');
    expect(s.messages[0].streaming).toBe(true);
    expect(s.messages[0].progressCard?.items).toHaveLength(1);
    expect(s.previewHandleCounter).toBe(1);
  });

  it('update_message updates the matching progress block in place', () => {
    let s = applyFrame(emptySlice(), frame({
      type: 'preview_start', session_key: KEY_A, session_id: 'sa', ref_id: 'r1',
      content: payload([{ kind: 'tool_use', tool: 'Read', text: 'reading' }]),
    } as any), 'web-preview-1');

    s = applyFrame(s, frame({
      type: 'update_message', session_key: KEY_A, preview_handle: 'web-preview-1',
      content: payload([
        { kind: 'tool_use', tool: 'Read', text: 'reading' },
        { kind: 'tool_result', tool: 'Read', text: 'file contents' },
      ]),
    } as any));

    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].progressCard?.items).toHaveLength(2);
  });

  it('update_message re-attaches when the preview was lost mid-turn', () => {
    // e.g. the preview frame was missed; update_message carries the FULL
    // accumulated progress, so it must re-create the block rather than drop it.
    const s = applyFrame(emptySlice(), frame({
      type: 'update_message', session_key: KEY_A, preview_handle: 'web-preview-9',
      content: payload([{ kind: 'tool_use', tool: 'Bash', text: 'ls' }]),
    } as any));

    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].previewHandle).toBe('web-preview-9');
    expect(s.messages[0].streaming).toBe(true);
  });

  it('delete_message finalizes the progress block instead of removing it', () => {
    let s = applyFrame(emptySlice(), frame({
      type: 'preview_start', session_key: KEY_A, session_id: 'sa', ref_id: 'r1',
      content: payload([{ kind: 'tool_use', tool: 'Read', text: 'x' }]),
    } as any), 'web-preview-1');

    s = applyFrame(s, frame({ type: 'delete_message', session_key: KEY_A, preview_handle: 'web-preview-1' } as any));

    expect(s.messages).toHaveLength(1);         // kept, not deleted
    expect(s.messages[0].streaming).toBe(false); // but no longer live
  });

  it('an unknown preview handle is ignored, leaving the slice unchanged', () => {
    const s0 = sliceWith(assistant('m', 'hi'));
    const s = applyFrame(s0, frame({
      type: 'delete_message', session_key: KEY_A, preview_handle: 'nope',
    } as any));
    expect(s).toBe(s0);   // identity preserved
  });
});

// ── History merge (the "switched away and back" case) ────────

describe('mergeHistoryIntoSlice', () => {
  it('seeds from history when there is no live output yet', () => {
    const s = mergeHistoryIntoSlice(emptySlice(), [
      user('hist-0', 'q1'), assistant('hist-1', 'a1'),
    ]);
    expect(s.messages.map(m => m.id)).toEqual(['hist-0', 'hist-1']);
  });

  it('preserves the live tail that arrived while viewing another conversation', () => {
    const live = sliceWith(user('hist-0', 'q1'), assistant('stream-1', 'partial answer', { streaming: true }));
    const s = mergeHistoryIntoSlice(live, [user('hist-0', 'q1')]);

    expect(s.messages.map(m => m.id)).toEqual(['hist-0', 'stream-1']);
    expect(s.messages[1].content).toBe('partial answer');
    expect(s.messages[1].streaming).toBe(true);   // in-flight state survives
  });

  it('puts history first and is idempotent across repeated fetches', () => {
    const hist = [user('hist-0', 'q1'), assistant('hist-1', 'a1')];
    let s = mergeHistoryIntoSlice(sliceWith(assistant('live-1', 'tail')), hist);
    const once = s.messages.map(m => m.id);

    s = mergeHistoryIntoSlice(s, hist);
    s = mergeHistoryIntoSlice(s, hist);
    expect(s.messages.map(m => m.id)).toEqual(once);   // no duplication
    expect(once).toEqual(['hist-0', 'hist-1', 'live-1']);
  });

  it('never resets live typing / pending-command state from history', () => {
    const live: SessionSlice = { ...sliceWith(assistant('live-1', 'x')), typing: true, pendingCmd: '/status' };
    const s = mergeHistoryIntoSlice(live, [user('hist-0', 'q1')]);
    expect(s.typing).toBe(true);
    expect(s.pendingCmd).toBe('/status');
  });

  it('keeps the slice identity when nothing changed (memo precondition)', () => {
    const hist = [user('hist-0', 'q1')];
    const s = mergeHistoryIntoSlice(emptySlice(), hist);
    const again = mergeHistoryIntoSlice(s, hist);
    expect(again).toBe(s);
  });

  it('does not drop an optimistic user message not yet persisted', () => {
    // The user just sent; history lags behind and does not contain it.
    const live = sliceWith(user('user-123', 'just typed'));
    const s = mergeHistoryIntoSlice(live, [user('hist-0', 'earlier')]);
    expect(s.messages.map(m => m.content)).toEqual(['earlier', 'just typed']);
  });

  it('historyToMessages marks entries with positional hist- ids', () => {
    const msgs = historyToMessages([
      { role: 'user', content: 'a', timestamp: 't1' },
      { role: 'assistant', content: 'b', timestamp: 't2', attachments: [{ kind: 'image', name: 'x.png', mime_type: 'image/png', path: 'p' }] },
    ]);
    expect(msgs.map(m => m.id)).toEqual(['hist-0', 'hist-1']);
    expect(isHistoryMessage(msgs[0])).toBe(true);
    expect(msgs[1].historyAttachments).toHaveLength(1);
  });
});

// ── Slash-command result panel ───────────────────────────────

describe('pending slash command routing', () => {
  it('routes the next reply to the command panel instead of the transcript', () => {
    const pending: SessionSlice = { ...emptySlice(), pendingCmd: '/status' };
    const s = applyFrame(pending, frame({
      type: 'reply', session_key: KEY_A, session_id: 'sa', content: 'all good', format: 'markdown',
    } as any));

    expect(s.pendingCmd).toBeNull();
    expect(s.cmdResult).toEqual({ command: '/status', content: 'all good', format: 'markdown' });
    expect(s.messages).toHaveLength(0);   // not in the transcript
    expect(s.typing).toBe(false);
  });

  it('routes a card reply to the panel', () => {
    const pending: SessionSlice = { ...emptySlice(), pendingCmd: '/help' };
    const s = applyFrame(pending, frame({
      type: 'card', session_key: KEY_A, session_id: 'sa', card: { elements: [{ type: 'divider' }] },
    } as any));
    expect(s.cmdResult?.format).toBe('card');
    expect(s.cmdResult?.command).toBe('/help');
    expect(s.messages).toHaveLength(0);
  });

  it('keeps panel results per conversation (no cross-talk)', () => {
    const a = applyFrame({ ...emptySlice(), pendingCmd: '/status' }, frame({
      type: 'reply', session_key: KEY_A, session_id: 'sa', content: 'A result', format: 'markdown',
    } as any));
    const b = applyFrame({ ...emptySlice(), pendingCmd: '/skills' }, frame({
      type: 'reply', session_key: KEY_B, session_id: 'sb', content: 'B result', format: 'markdown',
    } as any));

    expect(a.cmdResult?.command).toBe('/status');
    expect(a.cmdResult?.content).toBe('A result');
    expect(b.cmdResult?.command).toBe('/skills');
    expect(b.cmdResult?.content).toBe('B result');
  });

  it('leaves pendingCmd alone for an unrelated frame type', () => {
    const pending: SessionSlice = { ...emptySlice(), pendingCmd: '/status' };
    const s = applyFrame(pending, frame({ type: 'typing_start', session_key: KEY_A } as any));
    expect(s.pendingCmd).toBe('/status');
  });
});

// ── Card / buttons messages ──────────────────────────────────

describe('card and buttons messages', () => {
  it('appends a card message and settles the turn', () => {
    const live: SessionSlice = { ...sliceWith(assistant('stream-1', 'x', { streaming: true })), typing: true };
    const s = applyFrame(live, frame({
      type: 'card', session_key: KEY_A, session_id: 'sa', card: { elements: [{ type: 'divider' }] },
    } as any));

    expect(s.messages).toHaveLength(2);
    expect(s.messages[1].format).toBe('card');
    expect(s.messages[1].card).toEqual({ elements: [{ type: 'divider' }] });
    expect(s.messages[0].streaming).toBe(false);   // prior stream settled
    expect(s.typing).toBe(false);
  });

  it('appends a buttons message with its content and rows', () => {
    const buttons = [[{ text: 'Yes', data: 'yes' }], [{ text: 'No', data: 'no' }]];
    const s = applyFrame(emptySlice(), frame({
      type: 'buttons', session_key: KEY_A, session_id: 'sa', content: 'Pick one', buttons,
    } as any));

    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].format).toBe('buttons');
    expect(s.messages[0].content).toBe('Pick one');
    expect(s.messages[0].buttons).toEqual(buttons);
    expect(s.typing).toBe(false);
  });

  it('a card with no elements still produces a renderable row', () => {
    const s = applyFrame(emptySlice(), frame({
      type: 'card', session_key: KEY_A, session_id: 'sa', card: {},
    } as any));
    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].format).toBe('card');
  });
});

// ── Render-identity invariants (what makes React.memo work) ──

describe('reference preservation', () => {
  it('a streaming delta only allocates a new object for the streamed row', () => {
    const hist = [user('hist-0', 'q'), assistant('hist-1', 'old answer')];
    let s = sliceWith(...hist, assistant('stream-1', 'a', { streaming: true }));

    const s2 = applyFrame(s, frame({
      type: 'reply_stream', session_key: KEY_A, session_id: 'sa', full_text: 'ab', done: false,
    } as any));

    expect(s2.messages[0]).toBe(hist[0]);   // untouched rows keep identity
    expect(s2.messages[1]).toBe(hist[1]);
    expect(s2.messages[2]).not.toBe(s.messages[2]);
  });

  it('settledMessages returns the same array when nothing was streaming', () => {
    const msgs = [assistant('a', 'x'), user('u', 'y')];
    expect(settledMessages(msgs)).toBe(msgs);
  });

  it('settle preserves identity of non-streaming rows', () => {
    const done = assistant('a', 'done');
    const live = assistant('b', 'live', { streaming: true });
    const out = settledMessages([done, live]);
    expect(out[0]).toBe(done);
    expect(out[1]).not.toBe(live);
    expect(out[1].streaming).toBe(false);
  });

  it('finalizing a stream never mutates the previous state array', () => {
    // Regression guard: settledMessages may return the input array unchanged,
    // so the reducer must copy before overwriting a row.
    const prevMessages = [user('hist-0', 'q'), assistant('stream-1', 'partial', { streaming: true })];
    const prev: SessionSlice = { ...emptySlice(), messages: prevMessages };
    const prevFirst = prevMessages[0];

    const next = applyFrame(prev, frame({
      type: 'reply_stream', session_key: KEY_A, session_id: 'sa', full_text: 'final', done: true,
    } as any));

    expect(next).not.toBe(prev);
    expect(next.messages).not.toBe(prevMessages);
    expect(prevMessages[1].content).toBe('partial');    // old state intact
    expect(prevMessages[1].streaming).toBe(true);
    expect(prevMessages[0]).toBe(prevFirst);
    expect(next.messages[1].content).toBe('final');
  });

  it('a no-op frame returns the very same slice object', () => {
    const s = sliceWith(assistant('a', 'x'));
    expect(applyFrame(s, frame({ type: 'delete_message', session_key: KEY_A, preview_handle: 'zzz' } as any))).toBe(s);
    expect(applyFrame(s, frame({ type: 'some_unknown_type', session_key: KEY_A } as any))).toBe(s);
  });
});

// ── Terminal / idle behaviour ────────────────────────────────

describe('typing indicator', () => {
  it('typing_start then typing_stop clears both typing and streaming', () => {
    let s = applyFrame(emptySlice(), frame({ type: 'typing_start', session_key: KEY_A } as any));
    expect(s.typing).toBe(true);

    s = { ...s, messages: [assistant('stream-1', 'x', { streaming: true })] };
    s = applyFrame(s, frame({ type: 'typing_stop', session_key: KEY_A } as any));
    expect(s.typing).toBe(false);
    expect(s.messages[0].streaming).toBe(false);
  });

  it('typing_start on an already-typing slice keeps identity', () => {
    const s: SessionSlice = { ...emptySlice(), typing: true };
    expect(applyFrame(s, frame({ type: 'typing_start', session_key: KEY_A } as any))).toBe(s);
  });

  it('a reply clears the typing indicator', () => {
    const s: SessionSlice = { ...emptySlice(), typing: true };
    const out = applyFrame(s, frame({
      type: 'reply', session_key: KEY_A, session_id: 'sa', content: 'done', format: 'markdown',
    } as any));
    expect(out.typing).toBe(false);
  });
});
