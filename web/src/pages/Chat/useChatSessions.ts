import { useCallback, useRef, useState } from 'react';
import {
  emptySlice, applyFrame, mergeHistoryIntoSlice, settledMessages,
  setServerRunning as setServerRunningCore,
  type SessionSlice, type SliceMap,
} from './chatSessionsCore';
import type { ChatMsg } from './chatMessage';
import type { BridgeIncoming } from '@/hooks/useBridgeSocket';

// React binding for the per-conversation live-state store. The pure logic
// (slice shape, frame reducer, history merge) lives in chatSessionsCore.ts and
// is unit-tested there; this file only owns the state container and the
// identity-stability concerns that come with it.
//
// Re-exported so callers have one import for the whole feature.
export {
  emptySlice, applyFrame, mergeHistoryIntoSlice, historyToMessages,
  setServerRunning, isHistoryMessage,
} from './chatSessionsCore';
export type { SessionSlice, SliceMap } from './chatSessionsCore';

/**
 * Per-conversation live state store.
 *
 * Held in component state (keyed by conversation id) rather than an external
 * store: conversation→conversation navigation reuses the same route element,
 * so the store survives switching. If a future requirement needs live output
 * to survive leaving the chat page entirely, the internals here can be swapped
 * for a module-level store read via useSyncExternalStore without changing any
 * of the call sites.
 */
export function useChatSessions() {
  const [slices, setSlices] = useState<SliceMap>({});
  // Mirror for callbacks that must read the latest slices without being
  // re-created on every change (keeps their identity stable).
  const slicesRef = useRef(slices);
  slicesRef.current = slices;

  const ensureSlice = useCallback((id: string) => {
    if (!id) return;
    setSlices(prev => (prev[id] ? prev : { ...prev, [id]: emptySlice() }));
  }, []);

  const updateSlice = useCallback((id: string, fn: (s: SessionSlice) => SessionSlice) => {
    if (!id) return;
    setSlices(prev => {
      const current = prev[id] ?? emptySlice();
      const next = fn(current);
      // Preserve overall identity when the slice itself did not change, so
      // memoized consumers (MessageRow) are not invalidated needlessly.
      if (next === current) return prev;
      return { ...prev, [id]: next };
    });
  }, []);

  const apply = useCallback((id: string, frame: BridgeIncoming, previewHandle?: string, now?: string) => {
    updateSlice(id, s => applyFrame(s, frame, previewHandle, now));
  }, [updateSlice]);

  const seedHistory = useCallback((id: string, history: ChatMsg[]) => {
    updateSlice(id, s => mergeHistoryIntoSlice(s, history));
  }, [updateSlice]);

  /**
   * Overwrite this conversation's server-authoritative busy flag. Fed from the
   * management REST API (`session.running`) on load and on every session-list
   * poll — the only source that survives a page reload or a lost bridge frame.
   */
  const setServerRunning = useCallback((id: string, running: boolean) => {
    updateSlice(id, s => setServerRunningCore(s, running));
  }, [updateSlice]);

  /**
   * Settle every conversation's LOCAL stream flags (typing/streaming) — with the
   * connection down no terminal frame can arrive, so without this a red stop
   * button would stay stuck forever.
   *
   * `serverRunning` is deliberately NOT touched: it is REST-sourced truth, and
   * clearing it here would re-introduce the "reload shows a sendable composer
   * while the turn still runs" bug for every disconnected session. The next
   * successful poll / fetchData reconciles it.
   */
  const settleAll = useCallback(() => {
    setSlices(prev => {
      let changed = false;
      const next: SliceMap = {};
      for (const [id, s] of Object.entries(prev)) {
        if (s.typing || s.messages.some(m => m.streaming)) {
          changed = true;
          next[id] = { ...s, typing: false, messages: settledMessages(s.messages) };
        } else {
          next[id] = s;
        }
      }
      return changed ? next : prev;
    });
  }, []);

  return {
    slices, slicesRef, setSlices, ensureSlice, updateSlice, apply, seedHistory,
    setServerRunning, settleAll,
  };
}

export type ChatSessionsStore = ReturnType<typeof useChatSessions>;

// Re-exported so consumers can build the session_key → id index without
// importing the bridge types themselves.
export type { BridgeIncoming };
