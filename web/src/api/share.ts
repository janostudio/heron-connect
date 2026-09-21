import api from './client';

// ── File sharing ─────────────────────────────────────────────
//
// A share turns one file into a link that needs no login: the backend serves
// it from /api/v1/share/<token> outside the management auth check. Everything
// below goes through the normal authenticated client (api automatically sends
// the Authorization header) — only the recipient's GET is unauthenticated.
//
// `url` comes back as a server-relative path ("/api/v1/share/<token>"); it is
// resolved against the current origin when displayed so the copied link is
// absolute and openable by someone else.

export interface ShareInfo {
  token: string;
  url: string;
  project: string;
  path: string;
  file_name: string;
  created_at: number;
}

/** createShare mints a public link for one file in a project work dir. */
export async function createShare(project: string, path: string): Promise<ShareInfo> {
  return api.post<ShareInfo>('/share', { project, path });
}

/** listShares returns existing shares, optionally for a single project. */
export async function listShares(project?: string): Promise<ShareInfo[]> {
  const data = await api.get<{ shares: ShareInfo[] }>(
    '/share',
    project ? { project } : undefined,
  );
  return data?.shares ?? [];
}

/** revokeShare permanently disables a link. */
export async function revokeShare(token: string): Promise<void> {
  await api.delete(`/share/${encodeURIComponent(token)}`);
}

/**
 * absoluteShareURL turns the server-relative share URL into an absolute one.
 * Passing the relative form to someone else would resolve against THEIR
 * origin, so the link must be absolutised before it is copied or shown.
 */
export function absoluteShareURL(relative: string): string {
  if (!relative) return '';
  if (/^https?:\/\//i.test(relative)) return relative;
  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  return origin + (relative.startsWith('/') ? relative : '/' + relative);
}
