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

// TestHeronSessionRelaysUnattendedTurnReply covers the async-dispatch gap: a
// user turn answers "handed off", then Heron keeps working in a mailbox-driven
// turn. That later turn's reply must reach the caller as an unattended result,
// otherwise the answer exists only in the engine's own records.
func TestHeronSessionRelaysUnattendedTurnReply(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	session.progressMu.Lock()
	session.progressSeen = map[string]int64{"ft-user": 0}
	session.progressOrder = map[string]uint64{"ft-user": 1}
	session.turnSequence = 1
	session.progressMu.Unlock()

	frames := []string{
		`{"session_id":"fs-1","flow_turn_id":"ft-user","seq":1,"event_id":"fs-1-1","type":"assistant","content":[{"type":"text","text":"handed off"}]}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-user","seq":2,"event_id":"fs-1-2","type":"flow_turn_completed","reply":"已转给执行侧核实","status":"waiting_input"}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-bg","seq":3,"event_id":"fs-1-3","type":"flow_turn_started","origin":"mailbox"}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-bg","seq":4,"event_id":"fs-1-4","type":"assistant","content":[{"type":"text","text":"raw model json"}]}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-bg","seq":5,"event_id":"fs-1-5","type":"flow_turn_completed","origin":"mailbox","reply":"heron-ai 的主分支是 master","status":"completed","team_id":"default"}`,
	}
	for _, frame := range frames {
		if err := session.handleNotification("progress", json.RawMessage(frame)); err != nil {
			t.Fatal(err)
		}
	}

	var results []core.Event
	for len(session.events) > 0 {
		event := <-session.events
		if event.Type == core.EventResult {
			results = append(results, event)
		}
	}
	if len(results) != 1 {
		t.Fatalf("want exactly one relayed result, got %d: %+v", len(results), results)
	}
	if got := results[0]; got.Content != "heron-ai 的主分支是 master" || got.Metadata["flow_turn_id"] != "ft-bg" {
		t.Fatalf("relayed result = %+v", got)
	}
}

// TestHeronSessionDropsIntermediateBackgroundReplies guards the chained
// hand-off case (default -> owner -> other): only the turn that reports back to
// the entry team carries an answer. A Team handing work to its activator is an
// internal step and must not reach the user as a reply.
func TestHeronSessionDropsIntermediateBackgroundReplies(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	session.progressMu.Lock()
	session.progressSeen = map[string]int64{"ft-user": 0}
	session.progressOrder = map[string]uint64{"ft-user": 1}
	session.turnSequence = 1
	session.progressMu.Unlock()

	frame := `{"session_id":"fs-1","flow_turn_id":"ft-owner","seq":7,"event_id":"fs-1-7","type":"flow_turn_completed","origin":"mailbox","intermediate":true,"reply":"owner 已拿到 other 的结果，继续处理"}`
	if err := session.handleNotification("progress", json.RawMessage(frame)); err != nil {
		t.Fatal(err)
	}
	if len(session.events) != 0 {
		t.Fatalf("intermediate background reply leaked %d events", len(session.events))
	}
}

