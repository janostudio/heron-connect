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
  /** True when the session is user-pinned (shown first in lists). */
  pinned?: boolean;
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

// sortSessions orders sessions with pinned ones first, then by the user's
// most recent message (`last_user_at`) — NOT `updated_at`, which is stamped
// at turn completion and would float long-running sessions to the top even
// though the user has not said anything new. Both fall back to `created_at`
// so a session with no user message yet still sorts deterministically. Used
// consistently across chat and list views.
export function sortSessions<T extends { pinned?: boolean; last_user_at?: string; updated_at: string; created_at: string }>(list: T[]): T[] {
  return [...list].sort((a, b) => {
    const aPin = a.pinned ? 1 : 0;
    const bPin = b.pinned ? 1 : 0;
    if (aPin !== bPin) return bPin - aPin;
    const aTime = a.last_user_at || a.updated_at || a.created_at;
    const bTime = b.last_user_at || b.updated_at || b.created_at;
    return (bTime > aTime ? 1 : bTime < aTime ? -1 : 0);
  });
}
