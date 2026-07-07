package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

// ─── Phase 1: local session tracking & ListSessions fallback ────────

func TestAgent_recordSessionStart_thenListLocal(t *testing.T) {
	a := &Agent{localSessions: make(map[string]*localSessionInfo)}
	a.recordSessionStart("sess-1", "/tmp/proj")
	a.recordSessionStart("sess-2", "/tmp/other")

	got := a.listLocalSessions("/tmp/proj")
	if len(got) != 1 || got[0].ID != "sess-1" {
		t.Fatalf("listLocalSessions(/tmp/proj) = %+v, want [sess-1]", got)
	}

	all := a.listLocalSessions("")
	if len(all) != 2 {
		t.Fatalf("listLocalSessions() = %d, want 2", len(all))
	}
}

func TestAgent_recordSessionStart_overwritesCwd(t *testing.T) {
	a := &Agent{localSessions: make(map[string]*localSessionInfo)}
	a.recordSessionStart("s1", "/old")
	a.recordSessionStart("s1", "/new")
	got := a.listLocalSessions("/new")
	if len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("expected s1 under /new, got %+v", got)
	}
}

func TestAgent_recordSessionTitle_setsTitleOnce(t *testing.T) {
	a := &Agent{localSessions: make(map[string]*localSessionInfo)}
	a.recordSessionStart("s1", "/tmp")
	a.recordSessionTitle("s1", "First message")
	// Second call should NOT overwrite an existing title.
	a.recordSessionTitle("s1", "Second message")
	got := a.listLocalSessions("")
	if len(got) != 1 || got[0].Summary != "First message" {
		t.Fatalf("title = %q, want %q", got[0].Summary, "First message")
	}
}

func TestAgent_recordSessionTitle_unknownSession(t *testing.T) {
	a := &Agent{localSessions: make(map[string]*localSessionInfo)}
	// Should be a no-op, not panic.
	a.recordSessionTitle("unknown", "title")
	if len(a.localSessions) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(a.localSessions))
	}
}

func TestAgent_localFallback_filteredByCwd(t *testing.T) {
	a := &Agent{
		workDir:       "/tmp/proj",
		localSessions: make(map[string]*localSessionInfo),
	}
	a.recordSessionStart("s1", "/tmp/proj")
	a.recordSessionStart("s2", "/tmp/other")
	a.recordSessionTitle("s1", "Implement feature X")

	got := a.localFallback()
	if len(got) != 1 || got[0].ID != "s1" || got[0].Summary != "Implement feature X" {
		t.Fatalf("localFallback = %+v, want [s1]", got)
	}
}

// ─── Phase 2: ModelSwitcher interface ────────────────────────────────

func TestAgent_SetModel_updatesPendingModel(t *testing.T) {
	a := &Agent{}
	a.SetModel("glm-5.2-ioa")
	if got := a.model; got != "glm-5.2-ioa" {
		t.Fatalf("model = %q, want glm-5.2-ioa", got)
	}
	// GetModel prefers pending over cached.
	if got := a.GetModel(); got != "glm-5.2-ioa" {
		t.Fatalf("GetModel() = %q, want glm-5.2-ioa", got)
	}
}

func TestAgent_GetModel_fallsBackToServerCurrent(t *testing.T) {
	a := &Agent{}
	a.reportModels("default-model", nil)
	if got := a.GetModel(); got != "default-model" {
		t.Fatalf("GetModel() = %q, want default-model", got)
	}
}

func TestAgent_GetModel_pendingOverridesServer(t *testing.T) {
	a := &Agent{}
	a.reportModels("server-model", nil)
	a.SetModel("pending-model")
	if got := a.GetModel(); got != "pending-model" {
		t.Fatalf("GetModel() = %q, want pending-model", got)
	}
}

func TestAgent_AvailableModels_emptyBeforeHandshake(t *testing.T) {
	a := &Agent{}
	got := a.AvailableModels(context.Background())
	if len(got) != 0 {
		t.Fatalf("expected empty before handshake, got %v", got)
	}
}