// TestHeronSessionFirstTurnProgressPrecedesSingleResponseResult exercises the
// real Send reset path: the first FlowTurn's assistant text must arrive as
// EventText, and its progress completion must not duplicate the JSON-RPC result.
func TestHeronSessionFirstTurnProgressPrecedesSingleResponseResult(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	session := newTestHeronSession()
	session.stdin = writer
	defer session.cancel()

	if err := session.Send("find the default branch", nil, nil); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}

	frames := []string{
		`{"session_id":"fs-first","flow_turn_id":"ft-first","seq":1,"event_id":"fs-first-1","type":"assistant","content":[{"type":"text","text":"The default branch is main."}]}`,
		`{"session_id":"fs-first","flow_turn_id":"ft-first","seq":2,"event_id":"fs-first-2","type":"flow_turn_completed","origin":"user","reply":"The default branch is main.","status":"waiting_input"}`,
	}
	for _, frame := range frames {
		if err := session.handleNotification("progress", json.RawMessage(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.handleResponse(heronJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Result: mustJSON(map[string]any{
			"session_id":   "fs-first",
			"flow_turn_id": "ft-first",
			"status":       "waiting_input",
			"reply":        "The default branch is main.",
		}),
	}); err != nil {
		t.Fatal(err)
	}

	if got := <-session.events; got.Type != core.EventText || got.Content != "The default branch is main." {
		t.Fatalf("first-turn progress event = %+v, want assistant text", got)
	}
	if got := <-session.events; got.Type != core.EventResult || got.Content != "The default branch is main." {
		t.Fatalf("turn response event = %+v, want exactly one final result", got)
	}
	if len(session.events) != 0 {
		t.Fatalf("user turn emitted duplicate results: %d extra events", len(session.events))
	}
}

// TestHeronSessionTeamProgressSurfacesAsThinking covers the progress line a
// background Team adds, so the UI can show work continuing instead of silence.
func TestHeronSessionTeamProgressSurfacesAsThinking(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	session.progressMu.Lock()
	session.progressSeen = map[string]int64{"ft-user": 0}
	session.progressOrder = map[string]uint64{"ft-user": 1}
	session.turnSequence = 1
	session.progressMu.Unlock()

	frame := `{"session_id":"fs-1","flow_turn_id":"ft-bg","seq":9,"event_id":"fs-1-9","type":"team_turn_completed","team_id":"repo-info","status":"completed"}`
	if err := session.handleNotification("progress", json.RawMessage(frame)); err != nil {
		t.Fatal(err)
	}
	if got := <-session.events; got.Type != core.EventThinking || !strings.Contains(got.Content, "repo-info") {
		t.Fatalf("team progress event = %+v", got)
	}
}

// TestHeronSessionEmptyReplyIsNotError pins the trigger of the whole incident:
// an empty reply used to be an EventError, which flags the session for a resync
// and stops the background reader from ever starting — taking every later
// background result with it.
func TestHeronSessionEmptyReplyIsNotError(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	defer session.cancel()
	err := session.handleResponse(heronJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Result: mustJSON(map[string]any{
			"session_id":   "fs-9",
			"flow_turn_id": "ft-9",
			"status":       "waiting_input",
			"reply":        "",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	event := <-session.events
	if event.Type != core.EventResult {
		t.Fatalf("empty reply event type = %v, want EventResult", event.Type)
	}
	if !event.Done || event.Content != emptyReplyPlaceholder {
		t.Fatalf("empty reply event = %+v", event)
	}
	if empty, _ := event.Metadata["empty_reply"].(bool); !empty {
		t.Fatalf("empty reply metadata = %#v", event.Metadata)
	}
}

// TestHeronSessionAcceptsProgressAcrossFlowTurns is the regression test for the
// pinned-turn filter: both turns of one Send cycle must be accepted.
func TestHeronSessionAcceptsProgressAcrossFlowTurns(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	first := heronProgress{SessionID: "fs-1", FlowTurnID: "ft-a", Seq: 1, EventID: "e1", Type: "flow_turn_started"}
	second := heronProgress{SessionID: "fs-1", FlowTurnID: "ft-b", Seq: 2, EventID: "e2", Type: "flow_turn_started"}
	if !session.acceptProgress(first) {
		t.Fatal("first turn rejected")
	}
	if !session.acceptProgress(second) {
		t.Fatal("later turn of the same Send was rejected")
	}
	if session.acceptProgress(heronProgress{SessionID: "fs-1", FlowTurnID: "ft-b", Seq: 2, EventID: "e2", Type: "flow_turn_started"}) {
		t.Fatal("duplicate seq accepted")
	}
	if !session.progressIsUserTurn("ft-a") {
		t.Fatal("first turn should be the user turn")
	}
	if session.progressIsUserTurn("ft-b") {
		t.Fatal("later turn should not be the user turn")
	}
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
	session.progressSeen = map[string]int64{"old-turn": 90}
	session.progressStatus = "waiting_approval"
	session.pendingApprovals = []heronPendingApproval{{RequestID: "old-approval"}}
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
	if len(session.progressSeen) != 0 || session.progressStatus != "" || len(session.pendingApprovals) != 0 {
		t.Fatalf("progress turn state was not reset: seen=%v status=%q approvals=%#v", session.progressSeen, session.progressStatus, session.pendingApprovals)
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

func TestHeronSessionStructuredProgressMapsAssistantAndToolEvents(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	frames := []string{
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":8,"event_id":"fs-1-8","type":"assistant","content":[{"type":"tool_use","id":"tc-1","name":"Read","input":{"file":"README.md"}}]}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":9,"event_id":"fs-1-9","type":"tool_use","tool_use_id":"ct-1/1/tc-1","name":"Read","input":{"file":"README.md"}}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":10,"event_id":"fs-1-10","type":"tool_result","tool_use_id":"ct-1/1/tc-1","name":"Read","content":"file contents","is_error":false,"status":"completed","exit_code":0}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":11,"event_id":"fs-1-11","type":"assistant","content":[{"type":"text","text":"Here is the summary."}]}`,
	}
	for _, frame := range frames {
		if err := session.handleNotification("progress", json.RawMessage(frame)); err != nil {
			t.Fatal(err)
		}
	}

	toolUse := <-session.events
	if toolUse.Type != core.EventToolUse || toolUse.ToolID != "ct-1/1/tc-1" || toolUse.ToolName != "Read" {
		t.Fatalf("tool use event = %+v", toolUse)
	}
	if toolUse.ToolInput != "README.md" || toolUse.ToolInputRaw["file"] != "README.md" {
		t.Fatalf("tool input = %q, raw = %#v", toolUse.ToolInput, toolUse.ToolInputRaw)
	}

	toolResult := <-session.events
	if toolResult.Type != core.EventToolResult || toolResult.ToolID != toolUse.ToolID || toolResult.ToolResult != "file contents" {
		t.Fatalf("tool result event = %+v", toolResult)
	}
	if toolResult.ToolStatus != "completed" || toolResult.ToolSuccess == nil || !*toolResult.ToolSuccess {
		t.Fatalf("tool result status = %+v", toolResult)
	}
	if toolResult.ToolExitCode == nil || *toolResult.ToolExitCode != 0 {
		t.Fatalf("tool exit code = %+v", toolResult.ToolExitCode)
	}

	text := <-session.events
	if text.Type != core.EventText || text.Content != "Here is the summary." || text.SessionID != "fs-1" {
		t.Fatalf("text event = %+v", text)
	}
	if len(session.events) != 0 {
		t.Fatalf("unexpected extra events: %d", len(session.events))
	}

	if err := session.handleResponse(heronJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Result: mustJSON(map[string]any{
			"session_id":   "fs-1",
			"flow_turn_id": "ft-1",
			"status":       "waiting_input",
			"reply":        "Here is the summary.",
			"usage": map[string]int{
				"prompt_tokens":     12,
				"completion_tokens": 6,
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	result := <-session.events
	if result.Type != core.EventResult || result.Content != text.Content || result.InputTokens != 12 || result.OutputTokens != 6 {
		t.Fatalf("final result = %+v", result)
	}
}

func TestHeronSessionToolFailureMapsErrorState(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	frame := `{"session_id":"fs-1","flow_turn_id":"ft-1","seq":10,"event_id":"fs-1-10","type":"tool_result","tool_use_id":"ct-1/1/tc-1","name":"Bash","content":"command failed","is_error":true,"status":"failed"}`
	if err := session.handleNotification("progress", json.RawMessage(frame)); err != nil {
		t.Fatal(err)
	}
	event := <-session.events
	if event.Type != core.EventToolResult || event.ToolResult != "command failed" || event.ToolStatus != "failed" {
		t.Fatalf("tool failure event = %+v", event)
	}
	if event.ToolSuccess == nil || *event.ToolSuccess {
		t.Fatalf("tool success = %+v, want false", event.ToolSuccess)
	}
}

func TestHeronSessionProgressSequenceDeduplicatesAndAllowsGaps(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	frames := []string{
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":3,"event_id":"fs-1-3","type":"flow_turn_started"}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":5,"event_id":"fs-1-5","type":"assistant","content":[{"type":"text","text":"first"}]}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":5,"event_id":"fs-1-5","type":"assistant","content":[{"type":"text","text":"duplicate"}]}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":4,"event_id":"fs-1-4","type":"assistant","content":[{"type":"text","text":"out of order"}]}`,
		`{"session_id":"fs-1","flow_turn_id":"ft-1","seq":9,"event_id":"fs-1-9","type":"assistant","content":[{"type":"text","text":"after gap"}]}`,
	}
	for _, frame := range frames {
		if err := session.handleNotification("progress", json.RawMessage(frame)); err != nil {
			t.Fatal(err)
		}
	}

	if got := <-session.events; got.Type != core.EventText || got.Content != "first" {
		t.Fatalf("first text event = %+v", got)
	}
	if got := <-session.events; got.Type != core.EventText || got.Content != "after gap" {
		t.Fatalf("text event after seq gap = %+v", got)
	}
	if len(session.events) != 0 {
		t.Fatalf("duplicate or out-of-order frames emitted %d extra events", len(session.events))
	}
}

func TestHeronSessionWaitingResponseEmitsSuspendedNotError(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	if err := session.handleNotification("progress", json.RawMessage(`{"session_id":"fs-waiting","flow_turn_id":"ft-waiting","type":"status","status":"waiting_approval","pending_approvals":[{"request_id":"ap-1","call_id":"reviewer","tool_name":"Bash","reason":"run command"}]}`)); err != nil {
		t.Fatal(err)
	}

	err := session.handleResponse(heronJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Result: mustJSON(map[string]any{
			"session_id":   "fs-waiting",
			"flow_turn_id": "ft-waiting",
			"status":       "waiting_approval",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	thinking := <-session.events
	if thinking.Type != core.EventThinking || thinking.Content != "等待审批：Bash" {
		t.Fatalf("waiting notice = %+v", thinking)
	}
	suspended := <-session.events
	if suspended.Type != core.EventSuspended || suspended.SessionID != "fs-waiting" {
		t.Fatalf("suspended event = %+v", suspended)
	}
	if suspended.Metadata["status"] != "waiting_approval" {
		t.Fatalf("suspended metadata = %#v", suspended.Metadata)
	}
	approvals, ok := suspended.Metadata["pending_approvals"].([]heronPendingApproval)
	if !ok || len(approvals) != 1 || approvals[0].RequestID != "ap-1" {
		t.Fatalf("pending approvals = %#v", suspended.Metadata["pending_approvals"])
	}
	if len(session.events) != 0 {
		t.Fatalf("waiting response emitted unexpected events: %d", len(session.events))
	}
}

func TestHeronSessionWaitingToolResponseEmitsSuspended(t *testing.T) {
	session := newTestHeronSession()
	defer session.cancel()

	if err := session.handleResponse(heronJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Result: mustJSON(map[string]any{
			"session_id":   "fs-tool-waiting",
			"flow_turn_id": "ft-tool-waiting",
			"status":       "waiting_tool",
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if got := <-session.events; got.Type != core.EventThinking || got.Content != "等待工具任务完成" {
		t.Fatalf("waiting tool notice = %+v", got)
	}
	if got := <-session.events; got.Type != core.EventSuspended || got.Metadata["status"] != "waiting_tool" {
		t.Fatalf("suspended event = %+v", got)
	}
	if len(session.events) != 0 {
		t.Fatalf("waiting_tool response emitted unexpected events: %d", len(session.events))
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
