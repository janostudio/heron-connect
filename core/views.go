package core

import (
	"sync"
	"time"
)

// Web reading presence.
//
// The session list is ordered by what people are reading right now, so the
// backend has to know which conversations have a web client open. Only the
// client can know that, so it sends a heartbeat naming the conversation it is
// showing; this file turns those heartbeats into the two states the ordering
// needs:
//
//   - viewing: a heartbeat arrived within ViewPresenceTTL
//   - closed:  heartbeats stopped; the moment of the last heartbeat plus the
//     TTL is recorded as Session.LastViewClosedAt (persisted, so the order
//     survives a restart)
//
// Deliberate choices:
//
//   - The heartbeat carries no client identity. Two tabs reading the same
//     conversation are indistinguishable, which is fine: either tab closing
//     leaves the other one refreshing the same key, and only the absence of
//     every heartbeat closes the session.
//   - A restart drops the in-memory map and every client re-reports on its
//     next heartbeat, so nothing here needs persisting. Only the close time
//     is durable.
//   - Expiry is evaluated lazily whenever someone asks (the list is polled
//     every few seconds) instead of by a background ticker: no goroutine to
//     leak, and the recorded close time stays exact because it is derived
//     from the last heartbeat, not from when someone happened to look.
const ViewPresenceTTL = 90 * time.Second

type viewPresence struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

// HeartbeatView records that a web client is reading the session right now.
// Empty ids and unknown sessions are ignored by the caller's contract; this
// method only maintains the presence map.
func (sm *SessionManager) HeartbeatView(id string, now time.Time) {
	if id == "" {
		return
	}
	sm.views.mu.Lock()
	defer sm.views.mu.Unlock()
	if sm.views.lastSeen == nil {
		sm.views.lastSeen = make(map[string]time.Time)
	}
	sm.views.lastSeen[id] = now
}

type viewClose struct {
	id string
	at time.Time
}

// ViewingIDs returns the sessions being read right now, expiring heartbeats
// that went stale on the way. Expiry is a write (it persists the close time),
// so this is the single entry point the list handler needs: call it once per
// response and the returned set is consistent with the persisted state.
func (sm *SessionManager) ViewingIDs(now time.Time) map[string]bool {
	sm.views.mu.Lock()
	var closed []viewClose
	live := make(map[string]bool, len(sm.views.lastSeen))
	for id, seen := range sm.views.lastSeen {
		if now.Sub(seen) > ViewPresenceTTL {
			// The reader went away somewhere between its last heartbeat and
			// now; the TTL boundary is the most accurate close time available.
			closed = append(closed, viewClose{id: id, at: seen.Add(ViewPresenceTTL)})
			delete(sm.views.lastSeen, id)
			continue
		}
		live[id] = true
	}
	sm.views.mu.Unlock()

	if len(closed) > 0 {
		sm.markViewsClosed(closed)
	}
	return live
}

// markViewsClosed persists the close time of sessions whose readers left.
func (sm *SessionManager) markViewsClosed(closes []viewClose) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	changed := false
	for _, c := range closes {
		s, ok := sm.sessions[c.id]
		if !ok {
			continue
		}
		s.SetViewClosedAt(c.at)
		changed = true
	}
	if changed {
		sm.saveLocked()
	}
}
