import api, { ApiError } from '@/api/client';

// Web reading presence, client half.
//
// The session list is ordered by what is being read right now, which only the
// browser knows. Each tab therefore heartbeats the conversation it has open;
// the server treats "no heartbeat for ViewPresenceTTL" as "closed" and records
// that moment on the session.
//
// Consequences worth keeping in mind:
//
//   - The record is per TAB (sessionStorage, with an in-memory fallback when
//     storage is unusable), not per browser: two tabs can read two
//     conversations at once, and both must count. Closing a tab drops its
//     record, so its heartbeats stop and the server closes the session.
//   - A tab keeps reporting the conversation it opened even after the user
//     navigates to another page of the app, because closing the tab is what
//     ends the reading (an accidental click into the session list should not
//     move a conversation to "recently closed").
//   - Opening another conversation replaces the record; the previous one stops
//     being reported and closes after the TTL, which is what the user sees as
//     "I moved on".
//   - No "closed" message is ever sent. A tab can die without warning (browser
//     killed, laptop lid), so the absence of heartbeats is the only signal the
//     server can trust — and it also keeps two tabs on the same conversation
//     correct: one closing leaves the other refreshing the same key.

const VIEW_KEY = 'cc.viewing';

/**
 * How often a tab re-reports its open conversation. Pairs with the server's
 * 90s presence TTL: nine beats of slack, so a slow request, a browser timer
 * throttle or a brief network blip does not make an open conversation look
 * closed.
 */
export const VIEW_HEARTBEAT_MS = 10_000;

export interface ViewedSession {
  project: string;
  id: string;
}

// sessionStorage throws rather than returning null when storage is disabled by
// policy, so the access itself is guarded.
function store(): Storage | null {
  try {
    return typeof sessionStorage === 'undefined' ? null : sessionStorage;
  } catch {
    return null;
  }
}

// Fallback when sessionStorage cannot be read or written (private mode, quota,
// policy). The record then lives only as long as this page load — heartbeats
// keep working, they just do not survive a navigation-driven remount.
let memoryView: ViewedSession | null = null;

/** The conversation this tab currently has open, if any. */
export function viewedSession(): ViewedSession | null {
  const s = store();
  if (!s) return memoryView;
  try {
    const raw = s.getItem(VIEW_KEY);
    if (!raw) return memoryView;
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return memoryView;
    const { project, id } = parsed as Partial<ViewedSession>;
    if (typeof project !== 'string' || typeof id !== 'string' || !project || !id) return memoryView;
    return { project, id };
  } catch {
    return memoryView;
  }
}

/**
 * Remember that this tab has `id` open and report it immediately, so the
 * session shows up as being read without waiting for the next tick.
 */
export function setViewedSession(project: string, id: string): void {
  if (!project || !id) return;
  memoryView = { project, id };
  const s = store();
  if (s) {
    try {
      s.setItem(VIEW_KEY, JSON.stringify(memoryView));
    } catch {
      /* quota / private mode: the in-memory record keeps heartbeats going */
    }
  }
  sendHeartbeat(memoryView);
}

export function clearViewedSession(): void {
  memoryView = null;
  const s = store();
  if (!s) return;
  try {
    s.removeItem(VIEW_KEY);
  } catch {
    /* ignore */
  }
}

function sendHeartbeat(viewed: ViewedSession): void {
  void api
    .post(`/projects/${encodeURIComponent(viewed.project)}/session-view`, { session_id: viewed.id })
    .catch((err: unknown) => {
      // A 404 means the conversation (or its project) is gone — a tab that
      // outlived what it was showing. Drop the record so the heartbeat stops
      // retrying an id that can never resolve; any other failure (offline,
      // expired token, restart mid-flight) is transient and keeps retrying.
      if (err instanceof ApiError && err.status === 404) clearViewedSession();
    });
}

/**
 * Send one heartbeat for the stored conversation. Fire-and-forget: presence is
 * a side channel, so a failure must never surface in the UI or block the
 * caller.
 */
export function reportViewHeartbeat(): void {
  const viewed = viewedSession();
  if (viewed) sendHeartbeat(viewed);
}

/**
 * Start the heartbeat for this tab and return a stop function for effect
 * cleanup. Reports once right away so a page reload re-registers immediately
 * instead of after the first interval.
 */
export function startViewHeartbeat(intervalMs: number = VIEW_HEARTBEAT_MS): () => void {
  reportViewHeartbeat();
  const timer = setInterval(reportViewHeartbeat, intervalMs);
  return () => clearInterval(timer);
}
