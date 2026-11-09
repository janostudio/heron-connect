// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest';

// The store reads/writes localStorage on import-time side effects, so the
// module is re-imported per test group after resetting storage.

// Session endpoints and the cookie probe hit the network; stub them so these
// tests stay focused on state transitions.
vi.mock('@/api/auth', () => ({
  loginSession: vi.fn(() => Promise.resolve()),
  logoutSession: vi.fn(() => Promise.resolve()),
  probeSession: vi.fn(() => Promise.resolve(false)),
}));

describe('useAuthStore', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetModules();
  });

  const load = async () => {
    const mod = await import('./auth');
    // The api client is imported by the store; stub the token setter so these
    // tests stay focused on persistence + state transitions.
    const { api } = await import('@/api/client');
    const setToken = vi.spyOn(api, 'setToken').mockImplementation(() => {});
    return { useAuthStore: mod.useAuthStore, storedToken: mod.storedToken, setToken };
  };

  it('starts unauthenticated', async () => {
    const { useAuthStore } = await load();
    const s = useAuthStore.getState();
    expect(s.isAuthenticated).toBe(false);
    expect(s.token).toBe('');
    expect(s.serverUrl).toBe('');
  });

  it('login stores the token and marks the session authenticated', async () => {
    const { useAuthStore, setToken } = await load();
    useAuthStore.getState().login('tok-123');

    const s = useAuthStore.getState();
    expect(s.isAuthenticated).toBe(true);
    expect(s.token).toBe('tok-123');
    expect(localStorage.getItem('cc_token')).toBe('tok-123');
    expect(setToken).toHaveBeenCalledWith('tok-123');
  });

  it('login persists an optional server url', async () => {
    const { useAuthStore } = await load();
    useAuthStore.getState().login('tok', 'http://host:9820');
    expect(localStorage.getItem('cc_server_url')).toBe('http://host:9820');
    expect(useAuthStore.getState().serverUrl).toBe('http://host:9820');
  });

  it('login without a server url does not clobber the stored one', async () => {
    localStorage.setItem('cc_server_url', 'http://kept');
    const { useAuthStore } = await load();
    useAuthStore.getState().login('tok');
    expect(localStorage.getItem('cc_server_url')).toBe('http://kept');
  });

  it('logout clears state and storage', async () => {
    const { useAuthStore, setToken } = await load();
    useAuthStore.getState().login('tok', 'http://host');
    useAuthStore.getState().logout();

    const s = useAuthStore.getState();
    expect(s.isAuthenticated).toBe(false);
    expect(s.token).toBe('');
    expect(localStorage.getItem('cc_token')).toBeNull();
    expect(localStorage.getItem('cc_server_url')).toBeNull();
    expect(setToken).toHaveBeenLastCalledWith('');
  });

  it('init restores an authenticated session from storage', async () => {
    localStorage.setItem('cc_token', 'persisted');
    localStorage.setItem('cc_server_url', 'http://persisted');
    const { useAuthStore, setToken } = await load();

    useAuthStore.getState().init();
    const s = useAuthStore.getState();
    expect(s.isAuthenticated).toBe(true);
    expect(s.token).toBe('persisted');
    expect(s.serverUrl).toBe('http://persisted');
    expect(setToken).toHaveBeenCalledWith('persisted');
  });

  it('init stays unauthenticated when no token is stored', async () => {
    const { useAuthStore, setToken } = await load();
    useAuthStore.getState().init();
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(setToken).not.toHaveBeenCalled();
  });

  // Nothing expires a token: a 401 means the value no longer matches the
  // server's configured token. The refused value must survive so the login form
  // can prefill it — retyping a long token from memory is the part users
  // actually complain about.
  it('rejectToken keeps the stored token and records the refusal', async () => {
    localStorage.setItem('cc_token', 'refused-tok');
    const { useAuthStore, setToken } = await load();

    useAuthStore.getState().init();
    expect(useAuthStore.getState().isAuthenticated).toBe(true);

    useAuthStore.getState().rejectToken();
    const s = useAuthStore.getState();
    expect(s.isAuthenticated).toBe(false);
    expect(s.token).toBe('');
    expect(localStorage.getItem('cc_token')).toBe('refused-tok');
    expect(localStorage.getItem('cc_token_rejected')).toBe('refused-tok');
    expect(setToken).toHaveBeenLastCalledWith('');
  });

  it('init does not retry a refused token but keeps it for prefill', async () => {
    localStorage.setItem('cc_token', 'refused-tok');
    localStorage.setItem('cc_token_rejected', 'refused-tok');
    const { useAuthStore, storedToken, setToken } = await load();

    useAuthStore.getState().init();
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(setToken).not.toHaveBeenCalled();
    expect(storedToken()).toBe('refused-tok');
    expect(localStorage.getItem('cc_token')).toBe('refused-tok');
  });

  it('init trusts a stored token that differs from the refused one', async () => {
    localStorage.setItem('cc_token', 'new-tok');
    localStorage.setItem('cc_token_rejected', 'old-tok');
    const { useAuthStore, setToken } = await load();

    useAuthStore.getState().init();
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(setToken).toHaveBeenCalledWith('new-tok');
  });

  // A failed attempt with a different token must not condemn the stored one:
  // only the token that was actually sent gets the refusal mark.
  it('rejectToken records the token that was actually refused', async () => {
    localStorage.setItem('cc_token', 'stored-tok');
    const { useAuthStore } = await load();
    const { api } = await import('@/api/client');
    vi.spyOn(api, 'getToken').mockReturnValue('attempted-tok');

    useAuthStore.getState().rejectToken();
    expect(localStorage.getItem('cc_token_rejected')).toBe('attempted-tok');
    expect(localStorage.getItem('cc_token')).toBe('stored-tok');

    // The stored token itself was never refused, so it still restores.
    useAuthStore.getState().init();
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(useAuthStore.getState().token).toBe('stored-tok');
  });

  it('login clears an earlier refusal', async () => {
    localStorage.setItem('cc_token_rejected', 'old-tok');
    const { useAuthStore } = await load();

    useAuthStore.getState().login('fresh-tok');
    expect(localStorage.getItem('cc_token_rejected')).toBeNull();
    expect(localStorage.getItem('cc_token')).toBe('fresh-tok');
  });

  it('logout also clears an earlier refusal', async () => {
    localStorage.setItem('cc_token_rejected', 'old-tok');
    const { useAuthStore } = await load();

    useAuthStore.getState().logout();
    expect(localStorage.getItem('cc_token_rejected')).toBeNull();
  });

  // The reason a login "expires" after a few idle days: the browser evicts
  // localStorage. The session cookie survives, so the probe must be able to
  // restore the session without any token in memory.
  it('probe authenticates from the session cookie alone', async () => {
    const { useAuthStore } = await load();
    const { probeSession } = await import('@/api/auth');
    (probeSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(true);

    await useAuthStore.getState().probe();
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    // No token was involved: nothing gets written to storage.
    expect(localStorage.getItem('cc_token')).toBeNull();
  });

  it('probe leaves the session unauthenticated when the cookie is gone', async () => {
    const { useAuthStore } = await load();
    const { probeSession } = await import('@/api/auth');
    (probeSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(false);

    await useAuthStore.getState().probe();
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });

  it('login asks the server to persist the session cookie', async () => {
    const { useAuthStore } = await load();
    const { loginSession } = await import('@/api/auth');

    await useAuthStore.getState().login('tok-cookie');
    expect(loginSession).toHaveBeenCalledWith('tok-cookie');
  });

  it('logout clears the server-side session cookie too', async () => {
    const { useAuthStore } = await load();
    const { logoutSession } = await import('@/api/auth');

    useAuthStore.getState().logout();
    expect(logoutSession).toHaveBeenCalled();
  });
});
