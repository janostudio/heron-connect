// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

// Theme resolution + persistence. `resolveTheme` is private, so it is exercised
// through the store's public setTheme/init which is what the app actually calls.

describe('useThemeStore', () => {
  let mqMatches = false;

  beforeEach(() => {
    localStorage.clear();
    vi.resetModules();
    mqMatches = false;
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: mqMatches,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })) as any;
    document.documentElement.classList.remove('dark');
  });

  afterEach(() => {
    document.documentElement.classList.remove('dark');
  });

  const load = async () => (await import('./theme')).useThemeStore;

  it('defaults to dark', async () => {
    const store = await load();
    expect(store.getState().theme).toBe('dark');
    expect(store.getState().resolved).toBe('dark');
  });

  it('setTheme("light") applies the light class and persists', async () => {
    const store = await load();
    store.getState().setTheme('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
    expect(localStorage.getItem('cc_theme')).toBe('light');
    expect(store.getState().resolved).toBe('light');
  });

  it('setTheme("dark") adds the dark class and persists', async () => {
    const store = await load();
    store.getState().setTheme('light');
    store.getState().setTheme('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(localStorage.getItem('cc_theme')).toBe('dark');
  });

  it('setTheme("system") follows the OS preference', async () => {
    mqMatches = true;   // OS says dark
    const store = await load();
    store.getState().setTheme('system');
    expect(store.getState().theme).toBe('system');
    expect(store.getState().resolved).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('setTheme("system") resolves to light when the OS prefers light', async () => {
    mqMatches = false;
    const store = await load();
    store.getState().setTheme('system');
    expect(store.getState().resolved).toBe('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it('init restores a saved theme and applies it', async () => {
    localStorage.setItem('cc_theme', 'light');
    const store = await load();
    store.getState().init();
    expect(store.getState().theme).toBe('light');
    expect(store.getState().resolved).toBe('light');
    expect(document.documentElement.classList.contains('dark')).toBe(false);
  });

  it('init falls back to dark when nothing is stored', async () => {
    const store = await load();
    store.getState().init();
    expect(store.getState().theme).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);
  });

  it('init with a saved system theme consults the OS', async () => {
    mqMatches = true;
    localStorage.setItem('cc_theme', 'system');
    const store = await load();
    store.getState().init();
    expect(store.getState().theme).toBe('system');
    expect(store.getState().resolved).toBe('dark');
  });

  it('toggling between light and dark leaves no stale class', async () => {
    const store = await load();
    for (const t of ['light', 'dark', 'light', 'dark'] as const) {
      store.getState().setTheme(t);
      expect(document.documentElement.classList.contains('dark')).toBe(t === 'dark');
    }
  });
});
