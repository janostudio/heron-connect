import { describe, it, expect } from 'vitest';
import {
  BRIDGE_CLOSE_REPLACED,
  reconnectDelayMs,
  shouldReconnect,
} from './useBridgeSocket';

// ── shouldReconnect ──────────────────────────────────────────
//
// The regression this guards: the bridge closes a superseded connection (same
// client_id, newer connection) with BRIDGE_CLOSE_REPLACED. Reconnecting on
// that code kicks the newer connection, whose close handler reconnects and
// kicks back — a single overlap becomes an endless replacement loop instead
// of settling on one connection.

describe('shouldReconnect', () => {
  it('stops when the bridge replaced the connection', () => {
    expect(shouldReconnect(BRIDGE_CLOSE_REPLACED)).toBe(false);
  });

  it('keeps retrying on ordinary closes', () => {
    expect(shouldReconnect(1000)).toBe(true); // normal closure
    expect(shouldReconnect(1005)).toBe(true); // no status
    expect(shouldReconnect(1006)).toBe(true); // abnormal closure
  });
});

// ── reconnectDelayMs ─────────────────────────────────────────
//
// A fixed 3s retry made a broken path retry at full speed forever. The delay
// must grow per consecutive attempt (reset on a successful registration) and
// stay capped so a long outage still retries periodically.

describe('reconnectDelayMs', () => {
  const noJitter = () => 0;

  it('backs off exponentially from one second', () => {
    expect(reconnectDelayMs(0, noJitter)).toBe(1000);
    expect(reconnectDelayMs(1, noJitter)).toBe(2000);
    expect(reconnectDelayMs(2, noJitter)).toBe(4000);
    expect(reconnectDelayMs(3, noJitter)).toBe(8000);
  });

  it('caps the delay at 30 seconds', () => {
    expect(reconnectDelayMs(5, noJitter)).toBe(30000);
    expect(reconnectDelayMs(50, noJitter)).toBe(30000);
  });

  it('treats a negative attempt as the first one', () => {
    expect(reconnectDelayMs(-1, noJitter)).toBe(1000);
  });

  it('adds at most 250ms of jitter', () => {
    expect(reconnectDelayMs(0, () => 0)).toBe(1000);
    expect(reconnectDelayMs(0, () => 0.999)).toBe(1249);
  });
});
