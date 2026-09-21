/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { absoluteShareURL } from './share';

// The backend returns a server-relative share URL ("/api/v1/share/<token>").
// Handing that to a recipient verbatim would resolve against THEIR origin, so
// it must be absolutised before it is copied or shown. Getting this wrong
// silently produces a link that works for the sharer and 404s for everyone
// else — a failure that is easy to miss in manual testing.
describe('absoluteShareURL', () => {
  const original = window.location;

  afterEach(() => {
    vi.unstubAllGlobals();
    // @ts-expect-error restore the jsdom location object
    window.location = original;
  });

  it('prefixes the current origin for a root-relative path', () => {
    vi.stubGlobal('location', { ...original, origin: 'https://heron.example.com' });
    expect(absoluteShareURL('/api/v1/share/abc123'))
      .toBe('https://heron.example.com/api/v1/share/abc123');
  });

  it('adds a missing leading slash', () => {
    vi.stubGlobal('location', { ...original, origin: 'https://heron.example.com' });
    expect(absoluteShareURL('api/v1/share/abc123'))
      .toBe('https://heron.example.com/api/v1/share/abc123');
  });

  it('leaves an already-absolute URL untouched', () => {
    expect(absoluteShareURL('https://cdn.example.com/f.md')).toBe('https://cdn.example.com/f.md');
    expect(absoluteShareURL('http://localhost:9820/api/v1/share/x')).toBe('http://localhost:9820/api/v1/share/x');
  });

  it('returns empty for empty input', () => {
    expect(absoluteShareURL('')).toBe('');
  });
});
