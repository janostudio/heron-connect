// Connection lifecycle for the web bridge socket.
//
// The server owns keepalive: it pings the socket with WebSocket control
// frames, which the browser answers from its network stack even while the page
// is throttled in the background. Page JavaScript therefore does not run a
// keepalive timer — a `setInterval` ping stops firing when a tab is hidden,
// which is how connections used to die after 90 seconds in the background.
//
// What page JavaScript still owns:
//   - reconnect policy: hidden tabs do not reconnect (the server keeps the
//     socket alive, and timers cannot be trusted while hidden); becoming
//     visible reconnects immediately instead of waiting out a throttled timer.
//   - probing: on returning to the foreground, a socket may report OPEN while
//     the connection is actually gone. A `ping` that gets no `pong` within
//     PROBE_TIMEOUT_MS closes the socket so the normal reconnect path runs.

export type BridgeIncoming =
  | { type: 'register_ack'; ok: boolean; error?: string }
  | { type: 'reply'; session_key: string; session_id?: string; reply_ctx: string; message_id?: string; team?: string; content: string; format?: string; notice?: boolean }
  | { type: 'reply_stream'; session_key: string; session_id?: string; reply_ctx: string; delta: string; full_text: string; preview_handle?: string; done: boolean }
  | { type: 'card'; session_key: string; session_id?: string; reply_ctx: string; card: any }
  | { type: 'buttons'; session_key: string; session_id?: string; reply_ctx: string; content: string; buttons: { text: string; data: string }[][] }
  | { type: 'typing_start'; session_key: string }
  | { type: 'typing_stop'; session_key: string }
  | { type: 'preview_start'; ref_id: string; session_key: string; session_id?: string; reply_ctx: string; content: string }
  | { type: 'update_message'; session_key: string; preview_handle: string; content: string }
  | { type: 'delete_message'; session_key: string; preview_handle: string }
  | { type: 'error'; code: string; message: string }
  | { type: 'pong'; ts: number }
  | { type: string; [key: string]: any };

export type BridgeStatus = 'connecting' | 'registering' | 'connected' | 'disconnected' | 'error' | 'replaced';

/**
 * Close code the bridge sends when a newer connection registered the same
 * client_id (mirror of the server-side constant in bridge/bridge.go).
 */
export const BRIDGE_CLOSE_REPLACED = 4001;

export const RECONNECT_BASE_DELAY_MS = 1000;
export const RECONNECT_MAX_DELAY_MS = 30000;

/**
 * How long a probe waits for its pong before the socket is considered dead.
 * The server answers immediately on the same event loop that reads the ping,
 * so this only has to cover a slow round trip, not a busy server.
 */
export const PROBE_TIMEOUT_MS = 8000;

/**
 * Foreground probe cadence. Even with a healthy server this catches a socket
 * that died without a close frame (network change, sleeping laptop) while the
 * page stayed open.
 */
export const PROBE_INTERVAL_MS = 60000;

/** Protocol version advertised in the register frame. */
export const BRIDGE_PROTOCOL_VERSION = '2.0.0';

/**
 * Whether a closed socket should be reopened.
 *
 * A replaced connection must stay closed. Reconnecting would kick the newer
 * connection that superseded it, whose own close handler would then reconnect
 * and kick us back — a single overlap would turn into an endless replacement
 * loop instead of settling on one connection.
 */
export function shouldReconnect(closeCode: number): boolean {
  return closeCode !== BRIDGE_CLOSE_REPLACED;
}

/**
 * Delay before the n-th consecutive reconnect attempt (0-based): exponential
 * backoff with jitter, capped so a long outage still retries periodically.
 * `random` is injectable so the schedule can be asserted in tests.
 */
export function reconnectDelayMs(attempt: number, random: () => number = Math.random): number {
  const exp = Math.max(0, attempt);
  const base = Math.min(RECONNECT_MAX_DELAY_MS, RECONNECT_BASE_DELAY_MS * 2 ** exp);
  return base + Math.floor(random() * 250);
}

/** The slice of WebSocket this module needs; kept structural for tests. */
export interface BridgeSocket {
  readyState: number;
  send(data: string): void;
  close(): void;
  onopen: ((ev: any) => void) | null;
  onmessage: ((ev: { data: string }) => void) | null;
  onclose: ((ev: { code: number }) => void) | null;
  onerror: ((ev: any) => void) | null;
}

const SOCKET_OPEN = 1;

export interface BridgeConnectionOptions {
  url: string;
  platformName: string;
  clientID: string;
  onStatus: (status: BridgeStatus) => void;
  onMessage: (msg: BridgeIncoming) => void;
  /** Injected by tests. */
  createSocket?: (url: string) => BridgeSocket;
  /** Injected by tests; defaults to the document's visibility. */
  isVisible?: () => boolean;
  /** Injected by tests; defaults to Math.random. */
  random?: () => number;
}

function defaultSocket(url: string): BridgeSocket {
  return new WebSocket(url) as unknown as BridgeSocket;
}

function defaultVisible(): boolean {
  return typeof document === 'undefined' || document.visibilityState !== 'hidden';
}

export class BridgeConnection {
  private socket: BridgeSocket | null = null;
  private reconnectTimer?: ReturnType<typeof setTimeout>;
  private probeTimer?: ReturnType<typeof setTimeout>;
  private probeLoop?: ReturnType<typeof setInterval>;
  private attempt = 0;
  private registered = false;
  private running = false;
  private lastPongAt = 0;
  private probeSentAt = 0;
  /**
   * Set when the server closed this connection because a newer connection
   * registered the same client_id (code 4001). Terminal for the lifetime of
   * this instance: any reconnect — including the one triggered by becoming
   * visible — would kick the connection that replaced us and start the
   * replacement loop the close code exists to prevent.
   */
  private replacedTerminal = false;

