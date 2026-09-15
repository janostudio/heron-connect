import { describe, it, expect } from 'vitest';
import { bridgeConfigKey, bridgeSocketUrl, type BridgeConfig } from './bridgeConfig';

const cfg = (over: Partial<BridgeConfig> = {}): BridgeConfig => ({
  port: 9810,
  path: '/bridge',
  token: 'secret-token',
  ...over,
});

// ── bridgeConfigKey ──────────────────────────────────────────
//
// This is the connect effect's dependency. The bug it fixes: depending on the
// config OBJECT tore down a live WebSocket whenever the chat page re-fetched
// its config (a new but equal object), dropping the in-flight stream mid-turn.

describe('bridgeConfigKey', () => {
  it('is empty for a missing config (effect must not connect)', () => {
    expect(bridgeConfigKey(null)).toBe('');
    expect(bridgeConfigKey(undefined)).toBe('');
  });

  it('is equal for two distinct but equal config objects', () => {
    // The regression: these are different objects and must NOT trigger a reconnect.
    const a = cfg();
    const b = cfg();
    expect(a).not.toBe(b);
    expect(bridgeConfigKey(a)).toBe(bridgeConfigKey(b));
  });

  it('changes when the port changes', () => {
    expect(bridgeConfigKey(cfg({ port: 9811 }))).not.toBe(bridgeConfigKey(cfg()));
  });

  it('changes when the path changes', () => {
    expect(bridgeConfigKey(cfg({ path: '/other' }))).not.toBe(bridgeConfigKey(cfg()));
  });

  it('changes when the token rotates (a real reconnect is still allowed)', () => {
    expect(bridgeConfigKey(cfg({ token: 'new' }))).not.toBe(bridgeConfigKey(cfg()));
  });

  it('distinguishes field boundaries (no accidental collisions)', () => {
    // "1|23" vs "12|3" style ambiguity must not make two configs look equal.
    expect(bridgeConfigKey(cfg({ port: 1, path: '23' })))
      .not.toBe(bridgeConfigKey(cfg({ port: 12, path: '3' })));
  });
});

// ── bridgeSocketUrl ──────────────────────────────────────────

describe('bridgeSocketUrl', () => {
  it('uses ws:// on http and the current page host', () => {
    expect(bridgeSocketUrl(cfg(), { protocol: 'http:', host: 'localhost:9821' }))
      .toBe('ws://localhost:9821/bridge?token=secret-token');
  });

  it('uses wss:// on https', () => {
    expect(bridgeSocketUrl(cfg(), { protocol: 'https:', host: 'example.com' }))
      .toBe('wss://example.com/bridge?token=secret-token');
  });

  it('URL-encodes the token', () => {
    const url = bridgeSocketUrl(cfg({ token: 'a b&c=d/e' }), { protocol: 'http:', host: 'h' });
    expect(url).toBe('ws://h/bridge?token=a%20b%26c%3Dd%2Fe');
    expect(url).not.toContain('a b');
  });

  it('routes through the page origin, not the bridge port directly', () => {
    // The browser may not be able to reach the bridge port; it must go via the
    // Vite/nginx proxy on the page origin.
    const url = bridgeSocketUrl(cfg({ port: 9810 }), { protocol: 'https:', host: 'dash.internal' });
    expect(url).toContain('dash.internal');
    expect(url).not.toContain(':9810');
  });
});
