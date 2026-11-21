import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

// The api client is the only network dependency here; presence must never
// surface its failures, so the tests assert on the call, not on responses.
const post = vi.fn((_path: string, _body: unknown) => Promise.resolve({}));
vi.mock('@/api/client', () => ({ default: { post: (path: string, body: unknown) => post(path, body) } }));

import {
  viewedSession, setViewedSession, clearViewedSession, reportViewHeartbeat, startViewHeartbeat,
  VIEW_HEARTBEAT_MS,
} from './sessionView';

// Minimal sessionStorage stand-in: node has none, and the module must work with
// whichever storage the tab actually provides.
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
  (globalThis as any).sessionStorage = new MemoryStorage();
  post.mockClear();
});

afterEach(() => {
  delete (globalThis as any).sessionStorage;
  vi.useRealTimers();
});

describe('viewed session record', () => {
  it('starts empty', () => {
    expect(viewedSession()).toBeNull();
  });

  it('remembers the conversation the tab has open', () => {
    setViewedSession('proj-a', 's3');

    expect(viewedSession()).toEqual({ project: 'proj-a', id: 's3' });
  });

  // The project has to travel with the id: session ids are only unique within
  // one project ("s3" exists in every project).
  it('reports the heartbeat to the owning project', () => {
    setViewedSession('proj-a', 's3');

    expect(post).toHaveBeenCalledWith('/projects/proj-a/session-view', { session_id: 's3' });
  });

  it('replaces the previous conversation when another one is opened', () => {
    setViewedSession('proj-a', 's3');
    setViewedSession('proj-a', 's4');

    expect(viewedSession()).toEqual({ project: 'proj-a', id: 's4' });
  });

  it('forgets the conversation on clear', () => {
    setViewedSession('proj-a', 's3');
    clearViewedSession();

    expect(viewedSession()).toBeNull();
  });

  it('ignores a corrupt record instead of throwing', () => {
    sessionStorage.setItem('cc.viewing', '{not json');

    expect(viewedSession()).toBeNull();
  });

  it('ignores a record with missing parts', () => {
    sessionStorage.setItem('cc.viewing', JSON.stringify({ project: 'proj-a' }));

    expect(viewedSession()).toBeNull();
  });
});

describe('view heartbeat', () => {
  it('sends nothing when no conversation is open', () => {
    reportViewHeartbeat();

    expect(post).not.toHaveBeenCalled();
  });

  it('reports immediately when started and again on every tick', () => {
    vi.useFakeTimers();
    setViewedSession('proj-a', 's3');
    post.mockClear();

    const stop = startViewHeartbeat();
    expect(post).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(VIEW_HEARTBEAT_MS);
    expect(post).toHaveBeenCalledTimes(2);

    stop();
    vi.advanceTimersByTime(VIEW_HEARTBEAT_MS * 3);
    expect(post).toHaveBeenCalledTimes(2);
  });

  // Presence is a side channel: a rejected heartbeat (offline, expired token,
  // project deleted) must not become an unhandled rejection.
  it('swallows a failed heartbeat', async () => {
    post.mockImplementationOnce(() => Promise.reject(new Error('offline')));
    setViewedSession('proj-a', 's3');

    await expect(Promise.resolve()).resolves.toBeUndefined();
  });

  it('does nothing when the tab storage is unavailable', () => {
    delete (globalThis as any).sessionStorage;

    expect(() => setViewedSession('proj-a', 's3')).not.toThrow();
    expect(viewedSession()).toBeNull();
    expect(post).not.toHaveBeenCalled();
  });
});
