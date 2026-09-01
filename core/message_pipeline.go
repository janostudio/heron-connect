package core

// message_pipeline.go — explicit admission decisions for inbound messages.
//
// This is a lightweight, backward-compatible step toward openclaw's explicit
// turn pipeline (runChannelTurn + ChannelTurnAdmission): it gives each early
// reject path in handleMessage a typed outcome and a unified observability
// point, WITHOUT restructuring the existing control flow (session locking,
// workspace resolution, and goto sessionLocked remain untouched).
//
// The goal is behavioral equivalence first: every stage returns a decision the
// caller acts on exactly as the previous inline code did, but now each decision
// is observable and individually testable.

import "log/slog"

// MessageAdmission is the outcome of an inbound-message admission check.
type MessageAdmission string

const (
	// AdmissionDispatch lets the message proceed to the agent.
	AdmissionDispatch MessageAdmission = "dispatch"
	// AdmissionObserveOnly records the message but does not start an agent turn.
	AdmissionObserveOnly MessageAdmission = "observe_only"
	// AdmissionHandled means the message was fully handled locally (slash
	// command, permission answer, etc.) and needs no agent turn.
	AdmissionHandled MessageAdmission = "handled"
	// AdmissionDrop silently discards the message (empty content, no payload).
	AdmissionDrop MessageAdmission = "drop"
	// AdmissionReject refuses the message and replies a reason to the user
	// (rate limited, banned word, insufficient permission, etc.).
	AdmissionReject MessageAdmission = "reject"
)

// AdmissionDecision is the typed result of one admission stage.
type AdmissionDecision struct {
	Action MessageAdmission
	Reason string // human-readable reason, recorded in the admission log
	Reply  string // reply content for AdmissionReject
}

// dispatch returns a "let it through" decision.
func (e *Engine) admitDispatch() *AdmissionDecision {
	return &AdmissionDecision{Action: AdmissionDispatch}
}

// reject returns a "refuse with a reply" decision.
func (e *Engine) admitReject(reason, reply string) *AdmissionDecision {
	return &AdmissionDecision{Action: AdmissionReject, Reason: reason, Reply: reply}
}

// handled returns a "handled locally, no agent turn" decision.
func (e *Engine) admitHandled(reason string) *AdmissionDecision {
	return &AdmissionDecision{Action: AdmissionHandled, Reason: reason}
}

// drop returns a "silently discard" decision.
func (e *Engine) admitDrop(reason string) *AdmissionDecision {
	return &AdmissionDecision{Action: AdmissionDrop, Reason: reason}
}

// logAdmission records a unified admission trace for observability. It is a
// no-op passthrough that logs the decision and returns it, so callers can
// inline it without restructuring.
func (e *Engine) logAdmission(p Platform, msg *Message, stage string, d *AdmissionDecision) *AdmissionDecision {
	slog.Info("message admission",
		"stage", stage,
		"action", string(d.Action),
		"reason", d.Reason,
		"platform", msg.Platform,
		"session", msg.SessionKey,
		"user", msg.UserID,
	)
	return d
}
