// ── Per-conversation live state (pure logic) ─────────────────
//
// Every Web conversation owns its own session_key, and the bridge broadcasts
// EVERY frame to every connected client (bridge.sendToAdapter is a multicast).
// The page therefore receives live output for all conversations, not just the
// one on screen.
//
// Previously all of this lived in a single set of useState variables and the
// frame handler discarded anything that did not match the session being
// viewed — so switching away from a running conversation silently dropped its
// remaining output, and switching back replaced the transcript with the
// (lagging) persisted history.
//
// This module keeps a separate slice per conversation id, so a background
// conversation keeps accumulating streamed content, tool-progress cards and
// typing state while you look at another one.
//
// Everything here is pure (no React, no DOM) so it can be unit-tested directly
// — see chatSessionsCore.test.ts. The React binding lives in useChatSessions.ts.

import type { ChatMsg } from './chatMessage';
import type { CommandResult } from './CommandResultPanel';
import type { BridgeIncoming } from '@/hooks/useBridgeSocket';
import { nowStamp } from './messageTime';

// Local copy of ProgressCard.parseProgressCard so this module stays free of
// component imports (react-markdown et al) when unit-tested. Must stay in sync
// with ProgressCard.tsx:51-63.
const PROGRESS_CARD_PREFIX = '__heron_connect_progress_card_v1__:';

function parseProgressCard(content: string): ChatMsg['progressCard'] {
  if (!content || !content.startsWith(PROGRESS_CARD_PREFIX)) return undefined;
  try {
    const obj = JSON.parse(content.slice(PROGRESS_CARD_PREFIX.length));
    if (!Array.isArray(obj?.items) || obj.items.length === 0) return undefined;
    return obj;
  } catch {
    return undefined;
  }
}

export interface SessionSlice {
  messages: ChatMsg[];
  typing: boolean;
  /**
   * Server-authoritative busy flag from the management REST API
   * (`session.running`) for THIS conversation.
   *
   * Deliberately separate from `typing`: after a page reload there is no bridge
   * frame history, so only REST can tell us a turn is still running. The two are
   * OR-ed at render time (see ChatView's isRunning) and must never overwrite
   * each other — WS events settle their own side, REST polling settles this one.
   */
  serverRunning: boolean;
  /** Pending slash command whose next reply should go to the result panel. */
  pendingCmd: string | null;
  /** Result currently shown in this conversation's command panel. */
  cmdResult: CommandResult | null;
  /** Monotonic counter for preview handles within this conversation. */
  previewHandleCounter: number;
}

export function emptySlice(): SessionSlice {
  return {
    messages: [],
    typing: false,
    serverRunning: false,
    pendingCmd: null,
    cmdResult: null,
    previewHandleCounter: 0,
  };
}

/**
 * Apply the server's authoritative busy flag to one slice.
 *
 * Returns the input unchanged when the value is identical, so the 5s session
 * poll does not invalidate memoized consumers (MessageRow) on every tick.
 */
export function setServerRunning(slice: SessionSlice, running: boolean): SessionSlice {
  return slice.serverRunning === running ? slice : { ...slice, serverRunning: running };
}

export type SliceMap = Record<string, SessionSlice>;

/** Convert persisted history entries into transcript messages. */
export function historyToMessages(
  history: { role: string; content: string; timestamp: string; attachments?: any[] }[],
): ChatMsg[] {
  return history.map((h, i) => ({
    id: `hist-${i}`,
    role: h.role as 'user' | 'assistant',
    content: h.content,
    format: 'markdown' as const,
    timestamp: h.timestamp,
    historyAttachments: h.attachments,
  }));
}

// A message that came from the server's persisted history carries a synthetic
// positional id (`hist-<n>`). Those ids are only React list keys — they are
// never comparable across renders or to incoming frame data.
export function isHistoryMessage(m: ChatMsg): boolean {
  return m.id.startsWith('hist-');
}

/**
 * Merge freshly fetched history into an existing slice without destroying live
 * content that arrived while the user was viewing another conversation.
 *
 * History is a persisted snapshot, so it structurally cannot contain the
 * in-flight turn. The reconciliation is therefore:
 *
 *   history  ++  (live messages that are not themselves history)
 *
 * which is idempotent — refetching history repeatedly neither duplicates nor
 * drops the live tail. Typing/sending/pending-command are live state and are
 * never reset from history.
 */
export function mergeHistoryIntoSlice(slice: SessionSlice, history: ChatMsg[]): SessionSlice {
  const live = slice.messages.filter(m => !isHistoryMessage(m));
  // No live output: history is the whole truth. Still avoid producing a new
  // object when the incoming history is identical (position-wise) to what we
  // already hold — seedHistory runs on every session switch, and a fresh
  // identity would re-render every memoized row for nothing.
  const merged = live.length === 0 ? history : [...history, ...live];
  if (merged.length === slice.messages.length && merged.every((m, i) => m === slice.messages[i])) {
    return slice; // nothing changed → keep identity so memoized rows bail out
  }
  return { ...slice, messages: merged };
}

