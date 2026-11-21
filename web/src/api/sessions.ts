import api from './client';

export interface LastMessage {
  role: string;
  content: string;
  timestamp: string;
}

export interface Session {
  id: string;
  session_key: string;
  name: string;
  platform: string;
  agent_type: string;
  active: boolean;
  live: boolean;
  /** True while a turn is executing (foreground or background agent activity). */
  running?: boolean;
  /** True while the turn is blocked on a permission/AskUserQuestion answer. */
  waiting_permission?: boolean;
  /**
   * True when a message arriving mid-turn interrupts the running turn instead
   * of queueing until it ends. Project-level agent capability, so it is the
   * same for every session of a project — the UI uses it to tell the two busy
   * states apart. Absent/false means queueing.
   */
  interruptible?: boolean;
  /**
   * True when the session is user-pinned. Shown and toggled in the UI. It sorts
   * in the `activity` order (pinned first) and is ignored by the `reading`
   * order — see sortSessions.
   */
  pinned?: boolean;
  /**
   * True while a web client has this conversation open (it heartbeats while
   * the tab shows it). Server-derived, never set by the client.
   */
  viewing?: boolean;
  /**
   * When the last web reader left, recorded server-side when the heartbeat
   * lapses. Absent when the conversation has never been read from the web.
   * Persisted, so the reading order survives a restart.
   */
  view_closed_at?: string;
  /**
   * Who created the session. Absent/empty for ordinary chat sessions;
   * `cron` for a scheduled job's isolated run session (the job runs with
   * session_mode=new_per_run). The list UI hides these by default so a daily
   * or hourly job does not crowd out human conversations.
   */
  origin?: string;
  created_at: string;
  updated_at: string;
  /**
   * Timestamp of the user's most recent message — the list sort key.
   *
   * Deliberately not `updated_at`: that is stamped when the whole turn
   * finishes (assistant reply + every tool call), so a long-running turn
   * makes a session look newer than the user's actual last message.
   * Falls back to `updated_at` server-side when there is no user message yet.
   */
  last_user_at?: string;
  /**
   * Content of the session's FIRST user message, used as the card preview so
   * a card says what the session is about rather than showing the latest line
   * (often an assistant reply or tool output). Empty when there is no message.
   */
  first_user_message?: string;
  history_count: number;
  last_message: LastMessage | null;
  user_name?: string;
  chat_name?: string;
  /**
   * Present only when the list was fetched with a `q` query and the match came
   * from message history rather than metadata: a short excerpt around the hit,
   * shown instead of the last-message preview so the result makes sense.
   */
  match_snippet?: string;
}

export interface SessionDetail extends Session {
  agent_session_id: string;
  history: {
    role: string;
    content: string;
    timestamp: string;
    attachments?: {
      kind: 'image' | 'file';
      name: string;
      mime_type: string;
      path: string;
      size?: number;
    }[];
  }[];
}

// sessionTitle returns the display title: the (auto-generated or user-chosen)
// session name first, then the chat/user it came from, then the ID prefix.
// The backend normalizes placeholder names ("default"/"session") to "".
export function sessionTitle(s: Pick<Session, 'name' | 'user_name' | 'chat_name' | 'id'>): string {
  return s.name || s.user_name || s.chat_name || s.id.slice(0, 8);
}

// sessionSource returns the origin label (user or chat display name) shown
// as secondary context next to the title. Empty when unavailable.
export function sessionSource(s: Pick<Session, 'user_name' | 'chat_name'>): string {
  return s.user_name || s.chat_name || '';
}

// CRON_SESSION_ORIGIN is the Session.origin value the backend sets on sessions
// created by a scheduled job run. Keep in sync with core.OriginCron.
export const CRON_SESSION_ORIGIN = 'cron';

// isCronSession reports whether the session was created by a scheduled run
// rather than by a person talking to the agent.
export function isCronSession(s: Pick<Session, 'origin'>): boolean {
  return s.origin === CRON_SESSION_ORIGIN;
}

// splitByOrigin partitions a session list into human conversations and
// scheduled-run sessions. The list UI hides the latter behind a toggle but
// still reports the hidden count, so the filter is visible rather than silent.
export function splitByOrigin<T extends { origin?: string }>(list: T[]): { human: T[]; cron: T[] } {
  const human: T[] = [];
  const cron: T[] = [];
  for (const s of list) {
    if (isCronSession(s)) cron.push(s);
    else human.push(s);
  }
  return { human, cron };
}

// listSessions fetches the session list. When `q` is set the backend filters
// server-side against metadata AND full message history (returning
// `match_snippet` for history hits); omit it for the plain, poll-friendly list.
export const listSessions = (project: string, q?: string) =>
  api.get<{ sessions: Session[]; active_keys: Record<string, string> }>(
    `/projects/${project}/sessions`,
    q ? { q } : undefined,
  );
export const getSession = (project: string, id: string, historyLimit?: number) =>
  api.get<SessionDetail>(`/projects/${project}/sessions/${id}`, historyLimit ? { history_limit: String(historyLimit) } : undefined);
