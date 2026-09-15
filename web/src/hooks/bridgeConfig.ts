export interface BridgeConfig {
  port: number;
  path: string;
  token: string;
}

/**
 * Identity key for a bridge config, used as the connect effect's dependency.
 *
 * Why not depend on the config object itself: fetchBridgeConfig() builds a new
 * object on every call, and the chat page re-fetches it, so an object identity
 * dependency tore down and reopened the WebSocket mid-turn — dropping the
 * in-flight stream. Keying on the actual routing values means a genuine change
 * (e.g. token rotation) still reconnects, while a fresh-but-equal object does
 * not.
 *
 * Returns '' for a missing config, which the effect treats as "do not connect".
 */
export function bridgeConfigKey(cfg: BridgeConfig | null | undefined): string {
  if (!cfg) return '';
  return `${cfg.port}|${cfg.path}|${cfg.token}`;
}

/**
 * Build the bridge WebSocket URL. Uses the current page host:port so the
 * request goes through the Vite/nginx proxy instead of directly hitting the
 * bridge port (which may not be reachable from the browser).
 *
 * `location` is injected so this is testable outside a DOM.
 */
export function bridgeSocketUrl(
  cfg: BridgeConfig,
  location: { protocol: string; host: string },
): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${location.host}${cfg.path}?token=${encodeURIComponent(cfg.token)}`;
}