/**
 * Settle every streaming flag in one slice (terminal event for that turn).
 * Exported because the store's `settleAll` needs it for the disconnect path.
 */
export function settledMessages(messages: ChatMsg[]): ChatMsg[] {
  if (!messages.some(m => m.streaming)) return messages;
  // Only the rows that were actually streaming get a new object; everything
  // else keeps its reference so the memoized MessageRow does not re-render.
  return messages.map(m => (m.streaming ? { ...m, streaming: false } : m));
}

/**
 * Settle streaming flags and then overwrite one row, returning a NEW array.
 * The copy matters: settledMessages may hand back the input array unchanged
 * (when nothing was streaming), and mutating that in place would corrupt the
 * previous state object.
 *
 * `patch` is applied verbatim, so callers that only want to FILL IN a missing
 * timestamp must do so themselves (see the `stamp` usage below).
 */
function settleAndReplace(messages: ChatMsg[], idx: number, patch: Partial<ChatMsg>): ChatMsg[] {
  const next = [...settledMessages(messages)];
  next[idx] = { ...next[idx], ...patch, streaming: false };
  return next;
}

/**
 * The timestamp to write for a message we just created or just filled in.
 *
 * Assistant messages carry the time of the FIRST VISIBLE RESPONSE (first
 * streamed text / first progress card) and that value is frozen for the rest
 * of the turn — a later delta or the final `reply` must not move it. So this
 * only ever BACKFILLS: an existing stamp always wins.
 */
function stampFor(existing: string | undefined, stamp: string): string {
  return existing || stamp;
}

/**
 * Apply one bridge frame to one conversation slice. Pure: (slice, frame) →
 * slice. Unknown frame types return the input unchanged.
 *
 * `previewHandle` lets the caller supply the preview handle it already acked
 * to the backend for a `preview_start` frame, so the message id and the ack
 * always agree.
 *
 * `now` stamps the messages this frame creates. It defaults to the wall clock
 * and is injectable so the timestamp rules (first-visible-response wins, never
 * overwritten) can be asserted deterministically in tests.
 */
