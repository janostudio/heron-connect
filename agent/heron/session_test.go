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
