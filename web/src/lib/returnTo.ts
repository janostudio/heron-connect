// Where to send the user after they log in.
//
// Before this existed, every successful login landed on the dashboard (`/`)
// regardless of where the user came from. Being bounced to `/login` (the token
// no longer matched, or the browser dropped both localStorage and the session
// cookie) therefore also meant losing the conversation that was open: the user
// had to find it again by hand in the session list.
//
// Nothing here is security-sensitive: the value only selects a route inside the
// SPA, and every route it can name sits behind the same auth guard.
//
// Resolution order, strongest first:
//   1. `?next=` on the login URL — survives a reload of the login page, which is
//      the shape both the guard redirect and the token URL produce.
//   2. `location.state.from` — what the guard recorded for this redirect.
//   3. The last protected route the user visited — covers the case where the
//      user navigates to `/login` directly (bookmark, or the login page was
//      reloaded after the query string was gone).
//   4. `/`.

const RETURN_PATH_KEY = 'cc_return_path';

export const LOGIN_PATH = '/login';

/**
 * acceptable reports whether `path` is a usable in-app route.
 *
 * Absolute URLs and protocol-relative targets are rejected so a crafted `?next=`
 * cannot turn the login page into an open redirect, and `/login` itself is
 * rejected because redirecting there is a loop.
 */
function acceptable(path: string | null | undefined): path is string {
  if (!path || !path.startsWith('/')) return false;
  if (path.startsWith('//')) return false;
  return path !== LOGIN_PATH && !path.startsWith(`${LOGIN_PATH}?`);
}

/**
 * withoutToken drops a `token` query parameter before a path is persisted.
 *
 * The token is already in localStorage by design; keeping a second copy of it in
 * another storage key would widen the blast radius for no benefit, and the
 * parameter is not needed to restore a route.
 */
export function withoutToken(path: string): string {
  const at = path.indexOf('?');
  if (at < 0) return path;
  const params = new URLSearchParams(path.slice(at + 1));
  if (!params.has('token')) return path;
  params.delete('token');
  const rest = params.toString();
  return rest ? `${path.slice(0, at)}?${rest}` : path.slice(0, at);
}

/**
 * rememberReturnPath stores where the user is now, so a later login can resume
 * there even when the login URL carries no `next`.
 */
export function rememberReturnPath(path: string): void {
  const safe = withoutToken(path);
  if (!acceptable(safe)) return;
  try {
    localStorage.setItem(RETURN_PATH_KEY, safe);
  } catch {
    /* private mode / storage disabled — the in-memory redirect still works */
  }
}

/** returnPath resolves the post-login destination; see the file header. */
export function returnPath(search: string, state: unknown): string {
  const next = new URLSearchParams(search).get('next');
  if (acceptable(next)) return next;

  const from = (state as { from?: unknown } | null | undefined)?.from;
  if (typeof from === 'string' && acceptable(from)) return from;

  try {
    const saved = localStorage.getItem(RETURN_PATH_KEY);
    if (acceptable(saved)) return saved;
  } catch {
    /* fall through to the default */
  }
  return '/';
}
