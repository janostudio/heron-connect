import { DEFAULT_SESSION_SORT_MODE, type SessionSortMode } from '@/api/sessions';

// Which order the session lists use, remembered per browser.
//
// A preference rather than a per-page toggle state: the chat drawer and the
// session management page show the same list, and flipping the order in one of
// them and finding it reverted in the other would be a bug, not a choice.
//
// Storage is localStorage (not sessionStorage): the choice is about how this
// person likes to read their sessions, not about one tab.

const SORT_MODE_KEY = 'cc.sessions.sortMode';

function storage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

function parse(value: string | null): SessionSortMode {
  return value === 'activity' || value === 'reading' ? value : DEFAULT_SESSION_SORT_MODE;
}

/** The order to use on first render. Unusable storage reads as the default. */
export function readSortMode(): SessionSortMode {
  const store = storage();
  if (!store) return DEFAULT_SESSION_SORT_MODE;
  try {
    return parse(store.getItem(SORT_MODE_KEY));
  } catch {
    return DEFAULT_SESSION_SORT_MODE;
  }
}

export function writeSortMode(mode: SessionSortMode): void {
  const store = storage();
  if (!store) return;
  try {
    store.setItem(SORT_MODE_KEY, mode);
  } catch {
    /* private mode / quota: the choice stays in memory for this page */
  }
}
