package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

// sendStubPlatform implements core.Platform plus ReplyContextReconstructor so
// handleSend tests can exercise explicit-platform routing.
type sendStubPlatform struct {
	name string
	sent []string
}

func (p *sendStubPlatform) Name() string                    { return p.name }
func (p *sendStubPlatform) Start(core.MessageHandler) error { return nil }
func (p *sendStubPlatform) Stop() error                     { return nil }
func (p *sendStubPlatform) Reply(_ context.Context, _ any, content string) error {
	p.sent = append(p.sent, content)
	return nil
}
func (p *sendStubPlatform) Send(_ context.Context, _ any, content string) error {
	p.sent = append(p.sent, content)
	return nil
}
func (p *sendStubPlatform) ReconstructReplyCtx(sessionKey string) (any, error) {
	return "rctx", nil
}

func newSendTestEngine(t *testing.T, platforms ...core.Platform) *core.Engine {
	t.Helper()
	return core.NewEngine("test", &sendStubAgent{}, platforms, "", core.LangEnglish)
}

type sendStubAgent struct{}

func (a *sendStubAgent) Name() string { return "stub" }

func (a *sendStubAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	return nil, errors.New("not implemented")
}
func (a *sendStubAgent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (a *sendStubAgent) Stop() error { return nil }

func TestHandleSend_PlatformResponseFields(t *testing.T) {
	wecom := &sendStubPlatform{name: "wecom"}
	s := &APIServer{
		engines: map[string]*core.Engine{"test": newSendTestEngine(t, wecom)},
		mux:     http.NewServeMux(),
	}

	body, _ := json.Marshal(SendRequest{Project: "test", SessionKey: "wecom:C1:U1", Message: "hi", Platform: "wecom"})
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var ack map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ack["status"] != "ok" || ack["platform"] != "wecom" || ack["session_key"] != "wecom:C1:U1" {
		t.Fatalf("response = %#v, want status/platform/session_key", ack)
	}
	if len(wecom.sent) != 1 || wecom.sent[0] != "hi" {
		t.Fatalf("sent = %#v", wecom.sent)
	}
}

func TestHandleSend_PlatformNotFoundIs400(t *testing.T) {
	wecom := &sendStubPlatform{name: "wecom"}
	s := &APIServer{
		engines: map[string]*core.Engine{"test": newSendTestEngine(t, wecom)},
		mux:     http.NewServeMux(),
	}

	body, _ := json.Marshal(SendRequest{Project: "test", SessionKey: "wecom:C1:U1", Message: "hi", Platform: "nope"})
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSend_PlatformMismatchIs422(t *testing.T) {
	slack := &sendStubPlatform{name: "slack"}
	wecom := &sendStubPlatform{name: "wecom"}
	s := &APIServer{
		engines: map[string]*core.Engine{"test": newSendTestEngine(t, slack, wecom)},
		mux:     http.NewServeMux(),
	}

	body, _ := json.Marshal(SendRequest{Project: "test", SessionKey: "wecom:C1:U1", Message: "hi", Platform: "slack"})
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSend(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", rec.Code, rec.Body.String())
	}
}
