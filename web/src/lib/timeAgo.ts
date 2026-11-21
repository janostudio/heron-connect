/**
 * Relative time for session lists: compact while the gap is small, an absolute
 * date once "how long ago" stops being useful.
 *
 * Why not one shared `42d`: the lists hold conversations that can be a year old,
 * and a bare day count cannot be told apart from last month. The cutoff is a
 * week — inside it the gap reads naturally ("3d"), beyond it the date is what
 * the user actually needs.
 *
 * `now` is injectable so the boundaries can be tested without freezing the
 * clock globally.
 */
export type Translate = (key: string, options?: { defaultValue?: string }) => string;

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

function pad(value: number): string {
  return value < 10 ? `0${value}` : String(value);
}

function isSameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

function isYesterday(then: Date, now: Date): boolean {
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  return isSameDay(then, yesterday);
}

export function timeAgo(iso: string, t: Translate, now: Date = new Date()): string {
  if (!iso) return '';
  const then = new Date(iso);
  if (Number.isNaN(then.getTime())) return '';

  const diffMs = now.getTime() - then.getTime();
  // A timestamp slightly in the future (clock skew between server and browser)
  // must not produce "-2m"; treat it as "just now".
  if (diffMs < MINUTE) return t('sessions.justNow', { defaultValue: 'just now' });

  const mins = Math.floor(diffMs / MINUTE);
  if (mins < 60) return `${mins}m`;

  const hours = Math.floor(diffMs / HOUR);
  if (hours < 24) return `${hours}h`;

  if (isYesterday(then, now)) return t('chat.time.yesterday', { defaultValue: 'yesterday' });

  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d`;

  const monthDay = `${pad(then.getMonth() + 1)}-${pad(then.getDate())}`;
  return then.getFullYear() === now.getFullYear() ? monthDay : `${then.getFullYear()}-${monthDay}`;
}
