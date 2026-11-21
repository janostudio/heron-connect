// Browser tab title.
//
// The tab is a very narrow strip — roughly 12–14 CJK characters at the default
// tab width, less as tabs accumulate — so it carries exactly two things: which
// conversation this is, and one icon when something needs the user. Nothing
// else fits; the project name and the app name are already available from the
// favicon and the address bar.

/** Shown whenever a page does not own the title (matches web/index.html). */
export const DEFAULT_TITLE = 'Heron Connect Admin';

/**
 * One icon slot for every state that competes for the user's attention. They
 * never stack: an icon is either present or not, so the conversation name keeps
 * its position at the front of the title.
 */
export type TitleIcon = '' | 'waiting' | 'unread' | 'running';

const ICONS: Record<Exclude<TitleIcon, ''>, string> = {
  // Waiting on the user outranks everything: nothing moves until they answer.
  waiting: '⚠️',
  // Something arrived while the tab was in the background.
  unread: '●',
  // A turn is in flight; nothing for the user to do yet.
  running: '⏳',
};

/**
 * The most urgent state wins, so a single glyph is enough:
 * approval prompt > unread result > still running.
 */
export function pickTitleIcon(state: { waiting?: boolean; unread?: boolean; running?: boolean }): TitleIcon {
  if (state.waiting) return 'waiting';
  if (state.unread) return 'unread';
  if (state.running) return 'running';
  return '';
}

/** `⚠️ Fix the upload limit` / `Fix the upload limit`. */
export function composeTitle(name: string, icon: TitleIcon = ''): string {
  const trimmed = name.trim();
  if (!trimmed) return DEFAULT_TITLE;
  const glyph = icon ? ICONS[icon] : '';
  return glyph ? `${glyph} ${trimmed}` : trimmed;
}
