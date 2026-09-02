package core

import (
	"context"
	"testing"
	"time"
)

// TestExecuteCronNoDelivery_RunsAgentWithoutPlatform verifies a no_delivery
// cron job runs the agent turn to completion without targeting any platform:
// it must NOT error with "platform not found" and must NOT require a valid
// SessionKey.
func TestExecuteCronNoDelivery_RunsAgentWithoutPlatform(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, nil, "", LangEnglish)

	// A stub agent whose StartSession returns a controllable session that
	// emits a result event and closes, so the turn completes.
	agent := &noDeliveryTestAgent{}
	e.agent = agent

	job := &CronJob{
		ID:         "no-delivery-1",
		Project:    "test",
		SessionKey: "", // deliberately empty — must be ignored
		CronExpr:   "0 1 * * *",
		Prompt:     "produce a report",
		NoDelivery: true,
		Enabled:    true,
	}

	if err := e.ExecuteCronJob(job); err != nil {
		t.Fatalf("ExecuteCronJob(no_delivery) = %v, want nil", err)
	}

	if agent.startCalls != 1 {
		t.Fatalf("agent.StartSession calls = %d, want 1 (agent must actually run)", agent.startCalls)
	}
	if agent.sendCalls != 1 {
		t.Fatalf("agentSession.Send calls = %d, want 1", agent.sendCalls)
	}
}

// noDeliveryTestAgent is an Agent whose session emits a result and closes.
type noDeliveryTestAgent struct {
	startCalls int
	sendCalls  int
}

func (a *noDeliveryTestAgent) Name() string { return "no-delivery-test" }
func (a *noDeliveryTestAgent) StartSession(_ context.Context, _ string) (AgentSession, error) {
	a.startCalls++
	return &noDeliveryTestSession{agent: a}, nil
}
func (a *noDeliveryTestAgent) ListSessions(_ context.Context) ([]AgentSessionInfo, error) {
	return nil, nil
}
func (a *noDeliveryTestAgent) Stop() error { return nil }

type noDeliveryTestSession struct {
	agent *noDeliveryTestAgent
	done  bool
}

func (s *noDeliveryTestSession) Send(_ string, _ []ImageAttachment, _ []FileAttachment) error {
	s.agent.sendCalls++
	return nil
}
func (s *noDeliveryTestSession) RespondPermission(_ string, _ PermissionResult) error { return nil }
func (s *noDeliveryTestSession) Events() <-chan Event {
	ch := make(chan Event, 1)
	ch <- Event{Type: EventResult, Content: "report done", Done: true}
	close(ch)
	return ch
}
func (s *noDeliveryTestSession) CurrentSessionID() string { return "sess-1" }
func (s *noDeliveryTestSession) Alive() bool              { return !s.done }
func (s *noDeliveryTestSession) Close() error             { s.done = true; return nil }
func (s *noDeliveryTestSession) CancelTurn()              {}

// TestCronNoopPlatform_IsInert verifies the no-op platform never errors and
// reports its identity correctly.
func TestCronNoopPlatform_IsInert(t *testing.T) {
	p := cronNoopPlatform{}
	if p.Name() != "cron-noop" {
		t.Fatalf("Name() = %q, want cron-noop", p.Name())
	}
	if err := p.Start(nil); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if err := p.Reply(nil, nil, "x"); err != nil {
		t.Fatalf("Reply() = %v", err)
	}
	if err := p.Send(nil, nil, "x"); err != nil {
		t.Fatalf("Send() = %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop() = %v", err)
	}
}

// TestCronJob_NoDeliveryFieldPersists verifies the field survives store round-trip.
func TestCronJob_NoDeliveryFieldPersists(t *testing.T) {
	dir := t.TempDir()
	store, err := NewCronStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	job := &CronJob{
		ID:         "j1",
		Project:    "p",
		CronExpr:   "0 1 * * *",
		Prompt:     "x",
		NoDelivery: true,
		Enabled:    true,
		CreatedAt:  time.Now(),
	}
	if err := store.Add(job); err != nil {
		t.Fatal(err)
	}
	got := store.Get("j1")
	if got == nil || !got.NoDelivery {
		t.Fatalf("NoDelivery not persisted: %+v", got)
	}
}
