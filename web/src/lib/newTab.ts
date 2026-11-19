// Opening a tab from an async handler has one trap: a popup is only allowed
// while the click that asked for it is still being handled. `await` ends that
// window, so the tab has to be reserved first and pointed at its URL later.
//
// Both halves live here because the Web UI creates sessions from two places
// (the session-list dialog and the chat drawer) and they must behave the same.

/** Reserves a blank tab from inside the click. Returns null when blocked. */
export function reserveTab(): Window | null {
  return window.open('', '_blank');
}

/**
 * Points a reserved tab at `path`. Returns false when no tab was reserved
 * (popup blocked), so the caller can fall back to navigating the current tab
 * instead of losing the session it just created.
 */
export function openReservedTab(tab: Window | null, path: string): boolean {
  if (!tab) return false;
  // Absolute URL: a blank tab has no document to resolve a relative path
  // against.
  tab.location.replace(new URL(path, window.location.origin).href);
  return true;
}
