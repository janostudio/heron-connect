package acp

import (
	"encoding/json"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestMapSessionUpdate_agentMessageChunk(t *testing.T) {
	params := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "agent_message_chunk",
			"content": {"type": "text", "text": "hello"}
		}
	}`)
	evs := mapSessionUpdate("", params)
	if len(evs) != 1 || evs[0].Type != core.EventText || evs[0].Content != "hello" {
		t.Fatalf("got %+v", evs)
	}
}

func TestSessionUpdateMapper_marksSubagentEvents(t *testing.T) {
	var mapper sessionUpdateMapper

	parent := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call",
			"toolCallId": "agent-1",
			"title": "Agent",
			"kind": "other"
		}
	}`)
	if evs := mapper.mapSessionUpdate("s1", parent); len(evs) != 1 || evs[0].Type != core.EventToolUse || evs[0].IsSubagent {
		t.Fatalf("got %+v, want top-level Agent tool event", evs)
	}

	tests := []struct {
		name   string
		params json.RawMessage
		want   core.EventType
	}{
		{
			name: "text",
			params: json.RawMessage(`{
				"sessionId": "s1",
				"_meta": {"codebuddy.ai": {"parentToolUseId": "agent-1"}},
				"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "child text"}}
			}`),
			want: core.EventText,
		},
		{
			name: "thinking",
			params: json.RawMessage(`{
				"sessionId": "s1",
				"_meta": {"codebuddy.ai": {"parentToolUseId": "agent-1"}},
				"update": {"sessionUpdate": "reasoning_chunk", "content": {"type": "text", "text": "child thought"}}
			}`),
			want: core.EventThinking,
		},
		{
			name: "tool call",
			params: json.RawMessage(`{
				"sessionId": "s1",
				"_meta": {"codebuddy.ai": {"parentToolUseId": "agent-1"}},
				"update": {"sessionUpdate": "tool_call", "toolCallId": "child-tool", "title": "Read", "kind": "read"}
			}`),
			want: core.EventToolUse,
		},
		{
			name: "tool result",
			params: json.RawMessage(`{
				"sessionId": "s1",
				"_meta": {"codebuddy.ai": {"parentToolUseId": "agent-1"}},
				"update": {"sessionUpdate": "tool_call_update", "toolCallId": "child-tool", "title": "Read", "status": "completed", "rawOutput": {"type": "text", "text": "child result"}}
			}`),
			want: core.EventToolResult,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs := mapper.mapSessionUpdate("s1", tt.params)
			if len(evs) != 1 || evs[0].Type != tt.want || !evs[0].IsSubagent {
				t.Fatalf("got %+v, want marked %s event", evs, tt.want)
			}
		})
	}
}

func TestSessionUpdateMapper_keepsEventsForNonAgentParents(t *testing.T) {
	var mapper sessionUpdateMapper

	parent := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call",
			"toolCallId": "search-1",
			"title": "Search",
			"kind": "search"
		}
	}`)
	mapper.mapSessionUpdate("s1", parent)

	child := json.RawMessage(`{
		"sessionId": "s1",
		"_meta": {"codebuddy.ai": {"parentToolUseId": "search-1"}},
		"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "keep me"}}
	}`)
	if evs := mapper.mapSessionUpdate("s1", child); len(evs) != 1 || evs[0].Content != "keep me" || evs[0].IsSubagent {
		t.Fatalf("got %+v, want unmarked non-Agent parent event", evs)
	}
}

func TestSessionUpdateMapper_marksNestedSubagentEvents(t *testing.T) {
	var mapper sessionUpdateMapper

	for _, params := range []json.RawMessage{
		json.RawMessage(`{
			"sessionId": "s1",
			"update": {"sessionUpdate": "tool_call", "toolCallId": "agent-1", "title": "Agent", "kind": "other"}
		}`),
		json.RawMessage(`{
			"sessionId": "s1",
			"_meta": {"codebuddy.ai": {"parentToolUseId": "agent-1"}},
			"update": {"sessionUpdate": "tool_call", "toolCallId": "agent-2", "title": "Agent", "kind": "other"}
		}`),
	} {
		mapper.mapSessionUpdate("s1", params)
	}

	grandchild := json.RawMessage(`{
		"sessionId": "s1",
		"_meta": {"codebuddy.ai": {"parentToolUseId": "agent-2"}},
		"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "nested"}}
	}`)
	if evs := mapper.mapSessionUpdate("s1", grandchild); len(evs) != 1 || !evs[0].IsSubagent {
		t.Fatalf("got %+v, want marked nested subagent event", evs)
	}
}

func TestMapSessionUpdate_toolCallUpdate_inProgress(t *testing.T) {
	params := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call_update",
			"toolCallId": "c1",
			"title": "Run",
			"status": "in_progress",
			"content": [
				{"type": "content", "content": {"type": "text", "text": "partial output"}}
			]
		}
	}`)
	evs := mapSessionUpdate("", params)
	if len(evs) != 0 {
		t.Fatalf("got %+v, want no IM-visible event for in_progress tool_call_update", evs)
	}
}

