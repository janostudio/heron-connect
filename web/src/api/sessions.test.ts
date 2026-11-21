import { describe, it, expect } from 'vitest';
import { sortSessions, pickLatestActivity, splitByOrigin, isCronSession, CRON_SESSION_ORIGIN } from './sessions';

// Minimal shape sortSessions cares about.
type Row = {
  id: string;
  pinned?: boolean;
  viewing?: boolean;
  view_closed_at?: string;
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
  // The list answers "what is being read"; a pin says "I care about this" and
  // used to win over everything. It no longer sorts: a conversation pinned
  // weeks ago must not outrank the one open in a tab right now. The flag is
  // still shown and toggled in the UI.
  it('ignores the pin flag entirely', () => {
    const rows = [
      row({ id: 'fresh', last_user_at: '2026-05-01T00:00:00Z' }),
      row({ id: 'pinned', pinned: true, last_user_at: '2026-01-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['fresh', 'pinned']);
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

// Reading order: what is open now, then what was closed most recently, then
// everything else by message activity. These three groups are the whole point
// of the presence tracking in core/views.go + lib/sessionView.ts.
describe('sortSessions reading order', () => {
  it('puts conversations being read above closed and unread ones', () => {
    const rows = [
      row({ id: 'unread-newest', last_user_at: '2026-06-01T00:00:00Z' }),
      row({ id: 'viewing-old', viewing: true, last_user_at: '2026-01-01T00:00:00Z' }),
      row({ id: 'closed', view_closed_at: '2026-05-01T00:00:00Z', last_user_at: '2026-02-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['viewing-old', 'closed', 'unread-newest']);
  });

  it('orders the closed group by close time, newest first', () => {
    const rows = [
      row({ id: 'closed-long-ago', view_closed_at: '2026-07-01T00:00:00Z' }),
      row({ id: 'closed-just-now', view_closed_at: '2026-08-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['closed-just-now', 'closed-long-ago']);
  });

  // Close time decides before activity: a conversation read a minute ago and
  // silent for a week still belongs above one that was never read but received
  // a message yesterday.
  it('keeps close time above message time for the closed group', () => {
    const rows = [
      row({ id: 'never-read-loud', last_user_at: '2026-08-02T00:00:00Z' }),
      row({ id: 'read-quiet', view_closed_at: '2026-08-01T00:00:00Z', last_user_at: '2026-07-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['read-quiet', 'never-read-loud']);
  });

  it('breaks ties inside a group by activity', () => {
    const rows = [
      row({ id: 'viewing-older', viewing: true, last_user_at: '2026-01-01T00:00:00Z' }),
      row({ id: 'viewing-newer', viewing: true, last_user_at: '2026-05-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['viewing-newer', 'viewing-older']);
  });

  // First run, or a session store with no presence yet: the order must be the
  // plain activity order users already know.
  it('reproduces the activity order when nothing has been read', () => {
    const rows = [
      row({ id: 'old', last_user_at: '2026-01-01T00:00:00Z' }),
      row({ id: 'new', last_user_at: '2026-06-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['new', 'old']);
  });

  it('treats an empty close timestamp as unread rather than preferring it', () => {
    const rows = [
      row({ id: 'empty-close', view_closed_at: '', last_user_at: '2026-01-01T00:00:00Z' }),
      row({ id: 'viewing', viewing: true, last_user_at: '2026-01-01T00:00:00Z' }),
    ];
    expect(ids(rows)).toEqual(['viewing', 'empty-close']);
  });

  it('does not mutate the input array', () => {
    const rows = [row({ id: 'a' }), row({ id: 'b', viewing: true })];
    const before = rows.map((r) => r.id);
    sortSessions(rows);
    expect(rows.map((r) => r.id)).toEqual(before);
  });
});

// Auto-select on the chat page: "open the newest conversation" must stay an
// activity question, because the head of the list is now whatever is open in
// some tab.
describe('pickLatestActivity', () => {
  it('returns the newest conversation regardless of reading state', () => {
    const rows = [
      row({ id: 'viewing', viewing: true, last_user_at: '2026-01-01T00:00:00Z' }),
      row({ id: 'newest', last_user_at: '2026-06-01T00:00:00Z' }),
    ];
    expect(pickLatestActivity(rows)?.id).toBe('newest');
  });

  it('returns undefined for an empty list', () => {
    expect(pickLatestActivity([] as Row[])).toBeUndefined();
  });
});

// The other mode: the order the lists used before reading state existed —
// pinned conversations first, then the most recent user message. Reading state
// is ignored here, which is exactly what makes it a separate mode instead of a
// tweak of the reading order.
describe('sortSessions in the activity mode', () => {
  const activityIds = (rows: Row[]) => sortSessions(rows, 'activity').map((r) => r.id);

  it('puts pinned sessions first regardless of message time', () => {
    const rows = [
      row({ id: 'fresh', last_user_at: '2026-06-01T00:00:00Z' }),
      row({ id: 'pinned', pinned: true, last_user_at: '2026-01-01T00:00:00Z' }),
    ];
    expect(activityIds(rows)).toEqual(['pinned', 'fresh']);
  });

  it('ignores reading state entirely', () => {
    const rows = [
      row({ id: 'viewing', viewing: true, last_user_at: '2026-01-01T00:00:00Z' }),
      row({ id: 'closed', view_closed_at: '2026-08-01T00:00:00Z', last_user_at: '2026-02-01T00:00:00Z' }),
      row({ id: 'loud', last_user_at: '2026-06-01T00:00:00Z' }),
    ];
    expect(activityIds(rows)).toEqual(['loud', 'closed', 'viewing']);
  });

  it('keeps the pin flag above reading state', () => {
    const rows = [
      row({ id: 'viewing', viewing: true, last_user_at: '2026-06-01T00:00:00Z' }),
      row({ id: 'pinned', pinned: true, last_user_at: '2026-01-01T00:00:00Z' }),
    ];
    expect(activityIds(rows)).toEqual(['pinned', 'viewing']);
  });

  it('does not mutate the input array', () => {
    const rows = [row({ id: 'a', viewing: true }), row({ id: 'b', pinned: true })];
    const before = rows.map((r) => r.id);
    sortSessions(rows, 'activity');
    expect(rows.map((r) => r.id)).toEqual(before);
  });
});

// The server hands the list over in map order, which is not stable between the
// 5s polls. Two sessions can share every sort key, so the comparator needs a
// final deterministic tie-break or the tail of the list shuffles on refresh —
// and switching modes (which re-sorts the array already in state) could
// disagree with a fresh sort of the raw list.
describe('sortSessions tie-breaking', () => {
  it('orders exact ties by id in the reading mode', () => {
    const a = row({ id: 'fs_a', viewing: true, last_user_at: '2026-05-01T00:00:00Z' });
    const b = row({ id: 'fs_b', viewing: true, last_user_at: '2026-05-01T00:00:00Z' });

    expect(ids([a, b])).toEqual(['fs_a', 'fs_b']);
    expect(ids([b, a])).toEqual(['fs_a', 'fs_b']);
  });

  it('orders exact ties by id in the activity mode', () => {
    const a = row({ id: 'fs_a', last_user_at: '2026-05-01T00:00:00Z' });
    const b = row({ id: 'fs_b', last_user_at: '2026-05-01T00:00:00Z' });

    const inOrder = sortSessions([a, b], 'activity').map((r) => r.id);
    const reversed = sortSessions([b, a], 'activity').map((r) => r.id);
    expect(inOrder).toEqual(['fs_a', 'fs_b']);
    expect(reversed).toEqual(inOrder);
  });

  // Re-sorting an already-sorted list must be a no-op — that is what makes the
  // in-place mode switch equivalent to sorting the raw list again.
  it('is idempotent', () => {
    const rows = [
      row({ id: 'fs_c', viewing: true, last_user_at: '2026-03-01T00:00:00Z' }),
      row({ id: 'fs_a', view_closed_at: '2026-06-01T00:00:00Z', last_user_at: '2026-02-01T00:00:00Z' }),
      row({ id: 'fs_b', last_user_at: '2026-05-01T00:00:00Z' }),
    ];
    const once = sortSessions(rows);
    expect(sortSessions(once).map((r) => r.id)).toEqual(once.map((r) => r.id));
  });
});