func TestAgent_reportModels_cachesList(t *testing.T) {
	a := &Agent{}
	models := []core.ModelOption{
		{Name: "glm-5.2-ioa", Desc: "GLM 5.2"},
		{Name: "glm-5.2-air", Desc: "GLM 5.2 Air"},
	}
	a.reportModels("glm-5.2-ioa", models)
	got := a.AvailableModels(context.Background())
	if len(got) != 2 || got[0].Name != "glm-5.2-ioa" {
		t.Fatalf("AvailableModels = %+v", got)
	}
}

// ─── Phase 2: parseModels ────────────────────────────────────────────

func TestParseModels_fromConfigOptions(t *testing.T) {
	opts := []acpConfigOption{
		{
			ID:           "model",
			Category:     "model",
			Type:         "select",
			CurrentValue: "glm-5.2-ioa",
			Options: []acpConfigOptValue{
				{Value: "glm-5.2-ioa", Name: "GLM 5.2 IOA"},
				{Value: "glm-5.2-air", Name: "GLM 5.2 Air"},
			},
		},
	}
	current, available := parseModels(opts, nil)
	if current != "glm-5.2-ioa" {
		t.Fatalf("current = %q, want glm-5.2-ioa", current)
	}
	if len(available) != 2 || available[0].Name != "glm-5.2-ioa" {
		t.Fatalf("available = %+v", available)
	}
	if available[0].Desc != "GLM 5.2 IOA" {
		t.Fatalf("Desc = %q, want GLM 5.2 IOA", available[0].Desc)
	}
}

func TestParseModels_fromLegacyModelsBlock(t *testing.T) {
	legacy := &acpModelsBlock{
		CurrentModelID: "gpt-4o",
		AvailableModels: []acpModelEntry{
			{ModelID: "gpt-4o", Name: "GPT-4o"},
			{ModelID: "claude-3", Name: "Claude 3"},
		},
	}
	current, available := parseModels(nil, legacy)
	if current != "gpt-4o" {
		t.Fatalf("current = %q, want gpt-4o", current)
	}
	if len(available) != 2 || available[0].Name != "gpt-4o" {
		t.Fatalf("available = %+v", available)
	}
}

func TestParseModels_configOptionsWinsOverLegacy(t *testing.T) {
	opts := []acpConfigOption{{
		Category:     "model",
		CurrentValue: "new-model",
		Options:      []acpConfigOptValue{{Value: "new-model"}},
	}}
	legacy := &acpModelsBlock{
		CurrentModelID: "old-model",
		AvailableModels: []acpModelEntry{{ModelID: "old-model"}},
	}
	current, available := parseModels(opts, legacy)
	if current != "new-model" {
		t.Fatalf("current = %q, want new-model", current)
	}
	if len(available) != 1 || available[0].Name != "new-model" {
		t.Fatalf("available = %+v, want new-model only", available)
	}
}

func TestParseModels_emptyReturnsEmpty(t *testing.T) {
	current, available := parseModels(nil, nil)
	if current != "" || len(available) != 0 {
		t.Fatalf("current = %q, available len = %d", current, len(available))
	}
}

func TestParseModels_ignoresNonModelCategory(t *testing.T) {
	opts := []acpConfigOption{
		{Category: "mode", CurrentValue: "plan"},
		{Category: "thought_level", CurrentValue: "high"},
	}
	current, available := parseModels(opts, nil)
	if current != "" || len(available) != 0 {
		t.Fatalf("expected empty for non-model categories")
	}
}

// ─── Phase 2: SetModel RPC ───────────────────────────────────────────

func TestACPSession_SetModel_sendsSetModel(t *testing.T) {
	cb := &fakeCallbacks{}
	s, wResp, rReq := newTestSession(t, cb)

	go func() {
		sc := bufio.NewScanner(rReq)
		for sc.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					ModelID string `json:"modelId"`
				} `json:"params"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				continue
			}
			if req.Method == "session/set_model" {
				_, _ = fmt.Fprintf(wResp, `{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", req.ID)
				return
			}
		}
	}()

	if err := s.SetModel("glm-5.2-ioa"); err != nil {
		t.Fatalf("SetModel failed: %v", err)
	}
}

