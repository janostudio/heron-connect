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
  /** True when the session is user-pinned (shown first in lists). */
  pinned?: boolean;
  created_at: string;
  updated_at: string;
  history_count: number;
  last_message: LastMessage | null;
  user_name?: string;
  chat_name?: string;
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

export const listSessions = (project: string) =>
  api.get<{ sessions: Session[]; active_keys: Record<string, string> }>(`/projects/${project}/sessions`);
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

// sortSessions orders sessions with pinned ones first, then by most-recently
// updated (falling back to created). Used consistently across chat and list views.
export function sortSessions<T extends { pinned?: boolean; updated_at: string; created_at: string }>(list: T[]): T[] {
  return [...list].sort((a, b) => {
    const aPin = a.pinned ? 1 : 0;
    const bPin = b.pinned ? 1 : 0;
    if (aPin !== bPin) return bPin - aPin;
    const aTime = a.updated_at || a.created_at;
    const bTime = b.updated_at || b.created_at;
    return (bTime > aTime ? 1 : bTime < aTime ? -1 : 0);
  });
}
