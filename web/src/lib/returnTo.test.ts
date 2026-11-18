/**
 * @vitest-environment jsdom
 */
// Regression tests for post-login resume.
//
// Redirecting to /login used to lose the open conversation: the login page
// always navigated to `/` afterwards, so the user had to find the conversation
// again in the session list. `returnPath` is what the guard, the login page and
// the `/login` redirect all agree on; these tests pin its precedence and the
// open-redirect guards.

import { describe, it, expect, beforeEach } from 'vitest';
import { LOGIN_PATH, rememberReturnPath, returnPath, withoutToken } from './returnTo';

beforeEach(() => {
  localStorage.clear();
});

describe('returnPath precedence', () => {
  it('uses ?next= first, since it survives a login-page reload', () => {
    rememberReturnPath('/sessions');
    expect(returnPath('?next=%2Fchat%2Fdemo%2Fsess-1', null)).toBe('/chat/demo/sess-1');
  });

  it('falls back to the redirect state recorded by the guard', () => {
    expect(returnPath('', { from: '/chat/demo/sess-2' })).toBe('/chat/demo/sess-2');
  });

  it('falls back to the last protected route when the login URL carries nothing', () => {
    rememberReturnPath('/chat/demo/sess-3');
    expect(returnPath('', null)).toBe('/chat/demo/sess-3');
  });

  it('defaults to the dashboard when nothing was remembered', () => {
    expect(returnPath('', null)).toBe('/');
  });

  it('prefers ?next= over the redirect state and the remembered path', () => {
    rememberReturnPath('/sessions');
    expect(returnPath('?next=%2Fcron', { from: '/chat/demo/old' })).toBe('/cron');
  });
});

describe('returnPath rejects unusable targets', () => {
  it('ignores absolute and protocol-relative URLs (no open redirect)', () => {
    expect(returnPath('?next=https%3A%2F%2Fevil.example', null)).toBe('/');
    expect(returnPath('?next=%2F%2Fevil.example', null)).toBe('/');
  });

  it('ignores the login route itself to avoid a redirect loop', () => {
    expect(returnPath(`?next=${encodeURIComponent(LOGIN_PATH)}`, null)).toBe('/');
    expect(returnPath(`?next=${encodeURIComponent(`${LOGIN_PATH}?next=%2Fchat`)}`, null)).toBe('/');
  });

  it('ignores a non-string redirect state', () => {
    expect(returnPath('', { from: 42 })).toBe('/');
  });
});

describe('rememberReturnPath', () => {
  it('drops a token query parameter before persisting', () => {
    rememberReturnPath('/reports?token=secret&path=a.md');
    expect(localStorage.getItem('cc_return_path')).toBe('/reports?path=a.md');
    expect(localStorage.getItem('cc_return_path')).not.toContain('secret');
  });

  it('keeps ordinary query parameters', () => {
    rememberReturnPath('/reports/preview?path=docs%2Freport.md');
    expect(localStorage.getItem('cc_return_path')).toBe('/reports/preview?path=docs%2Freport.md');
  });

  it('does not persist an off-site or login path', () => {
    rememberReturnPath('https://evil.example/x');
    expect(localStorage.getItem('cc_return_path')).toBeNull();
    rememberReturnPath(LOGIN_PATH);
    expect(localStorage.getItem('cc_return_path')).toBeNull();
  });
});

describe('withoutToken', () => {
  it('returns the path untouched when there is no query', () => {
    expect(withoutToken('/chat/demo')).toBe('/chat/demo');
  });

  it('returns the path untouched when the query has no token', () => {
    expect(withoutToken('/chat/demo?a=1')).toBe('/chat/demo?a=1');
  });

  it('removes only the token parameter', () => {
    expect(withoutToken('/chat/demo?a=1&token=x&b=2')).toBe('/chat/demo?a=1&b=2');
  });
});
