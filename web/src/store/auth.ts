import { create } from 'zustand';
import { api } from '@/api/client';
import { loginSession, logoutSession, probeSession } from '@/api/auth';

export const TOKEN_STORAGE_KEY = 'cc_token';
export const SERVER_URL_STORAGE_KEY = 'cc_server_url';
/**
 * Holds the exact token value the server last refused (a 401). Storing the value
 * rather than a boolean means an unchanged token stays marked as refused across
 * reloads without retrying it on every page load, while logging in with a new
 * token clears the mark implicitly.
 */
export const REJECTED_TOKEN_STORAGE_KEY = 'cc_token_rejected';

interface AuthState {
  token: string;
  serverUrl: string;
  isAuthenticated: boolean;
  login: (token: string, serverUrl?: string) => Promise<void>;
  logout: () => void;
  rejectToken: () => void;
  /** probe restores a cookie-only session after localStorage is gone. */
  probe: () => Promise<void>;
  init: () => void;
}

/** storedToken returns the saved credential so the login form can prefill it. */
export function storedToken(): string {
  try {
    return localStorage.getItem(TOKEN_STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

export const useAuthStore = create<AuthState>((set) => ({
  token: '',
  serverUrl: '',
  isAuthenticated: false,
  login: async (token: string, serverUrl?: string) => {
    api.setToken(token);
    localStorage.setItem(TOKEN_STORAGE_KEY, token);
    // A successful login proves this token works, so an earlier rejection is
    // stale by definition.
    localStorage.removeItem(REJECTED_TOKEN_STORAGE_KEY);
    if (serverUrl) localStorage.setItem(SERVER_URL_STORAGE_KEY, serverUrl);
    set({ token, serverUrl: serverUrl || '', isAuthenticated: true });
    // Persist the login as a cookie too. localStorage can be evicted by the
    // browser after a few days idle; the cookie is what makes the next visit
    // skip the login page. A failure here is not fatal — the Bearer token still
    // covers this browsing session.
    try {
      await loginSession(token);
    } catch {
      /* keep the in-memory login */
    }
  },
  logout: () => {
    api.setToken('');
    localStorage.removeItem(TOKEN_STORAGE_KEY);
    localStorage.removeItem(SERVER_URL_STORAGE_KEY);
    localStorage.removeItem(REJECTED_TOKEN_STORAGE_KEY);
    set({ token: '', serverUrl: '', isAuthenticated: false });
    void logoutSession().catch(() => {});
  },
  /**
   * rejectToken handles a 401. The credential is wrong — but that is the ONLY
   * thing that invalidates it: nothing on either side expires a token, so it
   * stays valid for as long as it matches the server's configured value. The
   * refused value is kept in storage so the login form can prefill it and the
   * user can compare it with config.toml instead of retyping from memory.
   *
   * The refused value is the token that was actually sent (api.getToken()), not
   * whatever is in storage: a login attempt with a different token must not
   * condemn the stored one, which may still be valid.
   */
  rejectToken: () => {
    const refused = api.getToken() || storedToken();
    if (refused) localStorage.setItem(REJECTED_TOKEN_STORAGE_KEY, refused);
    api.setToken('');
    set({ token: '', isAuthenticated: false });
    // The cookie derived from the same token cannot be valid either.
    void logoutSession().catch(() => {});
  },
  /**
   * probe asks the server whether this browser still holds a valid session
   * cookie. It is the fallback for the case that makes token-only logins feel
   * like they expire: the browser evicted localStorage after a few idle days,
   * but the login cookie is still there.
   */
  probe: async () => {
    if (await probeSession()) {
      set({ isAuthenticated: true });
    }
  },
  init: () => {
    const token = localStorage.getItem(TOKEN_STORAGE_KEY) || '';
    const serverUrl = localStorage.getItem(SERVER_URL_STORAGE_KEY) || '';
    const rejected = localStorage.getItem(REJECTED_TOKEN_STORAGE_KEY) || '';
    // A token the server already refused is not retried on load: that would
    // bounce the user through the protected routes on every refresh. It stays
    // in storage for the login form to prefill.
    if (token && token !== rejected) {
      api.setToken(token);
      set({ token, serverUrl, isAuthenticated: true });
    }
  },
}));
