package codebuddy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

// ── normalizeMode tests ─────────────────────────────────────

func TestNormalizeMode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"yolo", "yolo"},
		{"YOLO", "yolo"},
		{"bypass", "yolo"},
		{"dangerously-skip-permissions", "yolo"},
		{"default", "default"},
		{"", "default"},
		{"unknown", "default"},
		{"  yolo  ", "yolo"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeMode(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeMode(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ── Agent identity tests ────────────────────────────────────

func TestAgent_Name(t *testing.T) {
	a := &Agent{}
	if got := a.Name(); got != "codebuddy" {
		t.Errorf("Name() = %q, want %q", got, "codebuddy")
	}
}

func TestAgent_CLIBinaryName(t *testing.T) {
	a := &Agent{}
	if got := a.CLIBinaryName(); got != "codebuddy" {
		t.Errorf("CLIBinaryName() = %q, want %q", got, "codebuddy")
	}
}

func TestAgent_CLIDisplayName(t *testing.T) {
	a := &Agent{}
	if got := a.CLIDisplayName(); got != "CodeBuddy" {
		t.Errorf("CLIDisplayName() = %q, want %q", got, "CodeBuddy")
	}
}

func TestAgent_SetWorkDir(t *testing.T) {
	a := &Agent{}
	a.SetWorkDir("/tmp/test")
	if got := a.GetWorkDir(); got != "/tmp/test" {
		t.Errorf("GetWorkDir() = %q, want %q", got, "/tmp/test")
	}
}

func TestAgent_SetModel(t *testing.T) {
	a := &Agent{}
	a.SetModel("claude-sonnet-4-6")
	a.mu.Lock()
	got := a.model
	a.mu.Unlock()
	if got != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want %q", got, "claude-sonnet-4-6")
	}
}

func TestAgent_SetMode(t *testing.T) {
	a := &Agent{}
	a.SetMode("yolo")
	if got := a.GetMode(); got != "yolo" {
		t.Errorf("GetMode() = %q, want %q", got, "yolo")
	}
}

func TestAgent_AvailableModels(t *testing.T) {
	a := &Agent{}
	models := a.AvailableModels(context.Background())
	if len(models) == 0 {
		t.Error("AvailableModels() returned empty list")
	}
}

func TestAgent_PermissionModes(t *testing.T) {
	a := &Agent{}
	modes := a.PermissionModes()
	if len(modes) != 2 {
		t.Errorf("PermissionModes() = %d items, want 2", len(modes))
	}
}

func TestAgent_SkillDirs(t *testing.T) {
	a := &Agent{workDir: "/tmp/test"}
	dirs := a.SkillDirs()
	if len(dirs) != 2 {
		t.Errorf("SkillDirs() = %d items, want 2", len(dirs))
	}
}

// verify Agent implements core.Agent
var _ core.Agent = (*Agent)(nil)

// ── handleEvent unit tests ──────────────────────────────────

func newTestSession() *codebuddySession {
	ctx, cancel := context.WithCancel(context.Background())
	cs := &codebuddySession{
		events: make(chan core.Event, 64),
		ctx:    ctx,
		cancel: cancel,
	}
	cs.alive.Store(true)
	return cs
}

func TestHandleAssistant_Text(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	content, _ := json.Marshal([]contentItem{
		{Type: "text", Text: "hello world"},
	})
	ev := &streamEvent{
		Type:      "assistant",
		SessionID: "test-sid-1",
		Message: &streamMessage{
			StopReason: "end_turn",
			Content:    content,
		},
	}
	cs.handleAssistant(ev, "")

	select {
	case got := <-cs.events:
		if got.Type != core.EventText || got.Content != "hello world" {
			t.Errorf("got type=%s content=%q, want EventText/hello world", got.Type, got.Content)
		}
	default:
		t.Error("expected a text event but channel was empty")
	}
}

func TestHandleAssistant_ToolUse(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	inputJSON, _ := json.Marshal(map[string]string{"command": "ls"})
	content, _ := json.Marshal([]contentItem{
		{Type: "tool_use", Name: "Bash", Input: inputJSON},
	})
	ev := &streamEvent{
		Type:      "assistant",
		SessionID: "test-sid-2",
		Message: &streamMessage{
			StopReason: "tool_use",
			Content:    content,
		},
	}
	cs.handleAssistant(ev, "")

	select {
	case got := <-cs.events:
		if got.Type != core.EventToolUse || got.ToolName != "Bash" {
			t.Errorf("got type=%s tool=%s, want EventToolUse/Bash", got.Type, got.ToolName)
		}
	default:
		t.Error("expected a tool_use event but channel was empty")
	}
}

func TestHandleAssistant_SkipsNonFinished(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	content, _ := json.Marshal([]contentItem{
		{Type: "text", Text: "partial"},
	})
	ev := &streamEvent{
		Type: "assistant",
		Message: &streamMessage{
			Content: content,
			// StopReason is empty — incomplete message, should be skipped
		},
	}
	cs.handleAssistant(ev, "")

	select {
	case got := <-cs.events:
		t.Errorf("expected no event, got type=%s content=%q", got.Type, got.Content)
	default:
		// ok
	}
}

func TestHandleUser_ToolResult(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	resultContent, _ := json.Marshal([]contentItem{
		{Type: "text", Text: "Command: ls\nStdout: file.txt"},
	})
	content, _ := json.Marshal([]contentItem{
		{Type: "tool_result", ToolUseID: "call_123", Content: resultContent},
	})
	ev := &streamEvent{
		Type:      "user",
		SessionID: "test-sid-3",
		Message: &streamMessage{
			Content: content,
		},
	}
	cs.handleUser(ev)

	select {
	case got := <-cs.events:
		if got.Type != core.EventToolResult {
			t.Errorf("got type=%s, want EventToolResult", got.Type)
		}
	default:
		t.Error("expected a tool_result event but channel was empty")
	}
}

func TestHandleResult(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	ev := &streamEvent{
		Type:      "result",
		Subtype:   "success",
		SessionID: "test-sid-4",
		Result:    "final output",
		IsError:   false,
	}
	cs.handleResult(ev, "")

	select {
	case got := <-cs.events:
		if got.Type != core.EventResult || got.Content != "final output" {
			t.Errorf("got type=%s content=%q, want EventResult/final output", got.Type, got.Content)
		}
		if !got.Done {
			t.Error("expected Done=true")
		}
	default:
		t.Error("expected a result event but channel was empty")
	}
}

func TestHandleResult_Error(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	ev := &streamEvent{
		Type:    "result",
		Subtype: "error",
		Result:  "something went wrong",
		IsError: true,
	}
	cs.handleResult(ev, "")

	select {
	case got := <-cs.events:
		if got.Type != core.EventResult {
			t.Errorf("got type=%s, want EventResult", got.Type)
		}
		if !got.Done {
			t.Error("expected Done=true")
		}
	default:
		t.Error("expected a result event but channel was empty")
	}
}