export function applyFrame(slice: SessionSlice, msg: BridgeIncoming, previewHandle?: string, now?: string): SessionSlice {
  const stamp = now || nowStamp();

  // A pending slash command claims the next reply/card and routes it to the
  // command result panel instead of the transcript.
  if (slice.pendingCmd && (msg.type === 'reply' || msg.type === 'card' || msg.type === 'buttons')) {
    const command = slice.pendingCmd;
    let result: CommandResult;
    if (msg.type === 'card') {
      result = { command, content: '', format: 'card', card: (msg as any).card };
    } else if (msg.type === 'buttons') {
      result = { command, content: (msg as any).content, format: 'buttons', buttons: (msg as any).buttons };
    } else {
      result = { command, content: (msg as any).content, format: 'markdown' };
    }
    return { ...slice, pendingCmd: null, cmdResult: result, typing: false };
  }

  switch (msg.type) {
    case 'reply': {
      const reply = msg as Extract<BridgeIncoming, { type: 'reply' }>;
      const format = (reply as any).format === 'markdown' ? 'markdown' : 'text';
      const idx = slice.messages.findIndex(m => m.streaming && m.role === 'assistant' && !m.previewHandle);
      if (idx >= 0) {
        const prev = slice.messages[idx];
        return { ...slice, messages: settleAndReplace(slice.messages, idx, {
          content: reply.content,
          format,
          timestamp: stampFor(prev.timestamp, stamp),
        }), typing: false };
      }
      // No matching placeholder (e.g. the turn started while this conversation
      // was not being viewed — `typing_start` was the only thing we saw). As
      // long as it was in flight, append the final reply rather than dropping it.
      return {
        ...slice,
        messages: [...settledMessages(slice.messages), {
          id: `reply-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          role: 'assistant',
          content: reply.content,
          format,
          timestamp: stamp,
          streaming: false,
        }],
        typing: false,
      };
    }

    case 'reply_stream': {
      const stream = msg as Extract<BridgeIncoming, { type: 'reply_stream' }>;
      if (stream.done) {
        const idx = slice.messages.findIndex(m => m.streaming && m.role === 'assistant' && !m.previewHandle);
        if (idx >= 0) {
          const prev = slice.messages[idx];
          return { ...slice, messages: settleAndReplace(slice.messages, idx, {
            content: stream.full_text,
            timestamp: stampFor(prev.timestamp, stamp),
          }), typing: false };
        }
        return {
          ...slice,
          messages: [...settledMessages(slice.messages), {
            id: `stream-done-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
            role: 'assistant',
            content: stream.full_text,
            format: 'markdown',
            timestamp: stamp,
            streaming: false,
          }],
          typing: false,
        };
      }
      // Intermediate delta. Match the answer placeholder only — a
      // thinking/tool progress preview carries a previewHandle and must never
      // be overwritten with the answer text (that would destroy the tool
      // record the user is watching).
      const answerIdx = slice.messages.findIndex(m => m.streaming && m.role === 'assistant' && !m.previewHandle);
      if (answerIdx >= 0) {
        const prev = slice.messages[answerIdx];
        const messages = [...slice.messages];
        messages[answerIdx] = {
          ...prev,
          content: stream.full_text,
          // First visible response time: set once, never moved by later deltas.
          timestamp: stampFor(prev.timestamp, stamp),
        };
        return { ...slice, messages };
      }
      // No answer placeholder yet (e.g. the first delta of a turn for a
      // conversation we were not viewing, where only typing_start arrived).
      // Append a fresh streaming row; never reuse a progress preview row.
      return {
        ...slice,
        messages: [...slice.messages, {
          id: `stream-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          role: 'assistant',
          content: stream.full_text,
          format: 'markdown',
          timestamp: stamp,
          streaming: true,
        }],
      };
    }

    case 'card': {
      const card = msg as Extract<BridgeIncoming, { type: 'card' }>;
      return {
        ...slice,
        messages: [...settledMessages(slice.messages), {
          id: `card-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          role: 'assistant',
          content: '',
          format: 'card',
          card: card.card,
          timestamp: stamp,
        }],
        typing: false,
      };
    }

    case 'buttons': {
      const btns = msg as Extract<BridgeIncoming, { type: 'buttons' }>;
      return {
        ...slice,
        messages: [...settledMessages(slice.messages), {
          id: `btn-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
          role: 'assistant',
          content: btns.content,
          format: 'buttons',
          buttons: btns.buttons,
          timestamp: stamp,
        }],
        typing: false,
      };
    }

    case 'typing_start':
      return slice.typing ? slice : { ...slice, typing: true };

    case 'typing_stop':
      return { ...slice, typing: false, messages: settledMessages(slice.messages) };

    case 'preview_start': {
      const ps = msg as Extract<BridgeIncoming, { type: 'preview_start' }>;
      // The router mints the handle (it must ack the backend with the exact
      // same value before the reducer runs), so honour it when present.
      const counter = slice.previewHandleCounter + 1;
      const handle = previewHandle || `web-preview-${counter}`;
      return {
        ...slice,
        previewHandleCounter: counter,
        messages: [...slice.messages, {
          id: `stream-${handle}`,
          role: 'assistant',
          content: ps.content,
          format: 'markdown',
          timestamp: stamp,
          streaming: true,
          previewHandle: handle,
          progressCard: parseProgressCard(ps.content),
        }],
      };
    }

    case 'update_message': {
      const um = msg as Extract<BridgeIncoming, { type: 'update_message' }>;
      const idx = slice.messages.findIndex(m => m.streaming && m.previewHandle === um.preview_handle);
      if (idx >= 0) {
        const messages = [...slice.messages];
        messages[idx] = {
          ...messages[idx],
          content: um.content,
          // Re-parse on every push so the structured payload tracks the latest
          // progress without losing previously-toggled expand state (the
          // ProgressCard component holds that state by stable content key).
          progressCard: parseProgressCard(um.content) ?? messages[idx].progressCard,
        };
        return { ...slice, messages };
      }
      // The preview message was lost — most commonly because the user switched
      // conversations mid-turn in an older build, but also possible if the
      // preview arrived before this slice existed. update_message carries the
      // FULL accumulated progress content (all tools so far), so re-attach
      // instead of dropping: the next tool event re-creates the progress block
      // and live updates continue. Only re-attach while the turn is still
      // producing output; finalized previews are never resurrected.
      if (um.preview_handle) {
        return {
          ...slice,
          messages: [...slice.messages, {
            id: `stream-${um.preview_handle}`,
            role: 'assistant',
            content: um.content,
            format: 'markdown',
            timestamp: stamp,
            streaming: true,
            previewHandle: um.preview_handle,
            progressCard: parseProgressCard(um.content),
          }],
        };
      }
      return slice;
    }

    case 'delete_message': {
      const dm = msg as Extract<BridgeIncoming, { type: 'delete_message' }>;
      const idx = slice.messages.findIndex(m => m.streaming && m.previewHandle === dm.preview_handle);
      if (idx < 0) return slice;
      // Finalize the progress block (thinking/tool) instead of removing it, so
      // the executed thinking/tool steps remain visible in the chat history.
      const messages = [...slice.messages];
      messages[idx] = { ...messages[idx], streaming: false };
      return { ...slice, messages };
    }

    default:
      return slice;
  }
}
