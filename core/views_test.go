package core

import (
	"path/filepath"
	"testing"
	"time"
)

// The web session list orders by reading state, so presence has exactly two
// jobs: report who is being read right now, and record when a reader left.
// Both are driven by heartbeats plus a TTL, which is what these tests pin.

func newViewTestSession(t *testing.T, storePath string) (*SessionManager, *Session) {
	t.Helper()
	sm := NewSessionManager(storePath)
	s := sm.NewSession("user@example.com", "demo")
	if s == nil || s.ID == "" {
		t.Fatal("expected a created session with an id")
	}
	return sm, s
}

func TestViewingIDs_HeartbeatKeepsSessionViewing(t *testing.T) {
	sm, s := newViewTestSession(t, "")
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	sm.HeartbeatView(s.ID, now)

	if !sm.ViewingIDs(now.Add(time.Second))[s.ID] {
		t.Fatal("a fresh heartbeat must report the session as viewing")
	}
}

func TestViewingIDs_StaleHeartbeatEndsViewing(t *testing.T) {
	sm, s := newViewTestSession(t, "")
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	sm.HeartbeatView(s.ID, now)

	got := sm.ViewingIDs(now.Add(ViewPresenceTTL + time.Second))

	if got[s.ID] {
		t.Fatal("a heartbeat older than the TTL must not count as viewing")
	}
}

// The close time is derived from the last heartbeat, not from when the expiry
// was noticed: a client that crashes keeps the session "viewing" until the next
// list request, and stamping *that* moment would float an old conversation to
// the top of the "recently closed" group on the next visit.
func TestViewingIDs_RecordsCloseTimeFromLastHeartbeat(t *testing.T) {
	sm, s := newViewTestSession(t, "")
	last := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	sm.HeartbeatView(s.ID, last)

	// Evaluated long after the fact.
	sm.ViewingIDs(last.Add(6 * time.Hour))

	got := sm.FindByID(s.ID).Snapshot().ViewClosedAt
	want := last.Add(ViewPresenceTTL)
	if !got.Equal(want) {
		t.Fatalf("ViewClosedAt = %v, want %v (last heartbeat + TTL)", got, want)
	}
}

// A heartbeat does not update the session, only a close does — otherwise every
// reader would rewrite the session store every few seconds.
func TestHeartbeatView_DoesNotTouchSessionRecord(t *testing.T) {
	sm, s := newViewTestSession(t, "")
	before := sm.FindByID(s.ID).Snapshot()

	sm.HeartbeatView(s.ID, time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))

	after := sm.FindByID(s.ID).Snapshot()
	if !after.ViewClosedAt.IsZero() || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("heartbeat changed the session record: %+v", after)
	}
}

// A conversation reopened later keeps its old close time until it closes again;
// the live "viewing" flag is what wins in the list, so there is nothing to
// clear on open.
func TestViewingIDs_ReopenedSessionStaysViewing(t *testing.T) {
	sm, s := newViewTestSession(t, "")
	first := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	sm.HeartbeatView(s.ID, first)
	sm.ViewingIDs(first.Add(ViewPresenceTTL + time.Second))

	second := first.Add(time.Hour)
	sm.HeartbeatView(s.ID, second)

	if !sm.ViewingIDs(second.Add(time.Second))[s.ID] {
		t.Fatal("a re-opened session must report as viewing again")
	}
	if got := sm.FindByID(s.ID).Snapshot().ViewClosedAt; !got.Equal(first.Add(ViewPresenceTTL)) {
		t.Fatalf("ViewClosedAt changed on reopen: %v", got)
	}
}

// "Long-term" ordering means the close time survives a restart; the live
// presence map deliberately does not, because every client re-reports on its
// next heartbeat.
func TestViewingIDs_CloseTimeSurvivesRestart(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	sm, s := newViewTestSession(t, storePath)
	last := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	sm.HeartbeatView(s.ID, last)
	sm.ViewingIDs(last.Add(ViewPresenceTTL + time.Second))

	reloaded := NewSessionManager(storePath)
	got := reloaded.FindByID(s.ID)
	if got == nil {
		t.Fatal("session missing after reload")
	}
	want := last.Add(ViewPresenceTTL)
	if closed := got.Snapshot().ViewClosedAt; !closed.Equal(want) {
		t.Fatalf("ViewClosedAt after reload = %v, want %v", closed, want)
	}
	if closed := reloaded.ViewingIDs(last.Add(2 * time.Hour)); closed[s.ID] {
		t.Fatal("presence must not be restored after a restart")
	}
}

// An unknown id (a tab that outlived its session) must not crash expiry.
func TestViewingIDs_IgnoresUnknownSession(t *testing.T) {
	sm, _ := newViewTestSession(t, "")
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	sm.HeartbeatView("s404", now)

	if got := sm.ViewingIDs(now.Add(ViewPresenceTTL + time.Second)); len(got) != 0 {
		t.Fatalf("ViewingIDs = %v, want empty", got)
	}
}