  constructor(private readonly opts: BridgeConnectionOptions) {}

  start(): void {
    if (this.running) return;
    this.running = true;
    this.probeLoop = setInterval(() => this.probe(), PROBE_INTERVAL_MS);
    this.connect();
  }

  stop(): void {
    this.running = false;
    this.clearReconnect();
    this.clearProbe();
    if (this.probeLoop) {
      clearInterval(this.probeLoop);
      this.probeLoop = undefined;
    }
    this.retireSocket();
    this.registered = false;
  }

  send(data: Record<string, unknown>): void {
    const sock = this.socket;
    if (sock && sock.readyState === SOCKET_OPEN) {
      sock.send(JSON.stringify(data));
    }
  }

  /**
   * Called when the page became visible. A live socket is probed; anything
   * else reconnects now rather than waiting for a timer the browser froze
   * while the tab was hidden.
   */
  handleVisibilityChange(): void {
    if (!this.running || this.replacedTerminal || !this.visible()) return;
    if (this.registered && this.socket?.readyState === SOCKET_OPEN) {
      this.probe();
      return;
    }
    this.clearReconnect();
    this.connect();
  }

  private visible(): boolean {
    return this.opts.isVisible ? this.opts.isVisible() : defaultVisible();
  }

  private connect(): void {
    if (!this.running || this.replacedTerminal || !this.visible()) return;
    this.retireSocket();
    this.opts.onStatus('connecting');

    const sock = this.opts.createSocket
      ? this.opts.createSocket(this.opts.url)
      : defaultSocket(this.opts.url);
    this.socket = sock;

    sock.onopen = () => {
      this.opts.onStatus('registering');
      sock.send(JSON.stringify({
        type: 'register',
        platform: this.opts.platformName,
        client_id: this.opts.clientID,
        capabilities: ['text', 'card', 'buttons', 'typing', 'update_message', 'preview', 'reconstruct_reply', 'image', 'file'],
        // progress_max_entries: 0 = show all progress entries (the default
        // 10-entry cap exists for IM message-size limits; Web streams over
        // WebSocket and has no such limit).
        // supports_progress_card_payload: true opts the Web client into
        // receiving structured __heron_connect_progress_card_v1__ payloads
        // instead of pre-rendered markdown, so the chat UI can render
        // individual tool/thinking blocks as collapsible cards.
        metadata: {
          version: BRIDGE_PROTOCOL_VERSION,
          description: 'Web Admin Dashboard',
          progress_max_entries: 0,
          supports_progress_card_payload: true,
        },
      }));
    };

    sock.onmessage = (evt) => {
      let msg: BridgeIncoming;
      try {
        msg = JSON.parse(evt.data) as BridgeIncoming;
      } catch {
        return;
      }
      if (msg.type === 'register_ack') {
        if (msg.ok) {
          this.attempt = 0;
          this.registered = true;
          this.opts.onStatus('connected');
        } else {
          this.opts.onStatus('error');
        }
      } else if (msg.type === 'pong') {
        this.lastPongAt = Date.now();
        this.clearProbe();
      }
      this.opts.onMessage(msg);
    };

    sock.onclose = (evt) => {
      if (this.socket === sock) this.socket = null;
      this.clearProbe();
      this.registered = false;
      if (!this.running) return;
      if (!shouldReconnect(evt.code)) {
        this.replacedTerminal = true;
        this.opts.onStatus('replaced');
        return;
      }
      this.opts.onStatus('disconnected');
      this.scheduleReconnect();
    };

    sock.onerror = () => {
      this.opts.onStatus('error');
    };
  }

  private scheduleReconnect(): void {
    if (!this.running || this.replacedTerminal || !this.visible()) return;
    const delay = reconnectDelayMs(this.attempt, this.opts.random);
    this.attempt += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = undefined;
      this.connect();
    }, delay);
  }

  private probe(): void {
    if (!this.running || !this.registered || !this.visible()) return;
    const sock = this.socket;
    if (!sock || sock.readyState !== SOCKET_OPEN) return;

    this.probeSentAt = Date.now();
    sock.send(JSON.stringify({ type: 'ping', ts: this.probeSentAt }));
    this.clearProbe();
    this.probeTimer = setTimeout(() => {
      this.probeTimer = undefined;
      if (!this.running || this.lastPongAt >= this.probeSentAt) return;
      // Half-open: the socket claims OPEN but nothing comes back. Closing it
      // runs the normal close handler, which schedules a reconnect.
      this.socket?.close();
    }, PROBE_TIMEOUT_MS);
  }

  private clearProbe(): void {
    if (this.probeTimer) {
      clearTimeout(this.probeTimer);
      this.probeTimer = undefined;
    }
  }

  private clearReconnect(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = undefined;
    }
  }

  /** Detach handlers before closing so a superseded socket cannot schedule a
   * reconnect for the connection that replaced it. */
  private retireSocket(): void {
    const sock = this.socket;
    this.socket = null;
    if (!sock) return;
    sock.onopen = null;
    sock.onmessage = null;
    sock.onclose = null;
    sock.onerror = null;
    sock.close();
  }
}
