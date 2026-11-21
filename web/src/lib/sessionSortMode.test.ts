import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { readSortMode, writeSortMode } from './sessionSortMode';

// Minimal localStorage stand-in: node has none, and the module must survive
// whichever storage the browser provides (or none at all).
class MemoryStorage {
  private map = new Map<string, string>();
  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key)! : null;
  }
  setItem(key: string, value: string): void {
    this.map.set(key, value);
  }
  removeItem(key: string): void {
    this.map.delete(key);
  }
  clear(): void {
    this.map.clear();
  }
  key(i: number): string | null {
    return Array.from(this.map.keys())[i] ?? null;
  }
  get length(): number {
    return this.map.size;
  }
}

beforeEach(() => {
  (globalThis as any).localStorage = new MemoryStorage();
});

afterEach(() => {
  delete (globalThis as any).localStorage;
  vi.restoreAllMocks();
});

describe('session sort mode preference', () => {
  it('defaults to the reading order', () => {
    expect(readSortMode()).toBe('reading');
  });

  it('remembers the activity order', () => {
    writeSortMode('activity');

    expect(readSortMode()).toBe('activity');
  });

  it('remembers a switch back to the reading order', () => {
    writeSortMode('activity');
    writeSortMode('reading');

    expect(readSortMode()).toBe('reading');
  });

  // A hand-edited or future value must not leave the list in an order no code
  // path produces.
  it('falls back to the default for an unknown stored value', () => {
    localStorage.setItem('cc.sessions.sortMode', 'whatever');

    expect(readSortMode()).toBe('reading');
  });

  it('falls back to the default when storage is unavailable', () => {
    delete (globalThis as any).localStorage;

    expect(readSortMode()).toBe('reading');
    expect(() => writeSortMode('activity')).not.toThrow();
  });

  it('falls back to the default when storage access throws', () => {
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() { throw new Error('blocked'); },
    });

    expect(readSortMode()).toBe('reading');
    expect(() => writeSortMode('activity')).not.toThrow();

    delete (globalThis as any).localStorage;
  });

  // Quota / private mode: the choice still applies to this page, it just cannot
  // be persisted. Writing must not throw into the caller.
  it('does not throw when the write is rejected', () => {
    (globalThis as any).localStorage = {
      getItem: () => null,
      setItem: () => { throw new Error('quota'); },
      removeItem: () => {},
    };

    expect(() => writeSortMode('activity')).not.toThrow();
  });
});
