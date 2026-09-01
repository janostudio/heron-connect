package core

import "testing"

func TestAdmissionDecisions(t *testing.T) {
	e := &Engine{name: "test"}

	d := e.admitDispatch()
	if d.Action != AdmissionDispatch {
		t.Fatalf("dispatch action = %q", d.Action)
	}

	d = e.admitReject("rate limited", "slow down")
	if d.Action != AdmissionReject || d.Reason != "rate limited" || d.Reply != "slow down" {
		t.Fatalf("reject = %+v", d)
	}

	d = e.admitHandled("slash command")
	if d.Action != AdmissionHandled || d.Reason != "slash command" {
		t.Fatalf("handled = %+v", d)
	}

	d = e.admitDrop("empty")
	if d.Action != AdmissionDrop || d.Reason != "empty" {
		t.Fatalf("drop = %+v", d)
	}
}

func TestAdmissionDecisionValuesDistinct(t *testing.T) {
	// Guard against accidental constant collisions.
	seen := map[MessageAdmission]bool{}
	for _, a := range []MessageAdmission{
		AdmissionDispatch, AdmissionObserveOnly, AdmissionHandled, AdmissionDrop, AdmissionReject,
	} {
		if seen[a] {
			t.Fatalf("duplicate admission value %q", a)
		}
		seen[a] = true
	}
}

func TestLogAdmissionPassthrough(t *testing.T) {
	e := &Engine{name: "test"}
	p := &stubPlatformEngine{n: "test"}
	msg := &Message{Platform: "test", SessionKey: "t:u:c", UserID: "u"}
	d := e.admitReject("banned", "no")
	out := e.logAdmission(p, msg, "banned_word", d)
	if out != d {
		t.Fatal("logAdmission must return the same decision pointer")
	}
	if out.Action != AdmissionReject {
		t.Fatalf("action = %q", out.Action)
	}
}
