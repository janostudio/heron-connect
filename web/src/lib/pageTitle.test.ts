import { describe, it, expect } from 'vitest';
import { composeTitle, pickTitleIcon, DEFAULT_TITLE } from './pageTitle';

describe('composeTitle', () => {
  it('shows the conversation name alone when nothing needs attention', () => {
    expect(composeTitle('Fix the upload limit')).toBe('Fix the upload limit');
  });

  // The tab strip is ~12–14 CJK characters wide: the name has to start at the
  // front, so the icon is a prefix and nothing else is appended.
  it('puts the icon in front of the name', () => {
    expect(composeTitle('Fix the upload limit', 'running')).toBe('⏳ Fix the upload limit');
    expect(composeTitle('Fix the upload limit', 'unread')).toBe('● Fix the upload limit');
    expect(composeTitle('Fix the upload limit', 'waiting')).toBe('⚠️ Fix the upload limit');
  });

  it('trims surrounding whitespace', () => {
    expect(composeTitle('  会话名  ')).toBe('会话名');
  });

  it('falls back to the product title when there is no name yet', () => {
    expect(composeTitle('')).toBe(DEFAULT_TITLE);
    expect(composeTitle('   ')).toBe(DEFAULT_TITLE);
    expect(composeTitle('   ', 'running')).toBe(DEFAULT_TITLE);
  });
});

describe('pickTitleIcon', () => {
  // Only one glyph fits, so the states have to rank: an approval prompt blocks
  // the turn, an unread result is waiting to be read, running is merely in
  // flight.
  it('prefers the approval prompt over everything', () => {
    expect(pickTitleIcon({ waiting: true, unread: true, running: true })).toBe('waiting');
    expect(pickTitleIcon({ waiting: true, running: true })).toBe('waiting');
  });

  it('prefers unread over running', () => {
    expect(pickTitleIcon({ unread: true, running: true })).toBe('unread');
  });

  it('shows running when nothing else applies', () => {
    expect(pickTitleIcon({ running: true })).toBe('running');
  });

  it('shows nothing when idle', () => {
    expect(pickTitleIcon({})).toBe('');
    expect(pickTitleIcon({ waiting: false, unread: false, running: false })).toBe('');
  });
});
