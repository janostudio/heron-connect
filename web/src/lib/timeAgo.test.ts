import { describe, it, expect } from 'vitest';
import { timeAgo } from './timeAgo';

// Fallback-only translator: the helper must keep working when a locale has not
// translated the two relative keys yet.
const t = (_key: string, options?: { defaultValue?: string }) => options?.defaultValue ?? _key;

const now = new Date(2026, 10, 21, 12, 0, 0); // 2026-11-21 12:00 local
const ago = (ms: number) => new Date(now.getTime() - ms).toISOString();
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

describe('timeAgo', () => {
  it('returns empty for a missing or unparseable value', () => {
    expect(timeAgo('', t, now)).toBe('');
    expect(timeAgo('not-a-date', t, now)).toBe('');
  });

  it('reads a fresh timestamp as just now', () => {
    expect(timeAgo(ago(30_000), t, now)).toBe('just now');
  });

  // Server and browser clocks are not in sync; a future timestamp must not
  // render as "-2m".
  it('treats a future timestamp as just now', () => {
    expect(timeAgo(new Date(now.getTime() + 5 * MIN).toISOString(), t, now)).toBe('just now');
  });

  it('counts minutes up to an hour', () => {
    expect(timeAgo(ago(5 * MIN), t, now)).toBe('5m');
    expect(timeAgo(ago(59 * MIN), t, now)).toBe('59m');
  });

  it('counts hours up to a day', () => {
    expect(timeAgo(ago(HOUR), t, now)).toBe('1h');
    expect(timeAgo(ago(23 * HOUR), t, now)).toBe('23h');
  });

  it('names the previous calendar day', () => {
    // Same wall-clock time yesterday is 24h back, which would otherwise read
    // as "1d" — "yesterday" is the clearer label.
    expect(timeAgo(ago(DAY), t, now)).toBe('yesterday');
    // Under 24h the hour count is more precise than the day label, even when it
    // crosses midnight (23:30 yesterday → 12:00 today reads as "12h").
    expect(timeAgo(new Date(2026, 10, 20, 23, 30).toISOString(), t, now)).toBe('12h');
  });

  it('counts days inside a week', () => {
    expect(timeAgo(new Date(2026, 10, 18, 12, 0).toISOString(), t, now)).toBe('3d');
    expect(timeAgo(new Date(2026, 10, 15, 12, 0).toISOString(), t, now)).toBe('6d');
  });

  // Beyond a week the gap stops being readable: last week and last spring both
  // collapse into "Nd", so show the date instead.
  it('shows a month-day date beyond a week in the same year', () => {
    expect(timeAgo(new Date(2026, 10, 11, 12, 0).toISOString(), t, now)).toBe('11-11');
    expect(timeAgo(new Date(2026, 0, 5, 12, 0).toISOString(), t, now)).toBe('01-05');
  });

  it('includes the year once the date is from another year', () => {
    expect(timeAgo(new Date(2025, 10, 21, 12, 0).toISOString(), t, now)).toBe('2025-11-21');
  });
});
