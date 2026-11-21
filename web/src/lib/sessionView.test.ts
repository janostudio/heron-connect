import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

// The api client is the only network dependency here; presence must never
// surface its failures, so the tests assert on the call, not on responses.
// ApiError is re-exported from the mock because the module under test narrows
// on it (a 404 ends the reading; anything else is transient).
const post = vi.fn((_path: string, _body: unknown) => Promise.resolve({}));
vi.mock('@/api/client', () => {
  class ApiError extends Error {
    constructor(message: string, public status: number) {
      super(message);
    }
  }
  return { default: { post: (path: string, body: unknown) => post(path, body) }, ApiError };
});

import { ApiError } from '@/api/client';
import {
  viewedSession, setViewedSession, clearViewedSession, reportViewHeartbeat, startViewHeartbeat,
  VIEW_HEARTBEAT_MS,
} from './sessionView';

const flush = () => new Promise((r) => setTimeout(r, 0));

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
  clearViewedSession(); // drop the module-level fallback left by a previous test
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
  it('keeps the record when a heartbeat fails for a transient reason', async () => {
    post.mockImplementationOnce(() => Promise.reject(new ApiError('upstream down', 500)));
    setViewedSession('proj-a', 's3');
    await flush();

    expect(viewedSession()).toEqual({ project: 'proj-a', id: 's3' });
  });

  // A tab can outlive the conversation it was showing. Without this the tab
  // would retry a deleted session id every 10s for as long as it stays open.
  it('drops the record when the server says the session is gone', async () => {
    post.mockImplementationOnce(() => Promise.reject(new ApiError('session not found', 404)));
    setViewedSession('proj-a', 's3');
    await flush();

    expect(viewedSession()).toBeNull();
  });

  // Storage can be absent or reject writes (private mode, quota, policy). The
  // tab still knows what it is showing, so presence must keep working — it just
  // does not survive a remount.
  it('falls back to memory when the storage write fails', () => {
    (globalThis as any).sessionStorage = {
      getItem: () => null,
      setItem: () => { throw new Error('quota'); },
      removeItem: () => {},
    };

    setViewedSession('proj-a', 's3');

    expect(viewedSession()).toEqual({ project: 'proj-a', id: 's3' });
    expect(post).toHaveBeenCalledWith('/projects/proj-a/session-view', { session_id: 's3' });
  });

  it('falls back to memory when no storage exists at all', () => {
    delete (globalThis as any).sessionStorage;

    expect(() => setViewedSession('proj-a', 's3')).not.toThrow();
    expect(viewedSession()).toEqual({ project: 'proj-a', id: 's3' });
    expect(post).toHaveBeenCalledTimes(1);
  });
});
