import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BridgeConnection,
  PROBE_INTERVAL_MS,
  PROBE_TIMEOUT_MS,
  reconnectDelayMs,
  type BridgeSocket,
  type BridgeStatus,
} from './bridgeConnection';

class MockSocket implements BridgeSocket {
  static instances: MockSocket[] = [];

  readyState = 0;
  sent: string[] = [];
  onopen: ((ev: any) => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  onerror: ((ev: any) => void) | null = null;

  constructor(readonly url: string) {
    MockSocket.instances.push(this);
  }

  send(data: string): void {
    this.sent.push(data);
  }

  close(): void {
    this.readyState = 3;
    // A real socket delivers onclose asynchronously; delivering it here keeps
    // the client-side state machine observable under fake timers.
    const handler = this.onclose;
    if (handler) {
      this.onclose = null;
      handler({ code: 1000 });
    }
  }

  open(): void {
    this.readyState = 1;
    this.onopen?.({});
  }

  reply(msg: unknown): void {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }

  serverClose(code: number): void {
    this.readyState = 3;
    this.onclose?.({ code });
  }

  lastSent(): any {
    return JSON.parse(this.sent[this.sent.length - 1]);
  }
}

interface Harness {
  conn: BridgeConnection;
  statuses: BridgeStatus[];
  setVisible: (v: boolean) => void;
}

function newHarness(): Harness {
  let visible = true;
  const statuses: BridgeStatus[] = [];
  const conn = new BridgeConnection({
    url: 'ws://example.test/bridge/ws?token=t',
    platformName: 'web',
    clientID: 'client-a',
    onStatus: (s) => statuses.push(s),
    onMessage: () => {},
    createSocket: (url) => new MockSocket(url),
    isVisible: () => visible,
    random: () => 0,
  });
  return { conn, statuses, setVisible: (v) => { visible = v; } };
}

function connectFirst(h: Harness): MockSocket {
  h.conn.start();
  const sock = MockSocket.instances[0];
  sock.open();
  sock.reply({ type: 'register_ack', ok: true });
  return sock;
}

beforeEach(() => {
  vi.useFakeTimers();
  MockSocket.instances = [];
});

afterEach(() => {
  vi.useRealTimers();
});

describe('register frame', () => {
  it('reuses the given client id and advertises protocol 2.0.0', () => {
    const h = newHarness();
    const sock = connectFirst(h);

    const frame = JSON.parse(sock.sent[0]);
    expect(frame.type).toBe('register');
    expect(frame.client_id).toBe('client-a');
    expect(frame.metadata.version).toBe('2.0.0');
    expect(h.statuses).toContain('connected');
  });
});

describe('visibility driven reconnect', () => {
  it('does not connect while hidden, and connects as soon as it becomes visible', () => {
    const h = newHarness();
    h.setVisible(false);
    h.conn.start();

    expect(MockSocket.instances).toHaveLength(0);

    h.setVisible(true);
    h.conn.handleVisibilityChange();

    expect(MockSocket.instances).toHaveLength(1);
  });

  it('does not reconnect a dropped socket while hidden', () => {
    const h = newHarness();
    const sock = connectFirst(h);

    h.setVisible(false);
    sock.serverClose(1006);
    vi.advanceTimersByTime(5 * 60 * 1000);

    expect(MockSocket.instances).toHaveLength(1);
    expect(h.statuses[h.statuses.length - 1]).toBe('disconnected');
  });

  it('reconnects immediately on becoming visible instead of waiting out the backoff', () => {
    const h = newHarness();
    const sock = connectFirst(h);

    h.setVisible(false);
    sock.serverClose(1006);
    h.setVisible(true);
    h.conn.handleVisibilityChange();

    expect(MockSocket.instances).toHaveLength(2);
  });
});

describe('foreground probe', () => {
  it('probes on becoming visible and keeps a socket that answers', () => {
    const h = newHarness();
    const sock = connectFirst(h);

    h.conn.handleVisibilityChange();
    expect(sock.lastSent().type).toBe('ping');

    sock.reply({ type: 'pong', ts: Date.now() });
    vi.advanceTimersByTime(PROBE_TIMEOUT_MS);

    expect(sock.readyState).toBe(1);
    expect(MockSocket.instances).toHaveLength(1);
  });

  it('closes a half-open socket that never answers and reconnects', () => {
    const h = newHarness();
    const sock = connectFirst(h);

    h.conn.handleVisibilityChange();
    vi.advanceTimersByTime(PROBE_TIMEOUT_MS);

    expect(sock.readyState).toBe(3);

    // 1s base backoff, no jitter in this harness.
    vi.advanceTimersByTime(1000);
    expect(MockSocket.instances).toHaveLength(2);
  });

  it('runs the probe periodically while connected', () => {
    const h = newHarness();
    const sock = connectFirst(h);

    const before = sock.sent.filter((s) => s.includes('"ping"')).length;
    vi.advanceTimersByTime(PROBE_INTERVAL_MS);

    expect(sock.sent.filter((s) => s.includes('"ping"')).length).toBe(before + 1);
  });
});

describe('terminal close code', () => {
  it('stays closed and does not reconnect when replaced by another connection', () => {
    const h = newHarness();
    const sock = connectFirst(h);

    sock.serverClose(4001);
    vi.advanceTimersByTime(5 * 60 * 1000);

    expect(MockSocket.instances).toHaveLength(1);
    expect(h.statuses[h.statuses.length - 1]).toBe('replaced');
  });

  it('stays closed when the page becomes visible again', () => {
    const h = newHarness();
    const sock = connectFirst(h);

    sock.serverClose(4001);
    h.setVisible(false);
    h.setVisible(true);
    h.conn.handleVisibilityChange();
    vi.advanceTimersByTime(5 * 60 * 1000);

    expect(MockSocket.instances).toHaveLength(1);
  });
});

describe('stop', () => {
  it('cancels pending reconnects and probes', () => {
    const h = newHarness();
    const sock = connectFirst(h);
    sock.serverClose(1006);

    h.conn.stop();
    vi.advanceTimersByTime(5 * 60 * 1000);

    expect(MockSocket.instances).toHaveLength(1);
  });
});

describe('reconnectDelayMs', () => {
  it('backs off exponentially and caps the delay', () => {
    expect(reconnectDelayMs(0, () => 0)).toBe(1000);
    expect(reconnectDelayMs(1, () => 0)).toBe(2000);
    expect(reconnectDelayMs(4, () => 0)).toBe(16000);
    expect(reconnectDelayMs(10, () => 0)).toBe(30000);
    expect(reconnectDelayMs(0, () => 0.999)).toBeGreaterThan(1000);
  });
});
