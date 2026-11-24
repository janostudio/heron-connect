import { useEffect, useRef, useCallback, useState, useMemo } from 'react';
import api from '@/api/client';
import { bridgeConfigKey, bridgeSocketUrl, type BridgeConfig } from './bridgeConfig';

// Re-exported so existing importers keep working.
export type { BridgeConfig };

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

// Media attachments sent alongside a chat message over the bridge `message`
// frame. `data` is the raw base64 payload (no data: URL prefix). Backend
// (bridge.handleMessage) already decodes these into core.Message.Images/.Files.
export interface BridgeMedia {
  images?: { mime_type: string; data: string; file_name?: string }[];
  files?: { mime_type: string; data: string; file_name: string }[];
}

export type BridgeStatus = 'connecting' | 'registering' | 'connected' | 'disconnected' | 'error' | 'replaced';

/**
 * Close code the bridge sends when a newer connection registered the same
 * client_id (mirror of the server-side constant in bridge/bridge.go).
 */
export const BRIDGE_CLOSE_REPLACED = 4001;

const RECONNECT_BASE_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 30000;

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

export interface UseBridgeSocketOptions {
  bridgeCfg: BridgeConfig | null;
  platformName?: string;
  sessionKey: string;
  projectName?: string;
  onMessage: (msg: BridgeIncoming) => void;
}

export function useBridgeSocket({ bridgeCfg, platformName = 'web', sessionKey, projectName, onMessage }: UseBridgeSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null);
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;
  const pingRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const [status, setStatus] = useState<BridgeStatus>('disconnected');

  // Per-tab client id: each browser tab/window is an independent bridge client
  // so multiple Web dashboards can connect simultaneously without kicking each
  // other off. sessionStorage scopes the id to the tab; a fallback random uuid
  // is used if storage is unavailable.
  const clientID = useMemo(() => {
    try {
      const key = 'bridge_client_id';
      const existing = sessionStorage.getItem(key);
      if (existing) return existing;
      const id = (crypto as any).randomUUID
        ? (crypto as any).randomUUID()
        : `web-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      sessionStorage.setItem(key, id);
      return id;
    } catch {
      return `web-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    }
  }, []);

  const send = useCallback((data: Record<string, any>) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(data));
    }
  }, []);

  const sendMessage = useCallback((content: string, media?: BridgeMedia, sessionID?: string) => {
    send({
      type: 'message',
      msg_id: `web-${Date.now()}`,
      session_key: sessionKey,
      session_id: sessionID || '',
      user_id: 'web-admin',
      user_name: 'Web Admin',
      content,
      reply_ctx: sessionKey,
      project: projectName || '',
      ...(media || {}),
    });
  }, [send, sessionKey, projectName]);

  const sendCardAction = useCallback((action: string) => {
    send({
      type: 'card_action',
      session_key: sessionKey,
      action,
      reply_ctx: sessionKey,
      project: projectName || '',
    });
  }, [send, sessionKey, projectName]);

  const sendPreviewAck = useCallback((refId: string, handle: string) => {
    send({ type: 'preview_ack', ref_id: refId, preview_handle: handle });
  }, [send]);

  // The connect effect must not re-run just because the caller handed us a
  // fresh (but equal) config object. fetchBridgeConfig() builds a new object
  // on every call, and the chat page re-fetches it, which used to tear down
  // and reopen the WebSocket mid-turn — dropping the in-flight stream.
  // Depend on the actual routing values instead, read from a ref.
  const bridgeCfgRef = useRef(bridgeCfg);
  bridgeCfgRef.current = bridgeCfg;
  const cfgKey = bridgeConfigKey(bridgeCfg);

  useEffect(() => {
    if (!cfgKey) return;

    const wsUrl = bridgeSocketUrl(bridgeCfgRef.current!, window.location);

    let ws: WebSocket | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
    let alive = true;
    // Consecutive reconnect attempts since the last successful registration.
    // Only register_ack resets it: a socket that opens and closes immediately
    // must still back off, otherwise a half-broken path retries at full speed.
    let attempt = 0;

    const clearPing = () => {
      if (pingRef.current) {
        clearInterval(pingRef.current);
        pingRef.current = null;
      }
    };

    function scheduleReconnect() {
      if (!alive) return;
      const delay = reconnectDelayMs(attempt);
      attempt += 1;
      reconnectTimer = setTimeout(connect, delay);
    }

    function connect() {
      if (!alive) return;
      // Retire a leftover socket first. Without this, its close handler can
      // clear wsRef and schedule a second reconnect on top of this one.
      if (ws && ws.readyState !== WebSocket.CLOSED) {
        ws.onclose = null;
        ws.close();
      }
      setStatus('connecting');
      const sock = new WebSocket(wsUrl);
      ws = sock;
      wsRef.current = sock;

      sock.onopen = () => {
        setStatus('registering');
        sock.send(JSON.stringify({
          type: 'register',
          platform: platformName,
          client_id: clientID,
          capabilities: ['text', 'card', 'buttons', 'typing', 'update_message', 'preview', 'reconstruct_reply', 'image', 'file'],
          // progress_max_entries: 0 = show all progress entries (the default
          // 10-entry cap exists for IM message-size limits; Web streams over
          // WebSocket and has no such limit).
          // supports_progress_card_payload: true opts the Web client into
          // receiving structured __heron_connect_progress_card_v1__ payloads
          // instead of pre-rendered markdown, so the chat UI can render
          // individual tool/thinking blocks as collapsible cards.
          metadata: { version: '1.0.0', description: 'Web Admin Dashboard', progress_max_entries: 0, supports_progress_card_payload: true },
        }));
      };

      sock.onmessage = (evt) => {
        try {
          const msg = JSON.parse(evt.data) as BridgeIncoming;
          if (msg.type === 'register_ack') {
            if (msg.ok) {
              attempt = 0;
              setStatus('connected');
              clearPing();
              pingRef.current = setInterval(() => {
                send({ type: 'ping', ts: Date.now() });
              }, 25000);
            } else {
              setStatus('error');
            }
          }
          onMessageRef.current(msg);
        } catch { /* ignore parse errors */ }
      };

      sock.onclose = (evt) => {
        // Only drop the ref if it still points at this socket: a newer
        // connection may already own it.
        if (wsRef.current === sock) wsRef.current = null;
        clearPing();
        if (!alive) return;
        if (!shouldReconnect(evt.code)) {
          // Another connection holds this client_id. Reconnecting would kick
          // it and start the replacement loop this code exists to break.
          setStatus('replaced');
          return;
        }
        setStatus('disconnected');
        scheduleReconnect();
      };

      sock.onerror = () => {
        setStatus('error');
      };
    }

    connect();

    return () => {
      alive = false;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      clearPing();
      const sock = wsRef.current;
      wsRef.current = null;
      ws = null;
      if (sock) {
        sock.onclose = null;
        sock.close();
      }
      setStatus('disconnected');
    };
  }, [cfgKey, platformName, send]);

  return { status, send, sendMessage, sendCardAction, sendPreviewAck };
}

// Fetch bridge config from the management API status endpoint.
export async function fetchBridgeConfig(): Promise<BridgeConfig | null> {
  try {
    const status = await api.get<any>('/status');
    if (status.bridge?.enabled) {
      return {
        port: status.bridge.port,
        path: status.bridge.path,
        token: status.bridge.token,
      };
    }
  } catch { /* bridge not available */ }
  return null;
}
