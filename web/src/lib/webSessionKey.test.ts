import { describe, it, expect } from 'vitest';
import { newConvKey, webAdminSessionKey, WEB_CONN_ID } from './webSessionKey';

// The session_key shape is a CONTRACT with the Go backend:
//   core.MintWebSessionKey produces bridge:web-admin:<project>:conv-<id>
// and session routing derives the platform from the `bridge:` prefix. A format
// drift here silently breaks per-conversation isolation (all conversations
// collapse back onto one agent session — the original cross-talk bug).

describe('newConvKey', () => {
  it('matches the backend shape', () => {
    expect(newConvKey('auto-bugfix')).toMatch(/^bridge:web-admin:auto-bugfix:conv-[0-9a-f-]+$/);
  });

  it('returns an empty string for a missing project', () => {
    expect(newConvKey('')).toBe('');
  });

  it('mints a DISTINCT key on every call (the core isolation invariant)', () => {
    // Two conversations in the same tab must never share a key, or their agent
    // sessions get bound to each other.
    const keys = new Set(Array.from({ length: 50 }, () => newConvKey('p')));
    expect(keys.size).toBe(50);
  });

  it('keeps the project segment verbatim', () => {
    expect(newConvKey('my-proj_2')).toContain(':web-admin:my-proj_2:');
  });

  it('never emits the legacy wc- prefix inside the conv id', () => {
    // The fallback generator used to produce "wc-..." ids; the replace() keeps
    // them from leaking into the conv- id and producing "conv-wc-...".
    for (let i = 0; i < 20; i++) {
      expect(newConvKey('p')).not.toContain('conv-wc-');
    }
  });

  it('produces a key whose platform prefix is exactly "bridge"', () => {
    // platformFromSessionKey splits on the first ':'; anything else breaks routing.
    expect(newConvKey('p').split(':')[0]).toBe('bridge');
  });
});

describe('webAdminSessionKey', () => {
  it('is scoped to the connection id', () => {
    expect(webAdminSessionKey('proj')).toBe(`bridge:web-admin:proj:${WEB_CONN_ID}`);
  });

  it('returns an empty string for a missing project', () => {
    expect(webAdminSessionKey('')).toBe('');
  });

  it('is stable within a page load and distinct from a conversation key', () => {
    expect(webAdminSessionKey('p')).toBe(webAdminSessionKey('p'));
    expect(webAdminSessionKey('p')).not.toBe(newConvKey('p'));
  });

  it('exposes a non-empty per-page connection id', () => {
    expect(WEB_CONN_ID.length).toBeGreaterThan(0);
  });
});
