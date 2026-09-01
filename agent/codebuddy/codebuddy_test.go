package codebuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/janostudio/heron-connect/core"
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

func TestAgent_AvailableModels_UsesModelsJSONWhenPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	modelsPath := filepath.Join(workDir, ".codebuddy", "models.json")
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"models": [{"id": "my-custom-model", "name": "My Custom Model"}]}`
	if err := os.WriteFile(modelsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{workDir: workDir}
	models := a.AvailableModels(context.Background())
	if len(models) != 1 || models[0].Name != "my-custom-model" {
		t.Errorf("expected only the configured custom model, got %v", models)
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

func TestAgent_CommandDirs(t *testing.T) {
	a := &Agent{workDir: "/tmp/test"}
	dirs := a.CommandDirs()
	if len(dirs) != 2 {
		t.Errorf("CommandDirs() = %d items, want 2", len(dirs))
	}
	if dirs[0] != filepath.Join("/tmp/test", ".codebuddy", "commands") {
		t.Errorf("CommandDirs()[0] = %q, want project-level .codebuddy/commands", dirs[0])
	}
}

// verify Agent implements core.CommandProvider
var _ core.CommandProvider = (*Agent)(nil)

// verify Agent implements core.Agent
var _ core.Agent = (*Agent)(nil)

// ── launchArgs tests ────────────────────────────────────────

func TestLaunchArgs_PlainPrompt(t *testing.T) {
	args := launchArgs("hello world", "", "default", "")
	want := []string{"-p", "--output-format", "stream-json", "--", "hello world"}
	if len(args) != len(want) {
		t.Fatalf("launchArgs = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("launchArgs[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestLaunchArgs_DashPrefixedPromptAfterEndOfOptions(t *testing.T) {
	// Custom command files carry YAML frontmatter and therefore start with
	// "---". The CLI parser rejects such tokens as unknown options unless
	// they appear after the "--" end-of-options marker.
	prompt := "---\ndescription: \"audit\"\n---\n\n# audit body"
	args := launchArgs(prompt, "sid-123", "yolo", "glm-5.3-ioa")

	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep == -1 {
		t.Fatalf("launchArgs missing \"--\" end-of-options marker: %v", args)
	}
	if sep != len(args)-2 {
		t.Errorf("\"--\" at index %d, want %d (directly before prompt)", sep, len(args)-2)
	}
	if args[len(args)-1] != prompt {
		t.Errorf("last arg = %q, want the prompt itself", args[len(args)-1])
	}
	// The prompt itself must never appear as a token before the marker.
	for i := 0; i < sep; i++ {
		if args[i] == prompt {
			t.Errorf("prompt leaked before \"--\" at index %d: %v", i, args)
		}
	}
}

func TestLaunchArgs_OptionalFlagsBeforeEndOfOptions(t *testing.T) {
	args := launchArgs("hi", "sid-1", "yolo", "m1")
	want := []string{
		"-p", "--output-format", "stream-json",
		"--resume", "sid-1",
		"--dangerously-skip-permissions",
		"--model", "m1",
		"--", "hi",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("launchArgs = %v, want %v", args, want)
	}
}

func TestLaunchArgs_DefaultModeOmitsYoloFlag(t *testing.T) {
	args := launchArgs("hi", "", "default", "")
	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			t.Errorf("launchArgs should omit yolo flag in default mode, got %v", args)
		}
	}
}

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

// TestHandleResult_EmptyContent verifies an empty result (model/API returned
// nothing) does NOT emit an EventResult — it returns false so readLoop falls
// through to exitFallbackEvent and surfaces the reason as an EventError. This
// is the regression guard for the "(空响应)" symptom.
func TestHandleResult_EmptyContent(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	ev := &streamEvent{
		Type:      "result",
		Subtype:   "success",
		SessionID: "test-sid-empty",
		Result:    "",
		IsError:   false,
	}
	if got := cs.handleResult(ev, ""); got {
		t.Fatal("handleResult() = true for empty result, want false (so readLoop falls through to exitFallback)")
	}

	// No EventResult must be emitted.
	select {
	case evt := <-cs.events:
		t.Fatalf("unexpected event emitted for empty result: type=%s", evt.Type)
	default:
		// expected: nothing emitted
	}
}

// TestHandleResult_EmptyResultButPendingText verifies pendingText still counts
// as output — an empty result with buffered assistant text is a valid turn.
func TestHandleResult_EmptyResultButPendingText(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	ev := &streamEvent{Type: "result", Subtype: "success", Result: ""}
	if got := cs.handleResult(ev, "buffered assistant text"); !got {
		t.Fatal("handleResult() = false with pendingText, want true")
	}

	select {
	case got := <-cs.events:
		if got.Type != core.EventResult || got.Content != "buffered assistant text" {
			t.Errorf("got type=%s content=%q, want EventResult/buffered assistant text", got.Type, got.Content)
		}
	default:
		t.Error("expected a result event but channel was empty")
	}
}

// ── exitFallbackEvent tests ─────────────────────────────────

// The silent clean-exit case (exit 0, zero stdout) previously produced an
// EMPTY EventResult — the user saw "(空响应)" and the turn looked successful
// while the CLI had actually failed. It must now surface as an explicit
// EventError, carrying stderr when the CLI wrote anything there.
func TestExitFallbackEvent_SilentCleanExitNoStderr(t *testing.T) {
	evt := exitFallbackEvent(nil, nil, nil, "", "s1")
	if evt.Type != core.EventError {
		t.Fatalf("type = %v, want EventError for silent zero-output exit", evt.Type)
	}
	if evt.Error == nil || evt.Error.Error() == "" {
		t.Fatalf("error = %v, want non-empty diagnostic", evt.Error)
	}
}

func TestExitFallbackEvent_SilentCleanExitWithStderr(t *testing.T) {
	evt := exitFallbackEvent(nil, nil, nil, "auth token expired, please login", "s1")
	if evt.Type != core.EventError {
		t.Fatalf("type = %v, want EventError", evt.Type)
	}
	if evt.Error == nil || evt.Error.Error() != "auth token expired, please login" {
		t.Fatalf("error = %v, want stderr content relayed", evt.Error)
	}
}

func TestExitFallbackEvent_PlainTextFallbackWins(t *testing.T) {
	evt := exitFallbackEvent([]string{"plain", "text"}, nil, nil, "ignored", "s1")
	if evt.Type != core.EventResult {
		t.Fatalf("type = %v, want EventResult for plain-text output", evt.Type)
	}
	if evt.Content != "plain\ntext" {
		t.Fatalf("content = %q, want joined plain lines", evt.Content)
	}
	if !evt.Done {
		t.Fatal("Done = false, want true")
	}
}

func TestExitFallbackEvent_ProcessErrorPrefersStderr(t *testing.T) {
	evt := exitFallbackEvent(nil, fmt.Errorf("exit status 1"), nil, "boom", "s1")
	if evt.Type != core.EventError {
		t.Fatalf("type = %v, want EventError", evt.Type)
	}
	if evt.Error == nil || evt.Error.Error() != "boom" {
		t.Fatalf("error = %v, want stderr preferred over exitErr", evt.Error)
	}
}

func TestExitFallbackEvent_ScanError(t *testing.T) {
	evt := exitFallbackEvent(nil, nil, fmt.Errorf("read: broken pipe"), "", "s1")
	if evt.Type != core.EventError {
		t.Fatalf("type = %v, want EventError", evt.Type)
	}
	if evt.Error == nil || evt.Error.Error() != "read stdout: read: broken pipe" {
		t.Fatalf("error = %v, want wrapped scan error", evt.Error)
	}
}

// ── shouldTrackInitSessionID tests ──────────────────────────

// Regression guard for the "subagent id replaces parent id" bug: subagent
// child sessions emit their own system/init events mid-conversation; only the
// FIRST init of a process run may establish the tracked top-level session id.
func TestShouldTrackInitSessionID(t *testing.T) {
	cases := []struct {
		name                 string
		sawInit              bool
		subtype, sessionID   string
		want                 bool
	}{
		{"first init accepted", false, "init", "dc918b77", true},
		{"second init rejected (subagent)", true, "init", "d492df45", false},
		{"empty session id rejected", false, "init", "", false},
		{"non-init subtype rejected", false, "other", "dc918b77", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldTrackInitSessionID(tc.sawInit, tc.subtype, tc.sessionID); got != tc.want {
				t.Fatalf("shouldTrackInitSessionID(%v, %q, %q) = %v, want %v",
					tc.sawInit, tc.subtype, tc.sessionID, got, tc.want)
			}
		})
	}
}

// Silent-exit fallbacks must carry the session_unrecoverable marker so the
// engine detaches the poisoned agent_session_id binding (self-heal).
func TestExitFallbackEvent_SilentExitMarksUnrecoverable(t *testing.T) {
	for name, stderr := range map[string]string{"": "", "with stderr": "No conversation found with session ID"} {
		evt := exitFallbackEvent(nil, nil, nil, stderr, "s1")
		if evt.Type != core.EventError {
			t.Fatalf("%s: type = %v, want EventError", name, evt.Type)
		}
		if v, _ := evt.Metadata[core.EventMetadataSessionUnrecoverable].(bool); !v {
			t.Fatalf("%s: metadata marker %v missing, want session_unrecoverable=true", name, evt.Metadata)
		}
	}
}

// Non-silent fallbacks (plain text / process error / scan error) must NOT
// carry the unrecoverable marker — those failures may be transient and the
// persisted binding stays for retry.
func TestExitFallbackEvent_OtherBranchesNotMarked(t *testing.T) {
	notMarked := []core.Event{
		exitFallbackEvent([]string{"plain"}, nil, nil, "", "s1"),
		exitFallbackEvent(nil, fmt.Errorf("exit status 1"), nil, "", "s1"),
		exitFallbackEvent(nil, nil, fmt.Errorf("read: broken pipe"), "", "s1"),
	}
	for i, evt := range notMarked {
		if v, _ := evt.Metadata[core.EventMetadataSessionUnrecoverable].(bool); v {
			t.Fatalf("branch %d: unexpected unrecoverable marker on non-silent fallback", i)
		}
	}
}

// ── toolUseID ↔ name resolution & thinking parsing ───────────
//
// Regression: previously handleUser's tool_result branch set ToolName to
// the CLI-emitted tool_use_id (e.g. "tooluse_3MNyh1...") instead of the
// readable tool name, and handleAssistant silently dropped "thinking"
// content blocks. The fix adds a sync.Map cache populated in handleAssistant
// and consulted in handleUser, plus a new "thinking" switch case that
// accepts either "thinking" or "text" payloads.

func TestHandleUser_ToolResult_ResolvesToolNameFromCache(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	// Assistant first emits a tool_use with the readable name → cache the id.
	toolInput, _ := json.Marshal(map[string]string{"command": "ls"})
	asstContent, _ := json.Marshal([]contentItem{
		{Type: "tool_use", ID: "call_abc", Name: "Bash", Input: toolInput},
	})
	cs.handleAssistant(&streamEvent{
		Type:      "assistant",
		SessionID: "s",
		Message:   &streamMessage{StopReason: "tool_use", Content: asstContent},
	}, "")
	// Drain the tool_use event so the channel is empty for the next assertion.
	select {
	case <-cs.events:
	default:
	}

	// Then the CLI's user turn emits a tool_result that only carries
	// tool_use_id. handleUser must resolve the readable name from cache.
	resultText, _ := json.Marshal([]contentItem{{Type: "text", Text: "file.txt"}})
	userContent, _ := json.Marshal([]contentItem{
		{Type: "tool_result", ToolUseID: "call_abc", Content: resultText},
	})
	cs.handleUser(&streamEvent{
		Type:      "user",
		SessionID: "s",
		Message:   &streamMessage{Content: userContent},
	})

	select {
	case got := <-cs.events:
		if got.Type != core.EventToolResult {
			t.Fatalf("got type=%s, want EventToolResult", got.Type)
		}
		if got.ToolName != "Bash" {
			t.Fatalf("got ToolName=%q, want %q (resolved from cache, not raw id)", got.ToolName, "Bash")
		}
	default:
		t.Fatal("expected a tool_result event but channel was empty")
	}
}

func TestHandleUser_ToolResult_ColdCacheFallsBackToID(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	// No prior tool_use emitted → cache is cold. The event must still be
	// emitted (with the raw id) so the engine can render something rather
	// than silently dropping the result.
	resultText, _ := json.Marshal([]contentItem{{Type: "text", Text: "x"}})
	userContent, _ := json.Marshal([]contentItem{
		{Type: "tool_result", ToolUseID: "call_orphan", Content: resultText},
	})
	cs.handleUser(&streamEvent{
		Type:      "user",
		SessionID: "s",
		Message:   &streamMessage{Content: userContent},
	})

	select {
	case got := <-cs.events:
		if got.Type != core.EventToolResult {
			t.Fatalf("got type=%s, want EventToolResult", got.Type)
		}
		if got.ToolName != "call_orphan" {
			t.Fatalf("got ToolName=%q, want %q (raw id fallback when cache cold)", got.ToolName, "call_orphan")
		}
	default:
		t.Fatal("expected a tool_result event but channel was empty")
	}
}

func TestHandleAssistant_Thinking(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	cases := []struct {
		name    string
		item    contentItem
		wantOut bool
		wantTxt string
	}{
		{
			name:    "thinking field populated",
			item:    contentItem{Type: "thinking", Thinking: "reasoning via thinking field"},
			wantOut: true,
			wantTxt: "reasoning via thinking field",
		},
		{
			name:    "text field fallback when thinking is empty",
			item:    contentItem{Type: "thinking", Text: "reasoning via text field"},
			wantOut: true,
			wantTxt: "reasoning via text field",
		},
		{
			name:    "both empty → no event",
			item:    contentItem{Type: "thinking"},
			wantOut: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, _ := json.Marshal([]contentItem{tc.item})
			cs.handleAssistant(&streamEvent{
				Type:      "assistant",
				SessionID: "s",
				Message:   &streamMessage{StopReason: "end_turn", Content: content},
			}, "")

			if !tc.wantOut {
				select {
				case got := <-cs.events:
					t.Fatalf("expected no event for empty thinking, got %+v", got)
				default:
					return
				}
			}
			select {
			case got := <-cs.events:
				if got.Type != core.EventThinking {
					t.Fatalf("got type=%s, want EventThinking", got.Type)
				}
				if got.Content != tc.wantTxt {
					t.Fatalf("got content=%q, want %q", got.Content, tc.wantTxt)
				}
			default:
				t.Fatal("expected a thinking event but channel was empty")
			}
		})
	}
}
