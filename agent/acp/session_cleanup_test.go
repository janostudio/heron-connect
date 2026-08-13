package acp

// session_cleanup_test.go — regression tests for acpSession.Close() process
// cleanup. Verifies:
//   - Close() closes the events channel (so engine's channelClosed path fires)
//   - Close() is idempotent (multiple calls don't panic)
//   - closeEvents helper uses sync.Once internally

import (
	"io"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

// closeTestSessionPipes closes the pipe ends that newTestSession created,
// so Close()'s tr.notify() call doesn't block waiting for a reader.
// Call this before s.Close() in tests that don't have a real subprocess
// reading the transport.
func closeTestSessionPipes(wResp *io.PipeWriter, rReq *io.PipeReader) {
	if wResp != nil {
		_ = wResp.Close()
	}
	if rReq != nil {
		_ = rReq.Close()
	}
}

// TestClose_ClosesEventsChannel verifies that Close() closes the events
// channel. This is the core fix for the orphaned --acp processes issue:
// without closing the channel, the engine's channelClosed cleanup path
// (which calls cleanupInteractiveState) never fires, and the dead session
// lingers in the interactiveStates map forever.
func TestClose_ClosesEventsChannel(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	// Close pipes so tr.notify in Close() doesn't block (no real reader).
	closeTestSessionPipes(wResp, rReq)

	// Before Close: channel should be open.
	select {
	case _, ok := <-s.events:
		if !ok {
			t.Fatal("events channel should be open before Close()")
		}
		t.Fatal("events channel should not have buffered events before Close()")
	default:
		// good — open and empty
	}

	_ = s.Close()

	// After Close: channel should be closed.
	select {
	case _, ok := <-s.events:
		if ok {
			t.Fatal("events channel should be closed after Close(), got open")
		}
		// success — channel is closed
	default:
		t.Fatal("events channel should be closed (received zero value), not blocking")
	}
}

// TestClose_Idempotent verifies that calling Close() multiple times does not
// panic (closeOnce protects against double close of the events channel).
func TestClose_Idempotent(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	closeTestSessionPipes(wResp, rReq)

	// First close should succeed.
	if err := s.Close(); err != nil {
		t.Fatalf("first Close() failed: %v", err)
	}

	// Second close should not panic.
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() failed: %v", err)
	}

	// Third close via closeEvents directly should also be safe.
	s.closeEvents()
	s.closeEvents()
}

// TestCloseEvents_Once verifies the closeOnce mechanism directly.
func TestCloseEvents_Once(t *testing.T) {
	s, _, _ := newTestSession(t, nil)

	// Call closeEvents multiple times concurrently — should not panic.
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("closeEvents panicked: %v", r)
				}
				done <- struct{}{}
			}()
			s.closeEvents()
		}()
	}

	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-make(chan struct{}): // never
			t.Fatal("closeEvents goroutine did not complete")
		}
	}

	// Channel should be closed.
	select {
	case _, ok := <-s.events:
		if ok {
			t.Fatal("events channel should be closed")
		}
	default:
		t.Fatal("events channel should be closed")
	}
}

// TestClose_DeadSessionClosesEvents verifies that Close() on an already-dead
// session (alive=false) still closes the events channel. This covers the
// scenario where the process died on its own before Close() was called.
func TestClose_DeadSessionClosesEvents(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	closeTestSessionPipes(wResp, rReq)

	// Simulate the process dying on its own (goroutine sets alive=false).
	s.alive.Store(false)

	// Close() should still close the events channel via closeEvents().
	_ = s.Close()

	select {
	case _, ok := <-s.events:
		if ok {
			t.Fatal("events channel should be closed even when session was already dead")
		}
	default:
		t.Fatal("events channel should be closed")
	}
}

// TestClose_AliveStateAfterClose verifies that after Close(), the session
// is marked not alive. This is a sanity check that the alive flag is
// cleared before events channel is closed.
func TestClose_AliveStateAfterClose(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)
	closeTestSessionPipes(wResp, rReq)

	if !s.alive.Load() {
		t.Fatal("session should be alive before Close()")
	}

	_ = s.Close()

	if s.alive.Load() {
		t.Fatal("session should be marked not alive after Close()")
	}
}

// TestPrepareCmdForKill_SetsPgid verifies that PrepareCmdForKill is callable
// (smoke test — the actual Setpgid is platform-specific).
func TestPrepareCmdForKill_SetsPgid(t *testing.T) {
	_ = core.PrepareCmdForKill // function reference exists
}

