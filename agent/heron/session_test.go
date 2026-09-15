package heron

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

func newTestHeronSession() *heronSession {
	ctx, cancel := context.WithCancel(context.Background())
	session := &heronSession{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan core.Event, 8),
		done:   make(chan struct{}),
	}
	session.alive.Store(true)
	return session
}

func TestAgentNewAddsJSONRPCFlag(t *testing.T) {
	agent, err := New(map[string]any{
		"command": "true",
		"args":    []any{"--flow", "default.yml"},
	})
	if err != nil {
		t.Fatal(err)
	}

	heronAgent, ok := agent.(*Agent)
	if !ok {
		t.Fatalf("agent type = %T, want *Agent", agent)
	}
	if len(heronAgent.args) != 3 ||
		heronAgent.args[0] != "--json-rpc" ||
		heronAgent.args[1] != "--flow" ||
		heronAgent.args[2] != "default.yml" {
		t.Fatalf("args = %#v", heronAgent.args)
	}
}

func TestHeronSessionSendWritesTurnRequest(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	session := newTestHeronSession()
	session.stdin = writer
	session.sessionID.Store("fs-1")
	defer session.cancel()

	err = session.Send("continue", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var request heronJSONRPCRequest
	if err := json.Unmarshal(bytes.TrimSpace(data), &request); err != nil {
		t.Fatal(err)
	}
	if request.JSONRPC != "2.0" || request.Method != "turn" {
		t.Fatalf("request = %+v", request)
	}
	if string(request.ID) != "1" {
		t.Fatalf("request id = %s, want 1", request.ID)
	}
	var params map[string]string
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["session_id"] != "fs-1" || params["input"] != "continue" {
		t.Fatalf("params = %#v", params)
	}
}

func TestHeronSessionHandleResponseEmitsResult(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	err := session.handleResponse(heronJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Result: mustJSON(map[string]any{
			"session_id":   "fs-2",
			"flow_turn_id": "ft-1",
			"status":       "completed",
			"reply":        "done",
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	event := <-session.events
	if event.Type != core.EventResult || event.Content != "done" || !event.Done {
		t.Fatalf("event = %+v", event)
	}
	if event.SessionID != "fs-2" || event.InputTokens != 10 || event.OutputTokens != 20 {
		t.Fatalf("event = %+v", event)
	}
	if session.CurrentSessionID() != "fs-2" {
		t.Fatalf("session id = %q", session.CurrentSessionID())
	}
}

func TestHeronSessionHandleErrorResponseEmitsError(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	err := session.handleResponse(heronJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"1"`),
		Error: &heronJSONRPCError{
			Code:    -32001,
			Message: "flow failed",
			Data:    mustJSON(map[string]string{"session_id": "fs-3"}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	event := <-session.events
	if event.Type != core.EventError || !strings.Contains(event.Error.Error(), "flow failed") {
		t.Fatalf("event = %+v", event)
	}
	if event.SessionID != "fs-3" {
		t.Fatalf("session id = %q", event.SessionID)
	}
}

func TestHeronSessionProgressNotificationEmitsThinking(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	err := session.handleNotification("progress", mustJSON(map[string]string{
		"content": "running diagnose",
	}))
	if err != nil {
		t.Fatal(err)
	}
	event := <-session.events
	if event.Type != core.EventThinking || event.Content != "running diagnose" {
		t.Fatalf("event = %+v", event)
	}
}

func TestAgentModelSwitcher(t *testing.T) {
	agent, err := New(map[string]any{
		"command": "true",
		"model":   "gpt-5.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	sw, ok := agent.(core.ModelSwitcher)
	if !ok {
		t.Fatalf("agent does not implement ModelSwitcher, type = %T", agent)
	}

	if sw.GetModel() != "gpt-5.4" {
		t.Fatalf("GetModel = %q, want gpt-5.4", sw.GetModel())
	}
	sw.SetModel("claude-sonnet-5")
	if sw.GetModel() != "claude-sonnet-5" {
		t.Fatalf("GetModel after SetModel = %q, want claude-sonnet-5", sw.GetModel())
	}
}

func TestAgentAvailableModels_ReadsModelsJSON(t *testing.T) {
	dir := t.TempDir()
	agentsDir := dir + "/.agents"
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelsJSON := `{"model":"gpt-5.4","models":[
		{"id":"gpt-5.4","name":"GPT-5.4"},
		{"id":"claude-sonnet-5","name":"Claude Sonnet 5"},
		{"id":"glm-5.2-ioa","name":"GLM 5.2 IOA"}
	]}`
	if err := os.WriteFile(agentsDir+"/models.json", []byte(modelsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	agent, err := New(map[string]any{
		"command":  "true",
		"work_dir": dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	sw := agent.(core.ModelSwitcher)

	models := sw.AvailableModels(context.Background())
	if len(models) != 3 {
		t.Fatalf("AvailableModels len = %d, want 3", len(models))
	}
	if models[0].Name != "gpt-5.4" || models[1].Name != "claude-sonnet-5" || models[2].Name != "glm-5.2-ioa" {
		t.Fatalf("model names = %#v", models)
	}
	if models[1].Desc != "Claude Sonnet 5" {
		t.Fatalf("model desc = %q, want 'Claude Sonnet 5'", models[1].Desc)
	}
}

func TestAgentAvailableModels_MissingFile(t *testing.T) {
	agent, err := New(map[string]any{
		"command":  "true",
		"work_dir": t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	sw := agent.(core.ModelSwitcher)
	if models := sw.AvailableModels(context.Background()); len(models) != 0 {
		t.Fatalf("expected empty model list for missing models.json, got %#v", models)
	}
}
