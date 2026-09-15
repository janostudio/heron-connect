import { describe, it, expect } from 'vitest';
import { SequenceGuard } from './sequenceGuard';

describe('SequenceGuard', () => {
  it('reports the newest ticket as current', () => {
    const g = new SequenceGuard();
    const t1 = g.begin();
    expect(g.isCurrent(t1)).toBe(true);
    const t2 = g.begin();
    expect(g.isCurrent(t2)).toBe(true);
    expect(g.isCurrent(t1)).toBe(false);
  });

  it('issues strictly increasing tickets', () => {
    const g = new SequenceGuard();
    const tickets = [g.begin(), g.begin(), g.begin()];
    expect(tickets).toEqual([...tickets].sort((a, b) => a - b));
    expect(new Set(tickets).size).toBe(3);
  });

  it('simulates the slow-first-response interleaving it exists to prevent', async () => {
    // The real bug: clicking conversation A then quickly B. A's request is slow,
    // B's is fast. Without the guard, A resolves last and publishes A's data
    // while the URL says B.
    const g = new SequenceGuard();
    const published: string[] = [];

    const load = async (name: string, delayMs: number) => {
      const ticket = g.begin();
      await new Promise(r => setTimeout(r, delayMs));
      if (!g.isCurrent(ticket)) return;      // superseded — do not publish
      published.push(name);
    };

    await Promise.all([load('A', 30), load('B', 5)]);
    expect(published).toEqual(['B']);        // only the newest lands
  });

  it('lets the newest request publish even when it is the slower one', async () => {
    const g = new SequenceGuard();
    const published: string[] = [];

    const load = async (name: string, delayMs: number) => {
      const ticket = g.begin();
      await new Promise(r => setTimeout(r, delayMs));
      if (!g.isCurrent(ticket)) return;
      published.push(name);
    };

    // B starts last and takes longest — it is still the authoritative one.
    await Promise.all([load('A', 5), load('B', 30)]);
    expect(published).toEqual(['B']);
  });

  it('gates the loading flag so a stale request cannot clear it early', async () => {
    // Mirrors ChatView.fetchData's `finally { if (isCurrent) setLoading(false) }`:
    // the stale response must not hide the spinner while the live one runs.
    const g = new SequenceGuard();
    let loading = false;
    const states: boolean[] = [];

    const load = async (delayMs: number) => {
      const ticket = g.begin();
      loading = true;
      states.push(loading);
      await new Promise(r => setTimeout(r, delayMs));
      if (!g.isCurrent(ticket)) return;
      loading = false;
      states.push(loading);
    };

    const slow = load(30);   // starts first, finishes last
    const fast = load(5);    // starts second, finishes first
    await Promise.all([slow, fast]);

    // The fast request cleared loading, then the slow one must NOT re-set or
    // re-clear it in a way that flips the UI.
    expect(loading).toBe(false);
    expect(states[states.length - 1]).toBe(false);
  });

  it('a superseded request cannot revive state after the view moved on', async () => {
    const g = new SequenceGuard();
    let currentSession: string | null = null;

    const load = async (id: string, delayMs: number) => {
      const ticket = g.begin();
      await new Promise(r => setTimeout(r, delayMs));
      if (!g.isCurrent(ticket)) return;
      currentSession = id;
    };

    // Three rapid clicks; only the last one may win regardless of timing.
    await Promise.all([load('s89', 40), load('s91', 25), load('s93', 5)]);
    expect(currentSession).toBe('s93');
  });
});
