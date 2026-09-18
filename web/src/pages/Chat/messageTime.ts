// Timestamp helpers for chat messages.
//
// Two sources of timestamps feed the transcript:
//
//   • live frames — stamped locally by nowStamp() as the message lands (see
//     chatSessionsCore.applyFrame and ChatView's send paths);
//   • persisted history — stamped server-side by core.Session.AddHistory and
//     delivered as RFC3339 in `history[].timestamp`.
//
// Both reach the UI as strings, so this module owns the single parse/format
// path. Everything here is pure (only Intl + Date), which keeps it unit-
// testable in the node environment and free of component imports.
//
// The assistant timestamp is deliberately the FIRST VISIBLE RESPONSE time
// (first streamed text / first progress card), not the time the turn settled —
// so callers must never overwrite an existing stamp. See applyFrame.

/** The current time as an RFC3339 string — same shape the backend emits. */
export function nowStamp(): string {
  return new Date().toISOString();
}

/**
 * Normalise a timestamp string to a Date. Accepts RFC3339 (what both the
 * backend and nowStamp produce); returns null for anything unusable so callers
 * can skip rendering rather than print "Invalid Date".
 */
export function parseStamp(ts?: string | null): Date | null {
  if (!ts) return null;
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** Local calendar day identity — compared as a triple, never as a time delta. */
function dayKey(d: Date): string {
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

/**
 * The display text for a message timestamp, e.g. "14:03", "Yesterday 14:03",
 * "Sep 12 14:03", or "Dec 1, 2025 14:03" for a previous year.
 *
 * Relative-ness is decided by LOCAL CALENDAR DAY (not by subtracting 86_400s),
 * so a message sent at 23:59 reads as "Yesterday" right after midnight, and a
 * DST shift in between cannot push a message into the wrong bucket.
 *
 * `locale` drives the date wording (Intl), `yesterdayLabel` the one word Intl
 * cannot supply — it is passed in by the caller from i18n so this module stays
 * free of translation plumbing.
 *
 * Returns null when there is nothing displayable (missing/invalid stamp).
 */
export function formatMessageTime(
  ts: string | undefined | null,
  locale: string,
  yesterdayLabel: string,
  now: Date = new Date(),
): string | null {
  const d = parseStamp(ts);
  if (!d) return null;

  const time = new Intl.DateTimeFormat(locale, {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(d);

  const key = dayKey(d);
  if (key === dayKey(now)) return time;

  // Yesterday: compare against the previous calendar day of `now`.
  const prev = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  if (key === dayKey(prev)) return `${yesterdayLabel} ${time}`;

  const sameYear = d.getFullYear() === now.getFullYear();
  const date = new Intl.DateTimeFormat(locale, {
    ...(sameYear ? {} : { year: 'numeric' }),
    month: 'short',
    day: 'numeric',
  }).format(d);
  return `${date} ${time}`;
}

/**
 * The full local timestamp for the `title` attribute (hover tooltip), so the
 * compact display above the bubble never has to be ambiguous.
 */
export function fullMessageTime(ts: string | undefined | null, locale: string): string | null {
  const d = parseStamp(ts);
  if (!d) return null;
  return new Intl.DateTimeFormat(locale, { dateStyle: 'full', timeStyle: 'short' }).format(d);
}
