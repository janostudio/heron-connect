import { useEffect, useRef, useCallback, useState, useMemo } from 'react';
import api from '@/api/client';
import { bridgeConfigKey, bridgeSocketUrl, type BridgeConfig } from './bridgeConfig';
import { BridgeConnection, type BridgeIncoming, type BridgeStatus } from './bridgeConnection';

// Re-exported so existing importers keep working.
export type { BridgeConfig, BridgeIncoming, BridgeStatus };
export { BRIDGE_CLOSE_REPLACED, shouldReconnect, reconnectDelayMs } from './bridgeConnection';

// Media attachments sent alongside a chat message over the bridge `message`
// frame. `data` is the raw base64 payload (no data: URL prefix). Backend
// (bridge.handleMessage) already decodes these into core.Message.Images/.Files.
export interface BridgeMedia {
  images?: { mime_type: string; data: string; file_name?: string }[];
  files?: { mime_type: string; data: string; file_name: string }[];
}

export interface UseBridgeSocketOptions {
  bridgeCfg: BridgeConfig | null;
  platformName?: string;
  sessionKey: string;
  projectName?: string;
  onMessage: (msg: BridgeIncoming) => void;
}

export function useBridgeSocket({ bridgeCfg, platformName = 'web', sessionKey, projectName, onMessage }: UseBridgeSocketOptions) {
  const connRef = useRef<BridgeConnection | null>(null);
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;
  const [status, setStatus] = useState<BridgeStatus>('disconnected');

  // Per-tab client id: each browser tab/window is an independent bridge client
  // so multiple Web dashboards can connect simultaneously without kicking each
  // other off. sessionStorage scopes the id to the tab; a fallback random uuid
  // is used if storage is unavailable.
  //
  // It must stay stable across reconnects: the server retires the previous
  // connection by client_id, so a regenerated id would leave the old adapter
  // registered and reintroduce cross-client interference.
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
    connRef.current?.send(data);
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

    const conn = new BridgeConnection({
      url: bridgeSocketUrl(bridgeCfgRef.current!, window.location),
      platformName,
      clientID,
      onStatus: setStatus,
      onMessage: (msg) => onMessageRef.current(msg),
    });
    connRef.current = conn;
    conn.start();

    // Visibility is owned here, not inside the connection: the same handler
    // both reconnects a dropped socket and probes a socket that looks alive.
    const onVisibilityChange = () => conn.handleVisibilityChange();
    const listen = typeof document !== 'undefined';
    if (listen) document.addEventListener('visibilitychange', onVisibilityChange);

    return () => {
      if (listen) document.removeEventListener('visibilitychange', onVisibilityChange);
      conn.stop();
      if (connRef.current === conn) connRef.current = null;
      setStatus('disconnected');
    };
  }, [cfgKey, platformName, clientID]);

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