func TestACPSession_SetModel_fallsBackToSetConfigOption(t *testing.T) {
	cb := &fakeCallbacks{}
	s, wResp, rReq := newTestSession(t, cb)

	go func() {
		sc := bufio.NewScanner(rReq)
		for sc.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				continue
			}
			if req.Method == "session/set_model" {
				// Server returns method-not-found.
				_, _ = fmt.Fprintf(wResp, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}`+"\n", req.ID)
				continue
			}
			if req.Method == "session/set_config_option" {
				_, _ = fmt.Fprintf(wResp, `{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", req.ID)
				return
			}
		}
	}()

	if err := s.SetModel("glm-5.2-air"); err != nil {
		t.Fatalf("SetModel fallback failed: %v", err)
	}
}

func TestACPSession_SetModel_deadSession(t *testing.T) {
	s, _, _ := newTestSession(t, nil)
	s.alive.Store(false)
	if err := s.SetModel("any"); err == nil {
		t.Fatal("expected error on dead session")
	}
}

// ─── Phase 3: notification handling ──────────────────────────────────

func TestACPSession_config_option_update(t *testing.T) {
	cb := &fakeCallbacks{}
	s, _, _ := newTestSession(t, cb)

	s.onNotification("session/update", json.RawMessage(`{
		"sessionId":"test-session-id",
		"update":{
			"sessionUpdate":"config_option_update",
			"configOptions":[{
				"id":"model","category":"model","type":"select","currentValue":"glm-5.2-air",
				"options":[{"value":"glm-5.2-air","name":"GLM 5.2 Air"}]
			}]
		}
	}`))

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if len(cb.modelCalls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(cb.modelCalls))
	}
	if cb.modelCalls[0].current != "glm-5.2-air" {
		t.Fatalf("current = %q, want glm-5.2-air", cb.modelCalls[0].current)
	}
}

func TestACPSession_session_info_update(t *testing.T) {
	cb := &fakeCallbacks{}
	s, _, _ := newTestSession(t, cb)
	// Pre-register the session so recordSessionTitle can find it.
	cb.recordSessionStart("test-session-id", "")

	s.onNotification("session/update", json.RawMessage(`{
		"sessionId":"test-session-id",
		"update":{"sessionUpdate":"session_info_update","title":"New Title"}
	}`))

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if len(cb.sessions) != 1 || cb.sessions[0].title != "New Title" {
		t.Fatalf("sessions = %+v", cb.sessions)
	}
}

func TestACPSession_usage_update(t *testing.T) {
	s, _, _ := newTestSession(t, nil)

	s.onNotification("session/update", json.RawMessage(`{
		"sessionId":"test-session-id",
		"update":{"sessionUpdate":"usage_update","used":53000,"size":200000}
	}`))

	usage := s.GetContextUsage()
	if usage == nil {
		t.Fatal("GetContextUsage() = nil, want snapshot")
	}
	if usage.UsedTokens != 53000 {
		t.Fatalf("UsedTokens = %d, want 53000", usage.UsedTokens)
	}
	if usage.ContextWindow != 200000 {
		t.Fatalf("ContextWindow = %d, want 200000", usage.ContextWindow)
	}
}

func TestACPSession_usage_update_beforeAnyUpdate(t *testing.T) {
	s, _, _ := newTestSession(t, nil)
	if got := s.GetContextUsage(); got != nil {
		t.Fatalf("GetContextUsage() before update = %+v, want nil", got)
	}
}

// ─── Phase 1: auto-extract session title ─────────────────────────────

func TestACPSession_autoExtractTitleFromFirstMessage(t *testing.T) {
	cb := &fakeCallbacks{}
	s, _, _ := newTestSession(t, cb)
	cb.recordSessionStart("test-session-id", "/tmp")

	// First agent_message_chunk — should set the title.
	s.onNotification("session/update", json.RawMessage(`{
		"sessionId":"test-session-id",
		"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"I'll help you implement that feature."}}
	}`))

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if len(cb.sessions) != 1 || cb.sessions[0].title == "" {
		t.Fatalf("expected title set, sessions = %+v", cb.sessions)
	}
	if !strings.Contains(cb.sessions[0].title, "I'll help") {
		t.Fatalf("title = %q", cb.sessions[0].title)
	}
}