func TestMapSessionUpdate_toolCallUpdate_completed(t *testing.T) {
	params := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call_update",
			"toolCallId": "c1",
			"title": "Run",
			"status": "completed",
			"content": [
				{"type": "content", "content": {"type": "text", "text": "final output"}}
			]
		}
	}`)
	evs := mapSessionUpdate("", params)
	if len(evs) != 1 || evs[0].Type != core.EventToolResult || evs[0].ToolName != "Run" || evs[0].Content != "final output" {
		t.Fatalf("got %+v", evs)
	}
}

func TestMapSessionUpdate_toolCallUpdate_completedRawOutputWins(t *testing.T) {
	params := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call_update",
			"toolCallId": "c1",
			"title": "Bash",
			"status": "completed",
			"content": [
				{"type": "content", "content": {"type": "text", "text": "fragment"}}
			],
			"rawOutput": {
				"type": "text",
				"text": "Command: wc -m file\nStdout: 365 file\nExit Code: 0"
			}
		}
	}`)
	evs := mapSessionUpdate("", params)
	if len(evs) != 1 {
		t.Fatalf("got %+v", evs)
	}
	if evs[0].ToolResult != "Command: wc -m file\nStdout: 365 file\nExit Code: 0" {
		t.Fatalf("ToolResult = %q", evs[0].ToolResult)
	}
}

func TestMapSessionUpdate_toolCallUpdate_completedRawOutputFallback(t *testing.T) {
	params := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call_update",
			"toolCallId": "c1",
			"title": "Bash",
			"status": "completed",
			"rawOutput": {
				"type": "text",
				"text": "Command: wc -m file\nStdout: 365 file\nExit Code: 0"
			}
		}
	}`)
	evs := mapSessionUpdate("", params)
	if len(evs) != 1 {
		t.Fatalf("got %+v", evs)
	}
	if evs[0].Type != core.EventToolResult {
		t.Fatalf("type = %s, want %s", evs[0].Type, core.EventToolResult)
	}
	if evs[0].ToolResult == "" {
		t.Fatalf("ToolResult should be populated from rawOutput, got %+v", evs[0])
	}
	if evs[0].ToolStatus != "completed" {
		t.Fatalf("ToolStatus = %q, want completed", evs[0].ToolStatus)
	}
	if evs[0].Content == "" {
		t.Fatalf("Content should also be populated, got %+v", evs[0])
	}
}

func TestMapSessionUpdate_reasoningChunk(t *testing.T) {
	params := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "reasoning_chunk",
			"content": {"type": "text", "text": "step 1"}
		}
	}`)
	evs := mapSessionUpdate("", params)
	if len(evs) != 1 || evs[0].Type != core.EventThinking || evs[0].Content != "step 1" {
		t.Fatalf("got %+v", evs)
	}
}

func TestMapSessionUpdate_toolCall(t *testing.T) {
	params := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call",
			"toolCallId": "c1",
			"title": "Read file",
			"kind": "read",
			"status": "pending"
		}
	}`)
	evs := mapSessionUpdate("", params)
	if len(evs) != 1 || evs[0].Type != core.EventToolUse || evs[0].ToolName != "Read file" {
		t.Fatalf("got %+v", evs)
	}
}

func TestPickPermissionOptionID(t *testing.T) {
	opts := []permissionOption{
		{OptionID: "a", Kind: "allow_once"},
		{OptionID: "r", Kind: "reject_once"},
	}
	if pickPermissionOptionID(true, opts) != "a" {
		t.Fatal("allow")
	}
	if pickPermissionOptionID(false, opts) != "r" {
		t.Fatal("deny")
	}
}

func TestBuildPermissionResult(t *testing.T) {
	allow := buildPermissionResult(true, "opt1")
	if allow["outcome"].(map[string]any)["optionId"] != "opt1" {
		t.Fatalf("%v", allow)
	}
	denySel := buildPermissionResult(false, "rej")
	if denySel["outcome"].(map[string]any)["optionId"] != "rej" {
		t.Fatalf("%v", denySel)
	}
	cancel := buildPermissionResult(false, "")
	if cancel["outcome"].(map[string]any)["outcome"] != "cancelled" {
		t.Fatalf("%v", cancel)
	}
}

func TestMapSessionUpdate_toolCall_withRawInput(t *testing.T) {
	params := json.RawMessage(`{
		"sessionId": "s1",
		"update": {
			"sessionUpdate": "tool_call",
			"toolCallId": "c1",
			"title": "Bash",
			"kind": "bash",
			"status": "pending",
			"rawInput": {"command": "ls -la /tmp"}
		}
	}`)
	evs := mapSessionUpdate("", params)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if evs[0].Type != core.EventToolUse {
		t.Fatalf("expected EventToolUse, got %s", evs[0].Type)
	}
	if evs[0].ToolInput != "ls -la /tmp" {
		t.Fatalf("expected tool input 'ls -la /tmp', got %q", evs[0].ToolInput)
	}
}

func TestSummarizeACPToolInput(t *testing.T) {
	tests := []struct {
		name string
		kind string
		raw  string
		want string
	}{
		{
			name: "bash command",
			kind: "bash",
			raw:  `{"command": "echo hello"}`,
			want: "echo hello",
		},
		{
			name: "read file",
			kind: "read",
			raw:  `{"file_path": "/tmp/test.txt"}`,
			want: "/tmp/test.txt",
		},
		{
			name: "empty raw",
			kind: "bash",
			raw:  "",
			want: "",
		},
		{
			name: "unknown kind falls back to formatted JSON",
			kind: "unknown_tool",
			raw:  `{"key": "value"}`,
			want: "{\n  \"key\": \"value\"\n}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw json.RawMessage
			if tt.raw != "" {
				raw = json.RawMessage(tt.raw)
			}
			got := summarizeACPToolInput(tt.kind, raw)
			if got != tt.want {
				t.Errorf("summarizeACPToolInput(%q, %s) = %q, want %q", tt.kind, tt.raw, got, tt.want)
			}
		})
	}
}
