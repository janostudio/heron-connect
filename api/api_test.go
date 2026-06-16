package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestHandleSend_RequiresMessageOrAttachment(t *testing.T) {
	s := &APIServer{
		engines: make(map[string]*core.Engine),
		mux:     http.NewServeMux(),
	}

	// Empty body — no message, no images, no files → 400
	body, _ := json.Marshal(SendRequest{Project: "test", SessionKey: "s:1:1"})
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSend(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty message+attachments, got %d", rec.Code)
	}
}

func TestHandleSend_AllowsAttachmentOnly(t *testing.T) {
	s := &APIServer{
		engines: make(map[string]*core.Engine),
		mux:     http.NewServeMux(),
	}

	// Images present, no message → should pass validation (not 400),
	// but project not found → 404.
	reqBody := SendRequest{
		Project:    "nonexistent",
		SessionKey: "session-1",
		Images: []core.ImageAttachment{{
			MimeType: "image/png",
			Data:     []byte("img"),
			FileName: "chart.png",
		}},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSend(rec, req)

	// Validation passes (images present), so we must NOT get 400.
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("attachment-only should pass validation, but got 400: %s", rec.Body.String())
	}
}

func TestHandleSend_MethodNotAllowed(t *testing.T) {
	s := &APIServer{engines: make(map[string]*core.Engine), mux: http.NewServeMux()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/send", nil)
	s.handleSend(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleRelaySend_UnavailableWithoutRelay(t *testing.T) {
	s := &APIServer{engines: make(map[string]*core.Engine), mux: http.NewServeMux()}
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(core.RelayRequest{To: "bot2", Message: "hi", SessionKey: "tg:1:1"})
	req := httptest.NewRequest(http.MethodPost, "/relay/send", bytes.NewReader(body))
	s.handleRelaySend(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when relay not configured, got %d", rec.Code)
	}
}

func TestHandleRelayBinding_RequiresChatID(t *testing.T) {
	s := &APIServer{engines: make(map[string]*core.Engine), mux: http.NewServeMux()}
	// No relay — should get 503
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/relay/binding", nil)
	s.handleRelayBinding(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when relay not configured, got %d", rec.Code)
	}
}

func TestHandleCronAdd_UnavailableWithoutCron(t *testing.T) {
	s := &APIServer{engines: make(map[string]*core.Engine), mux: http.NewServeMux()}
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(core.CronAddRequest{CronExpr: "* * * * *", Prompt: "hi"})
	req := httptest.NewRequest(http.MethodPost, "/cron/add", bytes.NewReader(body))
	s.handleCronAdd(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when cron not configured, got %d", rec.Code)
	}
}