func TestACPSession_autoExtractTitle_truncatesLongText(t *testing.T) {
	cb := &fakeCallbacks{}
	s, _, _ := newTestSession(t, cb)
	cb.recordSessionStart("test-session-id", "/tmp")

	longText := strings.Repeat("a", 200)
	s.onNotification("session/update", json.RawMessage(fmt.Sprintf(`{
		"sessionId":"test-session-id",
		"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":%q}}
	}`, longText)))

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if len(cb.sessions) != 1 {
		t.Fatalf("sessions = %+v", cb.sessions)
	}
	title := cb.sessions[0].title
	// Title should be truncated to ~60 runes + "…"
	if len([]rune(title)) > 70 {
		t.Fatalf("title too long (%d runes): %q", len([]rune(title)), title)
	}
}

func TestACPSession_autoExtractTitle_doesNotOverwriteExisting(t *testing.T) {
	a := &Agent{localSessions: make(map[string]*localSessionInfo)}
	cb := &fakeCallbacks{}
	a.recordSessionStart("test-session-id", "/tmp")
	// Pre-set a title through the Agent so the "already set" guard
	// in recordSessionTitle takes effect.
	a.recordSessionTitle("test-session-id", "Already set")

	s, _, _ := newTestSession(t, cb)
	// Replace the session's callbacks with the Agent so the
	// "already set" guard in Agent.recordSessionTitle is exercised.
	s.callbacks = a

	s.onNotification("session/update", json.RawMessage(`{
		"sessionId":"test-session-id",
		"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"New text"}}
	}`))

	local := a.listLocalSessions("")
	if len(local) != 1 || local[0].Summary != "Already set" {
		t.Fatalf("Summary = %q, want 'Already set'", local[0].Summary)
	}
}

// ─── Phase 3: config_option_update with no model ────────────────────

func TestACPSession_config_option_update_ignoresNonModel(t *testing.T) {
	cb := &fakeCallbacks{}
	s, _, _ := newTestSession(t, cb)

	s.onNotification("session/update", json.RawMessage(`{
		"sessionId":"test-session-id",
		"update":{
			"sessionUpdate":"config_option_update",
			"configOptions":[{"id":"mode","category":"mode","currentValue":"plan"}]
		}
	}`))

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if len(cb.modelCalls) != 0 {
		t.Fatalf("expected 0 model calls for mode config, got %d", len(cb.modelCalls))
	}
}

// ─── Integration: reportModels from session handshake response ──────

func TestAgent_reportModels_thenAvailableModels(t *testing.T) {
	a := &Agent{}
	models := []core.ModelOption{
		{Name: "model-a", Desc: "A"},
		{Name: "model-b", Desc: "B"},
	}
	a.reportModels("model-a", models)

	ctx := context.Background()
	got := a.AvailableModels(ctx)
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2", len(got))
	}
	if got[0].Name != "model-a" || got[0].Desc != "A" {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if got := a.GetModel(); got != "model-a" {
		t.Fatalf("GetModel() = %q, want model-a", got)
	}
}

func TestAgent_reportModels_overwritesPreviousList(t *testing.T) {
	a := &Agent{}
	a.reportModels("m1", []core.ModelOption{{Name: "m1"}})
	a.reportModels("m2", []core.ModelOption{{Name: "m2"}, {Name: "m3"}})

	got := a.AvailableModels(context.Background())
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2", len(got))
	}
	if got := a.GetModel(); got != "m2" {
		t.Fatalf("GetModel() = %q, want m2", got)
	}
}

// ─── Phase 1: ListSessions local fallback path ──────────────────────

