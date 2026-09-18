// Tests for the message timestamp formatter.
//
// The relative buckets are decided by LOCAL CALENDAR DAY, so every case here
// pins an explicit `now` and constructs the subject with local-time arguments
// (`new Date(y, m, d, h, min)`). That keeps assertions independent of the
// machine's timezone and of when the suite happens to run — the failure mode
// these tests exist to catch (a 23:59 message reading as "today" at 00:01, or
// a DST shift pushing a message into the wrong bucket) only shows up under a
// time-delta implementation.

import { describe, it, expect } from 'vitest';
import { formatMessageTime, fullMessageTime, parseStamp, nowStamp } from './messageTime';

const NOW = new Date(2026, 8, 18, 14, 30); // 2026-09-18 14:30 local
const YESTERDAY_WORD = 'Yesterday';

const fmt = (d: Date) => formatMessageTime(d.toISOString(), 'en-US', YESTERDAY_WORD, NOW);

describe('parseStamp', () => {
  it('accepts RFC3339 and local ISO strings', () => {
    expect(parseStamp('2026-09-18T06:03:00Z')).toBeInstanceOf(Date);
    expect(parseStamp('2026-09-18T14:03:00')).toBeInstanceOf(Date);
  });

  it('returns null for missing or invalid input', () => {
    expect(parseStamp(undefined)).toBeNull();
    expect(parseStamp(null)).toBeNull();
    expect(parseStamp('')).toBeNull();
    expect(parseStamp('garbage')).toBeNull();
  });
});

describe('nowStamp', () => {
  it('produces a parseable timestamp that round-trips to ~now', () => {
    const before = Date.now();
    const parsed = parseStamp(nowStamp());
    expect(parsed).not.toBeNull();
    expect(parsed!.getTime()).toBeGreaterThanOrEqual(before);
    expect(parsed!.getTime()).toBeLessThanOrEqual(Date.now());
  });
});

describe('formatMessageTime — buckets', () => {
  it('shows only the time for today', () => {
    expect(fmt(new Date(2026, 8, 18, 9, 5))).toBe('09:05');
    expect(fmt(new Date(2026, 8, 18, 0, 0))).toBe('00:00');
  });

  it('prefixes "yesterday" for the previous calendar day', () => {
    expect(fmt(new Date(2026, 8, 17, 23, 59))).toBe(`Yesterday 23:59`);
    expect(fmt(new Date(2026, 8, 17, 0, 1))).toBe(`Yesterday 00:01`);
  });

  it('shows a month/day for an earlier day in the same year', () => {
    const out = fmt(new Date(2026, 8, 12, 14, 3));
    expect(out).toMatch(/14:03$/);
    expect(out).toMatch(/12/);
    expect(out).not.toMatch(/2026/); // same year → no year
  });

  it('includes the year for a previous year', () => {
    const out = fmt(new Date(2025, 11, 1, 14, 3));
    expect(out).toMatch(/14:03$/);
    expect(out).toMatch(/2025/);
  });
});

describe('formatMessageTime — boundary conditions', () => {
  it('treats a message one minute before midnight as yesterday just after midnight', () => {
    // The classic time-delta bug: 00:01 is only 2 minutes after 23:59, but they
    // are different calendar days.
    const now = new Date(2026, 8, 18, 0, 1);
    const ts = new Date(2026, 8, 17, 23, 59).toISOString();
    expect(formatMessageTime(ts, 'en-US', YESTERDAY_WORD, now)).toBe('Yesterday 23:59');
  });

  it('keeps a same-day message as today across a DST spring-forward', () => {
    // 2026-03-08 is a US DST transition. 00:30 and 23:30 are the same local
    // day despite the wall-clock day being 23h (or 25h) long.
    const now = new Date(2026, 2, 8, 23, 30);
    const ts = new Date(2026, 2, 8, 0, 30).toISOString();
    expect(formatMessageTime(ts, 'en-US', YESTERDAY_WORD, now)).toBe('00:30');
  });

  it('classifies the day before a DST transition day as yesterday', () => {
    const now = new Date(2026, 2, 8, 12, 0);
    const ts = new Date(2026, 2, 7, 12, 0).toISOString();
    expect(formatMessageTime(ts, 'en-US', YESTERDAY_WORD, now)).toBe('Yesterday 12:00');
  });

  it('renders a UTC instant in local time', () => {
    // Whatever the machine's zone, the formatter must convert, not truncate:
    // a 'Z' instant must not print as its raw UTC wall clock unless local == UTC.
    const ts = '2026-09-18T06:03:00Z';
    const out = formatMessageTime(ts, 'en-US', YESTERDAY_WORD, new Date(2026, 8, 18, 23, 0));
    const expected = new Intl.DateTimeFormat('en-US', {
      hour: '2-digit', minute: '2-digit', hour12: false,
    }).format(new Date(ts));
    expect(out).toBe(expected);
  });

  it('returns null for missing or invalid stamps', () => {
    expect(formatMessageTime(undefined, 'en-US', YESTERDAY_WORD, NOW)).toBeNull();
    expect(formatMessageTime('', 'en-US', YESTERDAY_WORD, NOW)).toBeNull();
    expect(formatMessageTime('not-a-date', 'en-US', YESTERDAY_WORD, NOW)).toBeNull();
  });
});

describe('formatMessageTime — localization', () => {
  it('uses the locale for the date wording', () => {
    const ts = new Date(2026, 8, 12, 14, 3).toISOString();
    const zh = formatMessageTime(ts, 'zh-CN', '昨天', NOW)!;
    const en = formatMessageTime(ts, 'en-US', YESTERDAY_WORD, NOW)!;
    expect(zh).toMatch(/14:03$/);
    expect(en).toMatch(/14:03$/);
    expect(zh).not.toBe(en); // 9月12日 vs Sep 12
  });

  it('uses the injected yesterday word verbatim', () => {
    const ts = new Date(2026, 8, 17, 8, 0).toISOString();
    expect(formatMessageTime(ts, 'zh-CN', '昨天', NOW)).toBe('昨天 08:00');
    expect(formatMessageTime(ts, 'ja-JP', '昨日', NOW)).toBe('昨日 08:00');
  });
});

describe('fullMessageTime', () => {
  it('renders a full date + time for the hover title', () => {
    const out = fullMessageTime(new Date(2026, 8, 12, 14, 3).toISOString(), 'en-US');
    expect(out).toMatch(/2026/);
    expect(out).toMatch(/September/);
  });

  it('returns null for invalid input so the title can be omitted', () => {
    expect(fullMessageTime(undefined, 'en-US')).toBeNull();
    expect(fullMessageTime('nope', 'en-US')).toBeNull();
  });
});
