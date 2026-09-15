/**
 * A monotonically-increasing sequence guard for async work whose result must
 * only be applied if it is still the newest request.
 *
 * Why this exists: the chat page loads a conversation's detail from the URL
 * parameter. Clicking several conversations in quick succession starts
 * overlapping `getSession` calls, and without a guard the SLOWEST response
 * wins — publishing the wrong conversation's history into the view, or
 * clearing the loading flag while a newer fetch is still in flight.
 *
 * Usage:
 *   const seq = new SequenceGuard();
 *   const ticket = seq.begin();
 *   const data = await fetchSomething();
 *   if (!seq.isCurrent(ticket)) return;   // a newer request superseded us
 *   apply(data);
 *
 * Deliberately framework-free so it can be unit-tested (see
 * sequenceGuard.test.ts) and reused for any similar race in this codebase.
 */
export class SequenceGuard {
  private current = 0;

  /** Start a new request and return its ticket. */
  begin(): number {
    return ++this.current;
  }

  /** True when `ticket` is still the most recent request. */
  isCurrent(ticket: number): boolean {
    return ticket === this.current;
  }
}