func TestAgent_listSessions_returnsLocalWhenUnsupported(t *testing.T) {
	a := &Agent{
		workDir:       "/tmp/test-proj",
		localSessions: make(map[string]*localSessionInfo),
	}
	// listUnsupported is true — simulates server without session/list.
	a.listUnsupported.Store(true)
	a.recordSessionStart("local-1", "/tmp/test-proj")
	a.recordSessionTitle("local-1", "Local session")

	// This should return local sessions without spawning a probe.
	got, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "local-1" {
		t.Fatalf("ListSessions = %+v, want [local-1]", got)
	}
	if got[0].Summary != "Local session" {
		t.Fatalf("Summary = %q", got[0].Summary)
	}
}

func TestAgent_listSessions_returnsEmptyWhenNoLocal(t *testing.T) {
	a := &Agent{
		workDir:       "/tmp/empty",
		localSessions: make(map[string]*localSessionInfo),
	}
	a.listUnsupported.Store(true)
	got, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions error: %v", err)
	}
	if got != nil && len(got) != 0 {
		t.Fatalf("expected nil/empty, got %+v", got)
	}
}

// ─── ensure ModelSwitcher interface is satisfied ─────────────────────

func TestAgent_implementsModelSwitcher(t *testing.T) {
	var _ core.ModelSwitcher = (*Agent)(nil)
}

func TestACPSession_implementsContextUsageReporter(t *testing.T) {
	var _ core.ContextUsageReporter = (*acpSession)(nil)
}

// ─── ensure sessionCallbacks interface is complete ──────────────────

func TestAgent_implementsSessionCallbacks(t *testing.T) {
	var _ sessionCallbacks = (*Agent)(nil)
}

// ─── Phase 1: local session title with empty text is ignored ────────

func TestACPSession_autoExtractTitle_ignoresEmptyText(t *testing.T) {
	cb := &fakeCallbacks{}
	s, _, _ := newTestSession(t, cb)
	cb.recordSessionStart("test-session-id", "/tmp")

	s.onNotification("session/update", json.RawMessage(`{
		"sessionId":"test-session-id",
		"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":""}}
	}`))

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if len(cb.sessions) != 1 && cb.sessions[0].title != "" {
		t.Fatalf("expected empty title, got %q", cb.sessions[0].title)
	}
}

// ─── Phase 2: parseModels with mixed configOptions ───────────────────

func TestParseModels_mixedConfigOptions(t *testing.T) {
	opts := []acpConfigOption{
		{ID: "mode", Category: "mode", CurrentValue: "plan"},
		{ID: "thought_level", Category: "thought_level", CurrentValue: "high"},
		{
			ID:           "model",
			Category:     "model",
			CurrentValue: "glm-5.2-ioa",
			Options: []acpConfigOptValue{
				{Value: "glm-5.2-ioa", Name: "GLM 5.2 IOA"},
				{Value: "glm-5.2-air", Name: "GLM 5.2 Air"},
			},
		},
	}
	current, available := parseModels(opts, nil)
	if current != "glm-5.2-ioa" {
		t.Fatalf("current = %q, want glm-5.2-ioa", current)
	}
	if len(available) != 2 {
		t.Fatalf("available len = %d, want 2", len(available))
	}
}

// ─── Phase 3: usage_update with missing fields ──────────────────────

func TestACPSession_usage_update_partialData(t *testing.T) {
	s, _, _ := newTestSession(t, nil)

	// Only "used" present, "size" missing.
	s.onNotification("session/update", json.RawMessage(`{
		"sessionId":"test-session-id",
		"update":{"sessionUpdate":"usage_update","used":1000}
	}`))

	usage := s.GetContextUsage()
	if usage == nil {
		t.Fatal("GetContextUsage() = nil")
	}
	if usage.UsedTokens != 1000 {
		t.Fatalf("UsedTokens = %d, want 1000", usage.UsedTokens)
	}
	if usage.ContextWindow != 0 {
		t.Fatalf("ContextWindow = %d, want 0 (missing)", usage.ContextWindow)
	}
}

// ─── verify SetModel with empty session ID ──────────────────────────

func TestACPSession_SetModel_emptySessionID(t *testing.T) {
	s, _, _ := newTestSession(t, nil)
	s.setACPSessionID("")
	if err := s.SetModel("any"); err == nil {
		t.Fatal("expected error with empty session id")
	}
}
