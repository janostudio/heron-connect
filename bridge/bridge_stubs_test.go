package bridge

import (
	"context"
	"sync"

	"github.com/chenhg5/cc-connect/core"
)

// ---------------------------------------------------------------------------
// Shared test stubs — reused across bridge_test.go, bridge_capabilities_test.go,
// and bridge_capabilities_snapshot_test.go.
// ---------------------------------------------------------------------------

type stubAgent struct{}

func (a *stubAgent) Name() string { return "stub" }
func (a *stubAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	return &stubAgentSession{}, nil
}
func (a *stubAgent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (a *stubAgent) Stop() error { return nil }

type stubAgentSession struct{}

func (s *stubAgentSession) Send(_ string, _ []core.ImageAttachment, _ []core.FileAttachment) error {
	return nil
}
func (s *stubAgentSession) RespondPermission(_ string, _ core.PermissionResult) error {
	return nil
}
func (s *stubAgentSession) Events() <-chan core.Event { return make(chan core.Event) }
func (s *stubAgentSession) CurrentSessionID() string  { return "stub-session" }
func (s *stubAgentSession) Alive() bool               { return true }
func (s *stubAgentSession) Close() error              { return nil }
func (s *stubAgentSession) CancelTurn()               {}

type stubPlatformEngine struct {
	n    string
	sent []string
	mu   sync.Mutex
}

func (p *stubPlatformEngine) Name() string                    { return p.n }
func (p *stubPlatformEngine) Start(core.MessageHandler) error { return nil }
func (p *stubPlatformEngine) Reply(_ context.Context, _ any, content string) error {
	p.mu.Lock()
	p.sent = append(p.sent, content)
	p.mu.Unlock()
	return nil
}
func (p *stubPlatformEngine) Send(_ context.Context, _ any, content string) error {
	p.mu.Lock()
	p.sent = append(p.sent, content)
	p.mu.Unlock()
	return nil
}
func (p *stubPlatformEngine) Stop() error { return nil }

func (p *stubPlatformEngine) getSent() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]string, len(p.sent))
	copy(cp, p.sent)
	return cp
}

func (p *stubPlatformEngine) clearSent() {
	p.mu.Lock()
	p.sent = nil
	p.mu.Unlock()
}
