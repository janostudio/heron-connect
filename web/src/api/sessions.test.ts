import { describe, it, expect } from 'vitest';
import { sortSessions, splitByOrigin, isCronSession, CRON_SESSION_ORIGIN } from './sessions';

// Minimal shape sortSessions cares about.
type Row = {
  id: string;
  pinned?: boolean;
  last_user_at?: string;
  updated_at: string;
  created_at: string;
};

const row = (over: Partial<Row> & { id: string }): Row => ({
  updated_at: '2026-01-01T00:00:00Z',
  created_at: '2026-01-01T00:00:00Z',
  ...over,
});

const ids = (rows: Row[]) => sortSessions(rows).map((r) => r.id);

describe('sortSessions', () => {
  it('puts pinned sessions first', () => {
    const rows = [
      row({ id: 'fresh', last_user_at: '2026-05-01T00:00:00Z' }),
      row({ id: 'pinned', pinned: true, last_user_at: '2026-01-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['pinned', 'fresh']);
  });

  it('orders by last_user_at, newest first', () => {
    const rows = [
      row({ id: 'old', last_user_at: '2026-01-01T00:00:00Z' }),
      row({ id: 'new', last_user_at: '2026-06-01T00:00:00Z' }),
      row({ id: 'mid', last_user_at: '2026-03-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['new', 'mid', 'old']);
  });

  // The whole point of the field: a session whose turn is still running must
  // NOT float to the top just because updated_at keeps moving — only the
  // user's own message time counts.
  it('ignores updated_at when last_user_at is present', () => {
    const rows = [
      // Long-running turn: updated_at is the most recent of all, but the user
      // last spoke long ago.
      row({ id: 'busy-old', last_user_at: '2026-01-01T00:00:00Z', updated_at: '2026-12-31T00:00:00Z' }),
      row({ id: 'quiet-new', last_user_at: '2026-06-01T00:00:00Z', updated_at: '2026-06-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['quiet-new', 'busy-old']);
  });

  it('falls back to updated_at when last_user_at is absent', () => {
    const rows = [
      row({ id: 'no-user-msg', updated_at: '2026-07-01T00:00:00Z' }),
      row({ id: 'older', last_user_at: '2026-02-01T00:00:00Z' }),
    ];
    // no-user-msg has the later timestamp, so it leads.
    expect(ids(rows)).toEqual(['no-user-msg', 'older']);
  });

  it('falls back to created_at when both times are empty', () => {
    const rows = [
      { id: 'a', last_user_at: '', updated_at: '', created_at: '2026-02-01T00:00:00Z' },
      { id: 'b', last_user_at: '', updated_at: '', created_at: '2026-05-01T00:00:00Z' },
    ];
    expect(ids(rows)).toEqual(['b', 'a']);
  });

  it('does not mutate the input array', () => {
    const rows = [
      row({ id: 'old', last_user_at: '2026-01-01T00:00:00Z' }),
      row({ id: 'new', last_user_at: '2026-06-01T00:00:00Z' }),
    ];
    const before = rows.map((r) => r.id);
    sortSessions(rows);
    expect(rows.map((r) => r.id)).toEqual(before);
  });
});

describe('splitByOrigin', () => {
  it('separates scheduled-run sessions from human conversations', () => {
    const { human, cron } = splitByOrigin([
      { id: 'chat', origin: '' },
      { id: 'job', origin: CRON_SESSION_ORIGIN },
      { id: 'legacy-chat' },
    ]);
    expect(human.map((s) => s.id)).toEqual(['chat', 'legacy-chat']);
    expect(cron.map((s) => s.id)).toEqual(['job']);
  });

  it('treats an unknown origin as human so the toggle never hides it', () => {
    const { human, cron } = splitByOrigin([{ id: 'x', origin: 'some-future-origin' }]);
    expect(human.map((s) => s.id)).toEqual(['x']);
    expect(cron).toEqual([]);
  });

  it('preserves input order inside each bucket', () => {
    const { human, cron } = splitByOrigin([
      { id: 'c1', origin: CRON_SESSION_ORIGIN },
      { id: 'h1' },
      { id: 'c2', origin: CRON_SESSION_ORIGIN },
      { id: 'h2' },
    ]);
    expect(human.map((s) => s.id)).toEqual(['h1', 'h2']);
    expect(cron.map((s) => s.id)).toEqual(['c1', 'c2']);
  });

  it('does not mutate the input array', () => {
    const rows = [{ id: 'a' }, { id: 'b', origin: CRON_SESSION_ORIGIN }];
    const before = rows.map((r) => r.id);
    splitByOrigin(rows);
    expect(rows.map((r) => r.id)).toEqual(before);
  });
});

describe('isCronSession', () => {
  it('is true only for the cron origin', () => {
    expect(isCronSession({ origin: CRON_SESSION_ORIGIN })).toBe(true);
    expect(isCronSession({})).toBe(false);
    expect(isCronSession({ origin: '' })).toBe(false);
  });
});
