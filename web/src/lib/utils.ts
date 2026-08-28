import { clsx, type ClassValue } from 'clsx';

export function cn(...inputs: ClassValue[]) {
  return clsx(inputs);
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function formatTime(iso: string): string {
  if (!iso) return '-';
  return new Date(iso).toLocaleString();
}

export function truncate(s: string, max: number): string {
  return s.length > max ? s.slice(0, max) + '...' : s;
}

// ── localStorage helpers (cc_ prefix convention, see store/theme.ts etc.) ──

export function loadLS<T>(key: string): T | null {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return null;
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

export function saveLS<T>(key: string, value: T): void {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // ignore quota / private-mode failures
  }
}

// legacyCopy copies text via a hidden textarea + document.execCommand. It must
// run synchronously inside the user-gesture call stack — browsers tie
// execCommand('copy') to transient user activation, which an earlier await
// would consume. Used when the async Clipboard API is unavailable (plain-http
// LAN access is a non-secure context, navigator.clipboard is undefined there)
// or when it rejects (e.g. permission denied).
function legacyCopy(text: string): boolean {
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.readOnly = true; // keep mobile keyboards from popping up
    ta.style.position = 'fixed';
    ta.style.top = '0';
    ta.style.left = '0';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    ta.setSelectionRange(0, text.length);
    const ok = document.execCommand('copy');
    ta.remove();
    return ok;
  } catch {
    return false;
  }
}

// copyText copies text to the clipboard, resolving to true only when the copy
// actually happened. The common no-Clipboard-API case never awaits, so the
// legacy path keeps the user gesture and works on plain http.
export function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    return navigator.clipboard.writeText(text).then(
      () => true,
      () => legacyCopy(text),
    );
  }
  return Promise.resolve(legacyCopy(text));
}
