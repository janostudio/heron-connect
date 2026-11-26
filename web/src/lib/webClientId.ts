// Bridge client identity.
//
// The id must be unique per browser tab. It used to be cached in
// sessionStorage so a reload reused it, but browsers copy sessionStorage when
// a tab is duplicated: two tabs came up with the same id, and the bridge —
// which keeps exactly one connection per client_id — replaced one with the
// other, so the tabs kicked each other out on every reload.
//
// Minting a fresh id on every page load makes a duplicated tab an independent
// client. Reconnects inside one page keep the id (the caller stores it once),
// which is what the server's "same client_id replaces the previous connection"
// rule needs; a reload does not, because the old socket dies with the page.

export function newClientID(): string {
  try {
    const c = (globalThis as any).crypto;
    if (c && typeof c.randomUUID === 'function') return c.randomUUID();
  } catch {
    /* fall through to the timestamp form */
  }
  return `web-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
