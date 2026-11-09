import { ApiError } from './client';

// Session endpoints: they exchange the configured management token for a
// long-lived HttpOnly cookie. That cookie is the durable half of a login —
// localStorage (where the token is kept for Bearer auth) can be evicted by the
// browser after a few days without interaction, while a first-party HttpOnly
// cookie is not.
//
// These calls bypass the shared ApiClient on purpose: a failed login must not
// trip the global 401 handler (which would mark the stored token as refused),
// and the cookie exchange sends no Authorization header of its own.
async function postSession(path: '/login' | '/logout', body?: unknown): Promise<void> {
  const res = await fetch(`/api/v1${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  });
  if (!res.ok) {
    throw new ApiError(`session ${path} failed`, res.status);
  }
}

// loginSession persists the login as a cookie. Callers treat a failure as
// non-fatal: the Bearer token still works for the current tab, the cookie is
// what keeps the next visit logged in.
export const loginSession = (token: string) => postSession('/login', { token });

// logoutSession clears the cookie on the server side.
export const logoutSession = () => postSession('/logout');

// probeSession asks the server whether this browser still holds a valid session
// cookie. It bypasses the shared ApiClient on purpose: a failed probe is the
// normal "not logged in" case, and routing it through the global 401 handler
// would additionally fire a futile /logout request on every anonymous page load.
export async function probeSession(): Promise<boolean> {
  try {
    const res = await fetch('/api/v1/status', { credentials: 'same-origin' });
    return res.ok;
  } catch {
    return false;
  }
}
