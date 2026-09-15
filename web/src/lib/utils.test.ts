import { describe, it, expect } from 'vitest';
import { formatUptime, truncate } from './utils';

// Pure formatting helpers. cn() is a clsx passthrough and the localStorage /
// clipboard helpers touch browser globals, so they are covered by manual
// verification rather than here.

describe('formatUptime', () => {
  it('formats seconds only', () => {
    expect(formatUptime(30)).toBe('0m');
    expect(formatUptime(59)).toBe('0m');
    expect(formatUptime(60)).toBe('1m');
    expect(formatUptime(90)).toBe('1m');
  });

  it('formats minutes and hours', () => {
    expect(formatUptime(3600)).toBe('1h 0m');
    expect(formatUptime(3660)).toBe('1h 1m');
    expect(formatUptime(7200 + 1800)).toBe('2h 30m');
  });

  it('formats days when over a day', () => {
    expect(formatUptime(86400)).toBe('1d 0h 0m');
    expect(formatUptime(86400 * 2 + 3600 * 3 + 60 * 5)).toBe('2d 3h 5m');
  });

  it('handles zero', () => {
    expect(formatUptime(0)).toBe('0m');
  });

  it('truncates rather than rounds (floor semantics)', () => {
    expect(formatUptime(119)).toBe('1m');       // 1m59s
    expect(formatUptime(86399)).toBe('23h 59m'); // just under a day
  });

  it('omits the seconds component entirely', () => {
    expect(formatUptime(61)).not.toContain('1s');
  });
});

describe('truncate', () => {
  it('leaves short strings untouched', () => {
    expect(truncate('hello', 10)).toBe('hello');
  });

  it('leaves a string exactly at the limit untouched', () => {
    expect(truncate('hello', 5)).toBe('hello');
  });

  it('cuts longer strings and appends an ellipsis', () => {
    expect(truncate('hello world', 5)).toBe('hello...');
  });

  it('handles an empty string', () => {
    expect(truncate('', 5)).toBe('');
  });

  it('handles a zero limit', () => {
    expect(truncate('hello', 0)).toBe('...');
  });

  it('counts characters, not bytes', () => {
    // Multi-byte content must not be split mid-character.
    expect(truncate('会话标题很长', 3)).toBe('会话标...');
  });
});