export const createSession = (project: string, body: { session_key: string; name?: string }) =>
  api.post<{ id: string; session_key: string; name: string }>(`/projects/${project}/sessions`, body);
export const deleteSession = (project: string, id: string) => api.delete(`/projects/${project}/sessions/${id}`);
export const updateSession = (project: string, id: string, body: { name?: string; pinned?: boolean }) =>
  api.patch<{ id: string; name?: string; pinned?: boolean }>(`/projects/${project}/sessions/${id}`, body);
export const switchSession = (project: string, body: { session_key: string; session_id: string }) =>
  api.post(`/projects/${project}/sessions/switch`, body);
export const sendMessage = (project: string, body: { session_key: string; message: string }) =>
  api.post(`/projects/${project}/send`, body);

/**
 * How the session lists are ordered. Both modes are offered in the UI, and the
 * choice is remembered per browser — the two answer different questions:
 *
 * - `reading`: what is being read right now (see sortSessions).
 * - `activity`: the long-standing order — pinned first, then most recent user
 *   message. Kept because it is the better view when the user works through a
 *   fixed set of conversations they pinned themselves.
 */
export type SessionSortMode = 'reading' | 'activity';

/** The mode a fresh browser starts in: the reading order is the newer default. */
export const DEFAULT_SESSION_SORT_MODE: SessionSortMode = 'reading';

/**
 * Orders sessions by what people are reading, most relevant first:
 *
 *   1. being read right now (`viewing` — a web client is open on it)
 *   2. read before and since closed, most recently closed first
 *   3. never read from the web, by the user's most recent message
 *
 * Within each group the order is the user's most recent message, so the list
 * still reads naturally once the reading groups are separated.
 *
 * In this mode pinning is deliberately NOT part of the order: the list answers
 * "what am I (or is anyone) reading", and a pin would push conversations the
 * user has not touched in weeks above the one being read right now. The pin
 * flag still exists — it is shown and toggled in the UI. The `activity` mode
 * below is where pinning still sorts.
 *
 * `last_user_at` rather than `updated_at` is the activity key: the latter is
 * stamped when a whole turn finishes (assistant reply + every tool call), so a
 * long-running turn would make a session look newer than the user's own last
 * message. Both fall back to `created_at` so a session with no user message
 * yet still sorts deterministically. Used consistently across chat and list
 * views.
 */
export function sortSessions<T extends {
  id: string;
  pinned?: boolean;
  viewing?: boolean;
  view_closed_at?: string;
  last_user_at?: string;
  updated_at: string;
  created_at: string;
}>(list: T[], mode: SessionSortMode = DEFAULT_SESSION_SORT_MODE): T[] {
  const activityKey = (s: T) => s.last_user_at || s.updated_at || s.created_at;
  const byActivity = (a: T, b: T) => {
    const aTime = activityKey(a);
    const bTime = activityKey(b);
    return (bTime > aTime ? 1 : bTime < aTime ? -1 : 0);
  };
  // Final tie-break. Two sessions can share every sort key (same message time,
  // or two equally fresh heartbeats), and the server hands the list over in map
  // order — not stable between polls. Without this the tail of the list would
  // shuffle on every refresh, and re-sorting an already-sorted array (what the
  // mode switch does) could disagree with a fresh sort of the raw list.
  const byId = (a: T, b: T) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

  if (mode === 'activity') {
    // The original order: pinned conversations first, then by the user's most
    // recent message. Reading state is ignored here — that is the whole
    // difference between the two modes.
    return [...list].sort((a, b) => {
      const aPin = a.pinned ? 1 : 0;
      const bPin = b.pinned ? 1 : 0;
      if (aPin !== bPin) return bPin - aPin;
      return byActivity(a, b) || byId(a, b);
    });
  }

  // Reading order. Group rank: lower sorts first. A closed session without a
  // timestamp is grouped with the never-read ones, so a malformed payload
  // degrades to the activity order instead of pinning it to the top.
  const rank = (s: T) => (s.viewing ? 0 : s.view_closed_at ? 1 : 2);

  return [...list].sort((a, b) => {
    const aRank = rank(a);
    const bRank = rank(b);
    if (aRank !== bRank) return aRank - bRank;
    if (aRank === 1) {
      const aClosed = a.view_closed_at || '';
      const bClosed = b.view_closed_at || '';
      if (aClosed !== bClosed) return aClosed < bClosed ? 1 : -1;
    }
    return byActivity(a, b) || byId(a, b);
  });
}

/**
 * The newest conversation by activity, ignoring reading state. Auto-select uses
 * this: opening the chat page with no session in the URL should land on the
 * conversation with the latest message, not on whichever one someone happens
 * to have open in another tab.
 */
export function pickLatestActivity<T extends { id: string; viewing?: boolean; view_closed_at?: string; last_user_at?: string; updated_at: string; created_at: string }>(
  list: T[],
): T | undefined {
  let best: T | undefined;
  for (const s of list) {
    if (!best || (s.last_user_at || s.updated_at || s.created_at) > (best.last_user_at || best.updated_at || best.created_at)) {
      best = s;
    }
  }
  return best;
}
