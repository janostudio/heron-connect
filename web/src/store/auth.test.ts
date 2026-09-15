// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest';

// The store reads/writes localStorage on import-time side effects, so the
// module is re-imported per test group after resetting storage.

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
    return { useAuthStore: mod.useAuthStore, setToken };
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
});
