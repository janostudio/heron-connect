// Per-connection Web admin session key.
//
// The Web admin UI originally used a single shared key `bridge:web-admin:<project>`
// for every browser tab/machine. That meant multiple Web clients sending messages
// concurrently all fought over the SAME heron Session binding (agent_session_id),
// producing `interactive session mismatch` → recycle → killed process → empty reply
// (see heron-connect Web session-clash bug).
//
// Fix: scope the key per connection with a unique suffix, so each browser tab and
// each machine gets its own isolated Session — exactly like WeCom isolates per user.
// A UUID generated at module load is unique per tab (fresh instance) and per machine.
// The session list still shows all sessions for the project, so histories remain
// browsable across connections.

function newConnID(): string {
  const c = (globalThis as any).crypto;
  if (c && typeof c.randomUUID === 'function') return c.randomUUID();
  return `wc-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

// Stable for the lifetime of this page load (one per tab/machine).
export const WEB_CONN_ID: string = newConnID();

export function webAdminSessionKey(project: string): string {
  if (!project) return '';
  return `bridge:web-admin:${project}:${WEB_CONN_ID}`;
}

// newConvKey mints a fresh, unique session_key for a single Web conversation
// under the given project. Unlike webAdminSessionKey (one per tab, shared by
// every conversation in that tab — the cause of cross-conversation session
// mixing), each call returns a distinct key so every Web conversation maps to
// its own agent session. Matches core.MintWebSessionKey's shape on the backend.
export function newConvKey(project: string): string {
  if (!project) return '';
  const id = newConnID().replace(/^wc-/, '');
  return `bridge:web-admin:${project}:conv-${id}`;
}
