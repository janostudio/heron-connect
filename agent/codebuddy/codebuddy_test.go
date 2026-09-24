package codebuddy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

func TestParseEnv(t *testing.T) {
	got := parseEnv(map[string]any{
		"HERON_CONNECT_ENV": "cloud",
		"COUNT":             3,
		" bad=key ":         "ignored",
	})
	want := []string{"COUNT=3", "HERON_CONNECT_ENV=cloud"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseEnv() = %#v, want %#v", got, want)
	}
}

func TestAgentSetSessionEnvPreservesConfiguredEnvironment(t *testing.T) {
	a := &Agent{configEnv: []string{"HERON_CONNECT_ENV=cloud"}}
	a.SetSessionEnv([]string{"HERON_PROJECT=auto-bugfix", "HERON_SESSION_KEY=wecom:chat:user"})
	want := []string{
		"HERON_CONNECT_ENV=cloud",
		"HERON_PROJECT=auto-bugfix",
		"HERON_SESSION_KEY=wecom:chat:user",
	}
	if !reflect.DeepEqual(a.sessionEnv, want) {
		t.Fatalf("sessionEnv = %#v, want %#v", a.sessionEnv, want)
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
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+modelUpdateLine("gpt-5.6-terra", [2]string{"gpt-5.6-terra", "GPT-5.6-Terra"})+`'
sleep 0.3
`))
	a := &Agent{workDir: t.TempDir()}
	models := a.AvailableModels(context.Background())
	if len(models) == 0 {
		t.Error("AvailableModels() returned empty list")
	}
	if models[0].Name != "gpt-5.6-terra" {
		t.Errorf("models[0].Name = %q, want gpt-5.6-terra", models[0].Name)
	}
}

// The probe result is the base list; a models.json entry overrides the
// matching id and adds new ones.
func TestAgent_AvailableModels_ModelsJSONOverridesProbe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+modelUpdateLine("claude-sonnet-5",
		[2]string{"claude-sonnet-5", "Claude-Sonnet-5"},
		[2]string{"gpt-5.6-terra", "GPT-5.6-Terra"})+`'
sleep 0.3
`))
	workDir := t.TempDir()
	writeModelsJSON(t, workDir, `{"models":[{"id":"claude-sonnet-5","name":"My Sonnet"},{"id":"self-hosted","name":"Self Hosted"}]}`)

	a := &Agent{workDir: workDir}
	models := a.AvailableModels(context.Background())

	byName := map[string]string{}
	var order []string
	for _, m := range models {
		byName[m.Name] = m.Desc
		order = append(order, m.Name)
	}
	if got := byName["claude-sonnet-5"]; got != "My Sonnet" {
		t.Errorf("override lost: claude-sonnet-5 desc = %q, want %q", got, "My Sonnet")
	}
	if got := byName["gpt-5.6-terra"]; got != "GPT-5.6-Terra" {
		t.Errorf("probe entry lost: gpt-5.6-terra desc = %q", got)
	}
	if got := byName["self-hosted"]; got != "Self Hosted" {
		t.Errorf("custom entry missing: %v", models)
	}
	// An override must replace in place, not duplicate or reorder.
	if len(order) != 3 {
		t.Errorf("order = %v, want 3 entries with the override in place", order)
	}
	if order[0] != "claude-sonnet-5" {
		t.Errorf("override moved: order = %v", order)
	}
}

// A non-empty availableModels allow-list filters the discovered list.
func TestAgent_AvailableModels_AllowListFiltersProbe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+modelUpdateLine("b",
		[2]string{"a", "A"}, [2]string{"b", "B"}, [2]string{"c", "C"})+`'
sleep 0.3
`))
	workDir := t.TempDir()
	writeModelsJSON(t, workDir, `{"availableModels":["b","c"]}`)

	a := &Agent{workDir: workDir}
	models := a.AvailableModels(context.Background())
	if len(models) != 2 || models[0].Name != "b" || models[1].Name != "c" {
		t.Errorf("got %+v, want only b and c", models)
	}
}

// When the probe fails but models.json defines something, models.json wins
// over the built-in fallback.
func TestAgent_AvailableModels_FallsBackToModelsJSONWhenProbeFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no codebuddy binary

	workDir := t.TempDir()
	writeModelsJSON(t, workDir, `{"models":[{"id":"my-custom-model","name":"My Custom Model"}]}`)

	a := &Agent{workDir: workDir}
	models := a.AvailableModels(context.Background())
	if len(models) != 1 || models[0].Name != "my-custom-model" {
		t.Errorf("expected only the configured custom model, got %v", models)
	}
}

// With neither probe nor models.json available, the picker still gets a list.
func TestAgent_AvailableModels_FallsBackToBuiltin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no codebuddy binary

	a := &Agent{workDir: t.TempDir()}
	models := a.AvailableModels(context.Background())
	if len(models) == 0 {
		t.Fatal("AvailableModels() returned empty list")
	}
}

// A pending user selection is reported as current and guaranteed to be in
// the list, even when the CLI does not advertise it.
func TestAgent_AvailableModels_PendingModelIsReportedAndPresent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+modelUpdateLine("a", [2]string{"a", "A"}, [2]string{"b", "B"})+`'
sleep 0.3
`))
	a := &Agent{workDir: t.TempDir()}
	a.SetModel("not-advertised")

	models := a.AvailableModels(context.Background())
	if got := a.GetModel(); got != "not-advertised" {
		t.Errorf("GetModel() = %q, want the pending selection", got)
	}
	if len(models) == 0 || models[0].Name != "not-advertised" {
		t.Errorf("pending model not prepended: %+v", models)
	}
}

// Without a pending selection, GetModel reports the CLI's active model.
func TestAgent_GetModel_UsesDiscoveredModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+modelUpdateLine("glm-5.3-ioa", [2]string{"glm-5.3-ioa", "GLM-5.3"})+`'
sleep 0.3
`))
	a := &Agent{workDir: t.TempDir()}
	a.AvailableModels(context.Background())

	if got := a.GetModel(); got != "glm-5.3-ioa" {
		t.Errorf("GetModel() = %q, want glm-5.3-ioa", got)
	}
}

// GetModel must stay O(1): it is called while rendering the footer and
// status card, so it must never spawn a probe.
func TestAgent_GetModel_DoesNotProbe(t *testing.T) {
	// A PATH with no codebuddy binary: if GetModel probed, it would log a
	// spawn failure and take measurable time.
	t.Setenv("PATH", t.TempDir())
	a := &Agent{workDir: t.TempDir()}

	done := make(chan string, 1)
	go func() { done <- a.GetModel() }()
	select {
	case got := <-done:
		if got != "" {
			t.Errorf("GetModel() = %q, want empty", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetModel blocked — it must not run the probe")
	}
}

func writeModelsJSON(t *testing.T, workDir, content string) {
	t.Helper()
	path := filepath.Join(workDir, ".codebuddy", "models.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureModelPresent(t *testing.T) {
	base := []core.ModelOption{{Name: "a"}, {Name: "b"}}
	tests := []struct {
		name    string
		current string
		want    []string
	}{
		{"present leaves list untouched", "b", []string{"a", "b"}},
		{"absent is prepended", "z", []string{"z", "a", "b"}},
		{"empty current leaves list untouched", "", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureModelPresent(base, tt.current)
			names := make([]string, len(got))
			for i, m := range got {
				names[i] = m.Name
			}
			if !reflect.DeepEqual(names, tt.want) {
				t.Errorf("ensureModelPresent(%q) = %v, want %v", tt.current, names, tt.want)
			}
		})
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
	args := launchArgs("hello world", "", "default", "", nil)
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
	args := launchArgs(prompt, "sid-123", "yolo", "glm-5.3-ioa", nil)

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
	args := launchArgs("hi", "sid-1", "yolo", "m1", nil)
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
	args := launchArgs("hi", "", "default", "", nil)
	for _, a := range args {
		if a == "--dangerously-skip-permissions" {
			t.Errorf("launchArgs should omit yolo flag in default mode, got %v", args)
		}
	}
}

func TestLaunchArgs_ExtraArgsBeforeEndOfOptions(t *testing.T) {
	args := launchArgs("hi", "sid-1", "default", "m1", []string{"--system-prompt-file", "/tmp/sp.md"})
	want := []string{
		"-p", "--output-format", "stream-json",
		"--resume", "sid-1",
		"--model", "m1",
		"--system-prompt-file", "/tmp/sp.md",
		"--", "hi",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("launchArgs = %v, want %v", args, want)
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
		name               string
		sawInit            bool
		subtype, sessionID string
		want               bool
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

// ──────────────────────────────────────────────────────────────
// Interruptible / resident-mode tests
// ──────────────────────────────────────────────────────────────

// TestLaunchArgs_UnchangedInPerTurnMode is the regression guard: the default
// (non-interruptible) argument list must stay byte-identical, positional prompt
// and end-of-options marker included.
func TestLaunchArgs_UnchangedInPerTurnMode(t *testing.T) {
	args := launchArgs("hello", "sid-1", "default", "m1", nil)
	want := []string{"-p", "--output-format", "stream-json", "--resume", "sid-1", "--model", "m1", "--", "hello"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("launchArgs = %v, want %v", args, want)
	}
}

// TestResidentArgs_AddsInputFormatAndDropsPrompt verifies the resident argument
// list opens the stdin channel and carries NO positional prompt (a positional
// prompt would conflict with the stream-json input).
func TestResidentArgs_AddsInputFormatAndDropsPrompt(t *testing.T) {
	args := residentArgs("sid-1", "default", "m1", []string{"--foo"})

	want := []string{
		"--input-format", "stream-json",
		"-p", "--output-format", "stream-json",
		"--resume", "sid-1",
		"--model", "m1",
		"--foo",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("residentArgs = %v, want %v", args, want)
	}
	for _, a := range args {
		if a == "--" {
			t.Error("resident args must not contain the end-of-options marker")
		}
	}
}

// TestNewCodeBuddySession_PerTurnDoesNotSpawn verifies the default mode stays
// lazy: no process is started until Send, preserving the historical lifecycle.
func TestNewCodeBuddySession_PerTurnDoesNotSpawn(t *testing.T) {
	cs, err := newCodeBuddySession(context.Background(), ".", "", "default", "", nil, nil, false)
	if err != nil {
		t.Fatalf("newCodeBuddySession: %v", err)
	}
	defer cs.Close()

	if cs.Interruptible() {
		t.Error("session created without interruptible must report false")
	}
	cs.cmdMu.Lock()
	cmd := cs.osCmd
	cs.cmdMu.Unlock()
	if cmd != nil {
		t.Error("per-turn mode must not start a process at construction time")
	}
}

// TestCancelTurn_NoopInPerTurnMode verifies CancelTurn does nothing when the
// adapter is not in resident mode (there is no stdin to send an interrupt on),
// so the engine's capability gate is the only thing that matters.
func TestCancelTurn_NoopInPerTurnMode(t *testing.T) {
	cs, err := newCodeBuddySession(context.Background(), ".", "", "default", "", nil, nil, false)
	if err != nil {
		t.Fatalf("newCodeBuddySession: %v", err)
	}
	defer cs.Close()

	cs.CancelTurn() // must not panic or write anywhere
}

// TestWriteJSON_RequiresResidentStdin verifies writeJSON fails loudly instead of
// silently discarding a prompt when no resident stdin exists.
func TestWriteJSON_RequiresResidentStdin(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	if err := cs.writeJSON(map[string]any{"type": "user"}); err == nil {
		t.Error("writeJSON without resident stdin must return an error")
	}
}

// TestCancelTurn_WritesInterruptFrame verifies the interrupt frame shape in
// resident mode.
func TestCancelTurn_WritesInterruptFrame(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true

	stdin := &captureStdin{}
	cs.stdin = stdin

	cs.CancelTurn()

	frames := stdin.frames(t)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d: %v", len(frames), frames)
	}
	f := frames[0]
	if got := f["type"]; got != "control_request" {
		t.Errorf("type = %v, want control_request", got)
	}
	req, ok := f["request"].(map[string]any)
	if !ok {
		t.Fatalf("request is not an object: %v", f["request"])
	}
	if got := req["subtype"]; got != "interrupt" {
		t.Errorf("request.subtype = %v, want interrupt", got)
	}
	if id, _ := f["request_id"].(string); id == "" {
		t.Error("request_id must be non-empty")
	}
}

// TestCancelTurn_DeadResidentSession_Noop verifies a dead resident session
// writes nothing, so the engine falls back to queueing.
func TestCancelTurn_DeadResidentSession_Noop(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true
	stdin := &captureStdin{}
	cs.stdin = stdin
	cs.alive.Store(false)

	cs.CancelTurn()

	if frames := stdin.frames(t); len(frames) != 0 {
		t.Errorf("dead session wrote %d frames, want 0", len(frames))
	}
}

// TestEmit_StampsTurnEpoch verifies emitted events carry the engine's epoch.
func TestEmit_StampsTurnEpoch(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	cs.SetTurnEpoch(7)
	cs.emit(core.Event{Type: core.EventText, Content: "hi"})

	select {
	case ev := <-cs.events:
		if ev.TurnEpoch != 7 {
			t.Errorf("TurnEpoch = %d, want 7", ev.TurnEpoch)
		}
	case <-time.After(time.Second):
		t.Fatal("emit did not deliver the event")
	}
}

// TestEmit_UnstampedByDefault is the compatibility guard for the per-turn path.
func TestEmit_UnstampedByDefault(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	cs.emit(core.Event{Type: core.EventText, Content: "hi"})

	select {
	case ev := <-cs.events:
		if ev.TurnEpoch != 0 {
			t.Errorf("TurnEpoch = %d, want 0 by default", ev.TurnEpoch)
		}
	case <-time.After(time.Second):
		t.Fatal("emit did not deliver the event")
	}
}

// ── control_request (can_use_tool) tests ────────────────────
//
// The CLI emits control_request and blocks on stdin until a control_response
// arrives. Dropping the frame leaves the CLI waiting forever, so the turn
// never produces a result — the ExitPlanMode hang. These tests pin both halves
// of the fix: the frame becomes an EventPermissionRequest, and the reply
// reaches stdin.

// TestHandleControlRequest_EmitsPermissionRequest verifies a can_use_tool
// frame is surfaced to the engine carrying everything the reply needs.
func TestHandleControlRequest_EmitsPermissionRequest(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true

	cs.handleControlRequest(&streamEvent{
		Type:      "control_request",
		RequestID: "perm_1789891817931_2",
		Request: &controlRequestBody{
			Subtype:   "can_use_tool",
			ToolName:  "ExitPlanMode",
			ToolUseID: "call_abc",
			Input:     map[string]any{"plan": "do the thing"},
		},
	})

	select {
	case ev := <-cs.events:
		if ev.Type != core.EventPermissionRequest {
			t.Fatalf("event type = %v, want EventPermissionRequest", ev.Type)
		}
		if ev.RequestID != "perm_1789891817931_2" {
			t.Errorf("request_id = %q, want perm_1789891817931_2", ev.RequestID)
		}
		if ev.ToolName != "ExitPlanMode" {
			t.Errorf("tool name = %q, want ExitPlanMode", ev.ToolName)
		}
		// ToolInputRaw must survive so an "allow" reply can echo updatedInput.
		if got, _ := ev.ToolInputRaw["plan"].(string); got != "do the thing" {
			t.Errorf("ToolInputRaw[plan] = %q, want %q", got, "do the thing")
		}
	case <-time.After(time.Second):
		t.Fatal("control_request did not produce a permission event")
	}
}

// TestHandleControlRequest_NonResidentEmitsError verifies that without a stdin
// channel the adapter fails loudly instead of leaving the CLI blocked in
// silence — the failure mode that made this bug so hard to see.
func TestHandleControlRequest_NonResidentEmitsError(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = false

	cs.handleControlRequest(&streamEvent{
		Type:      "control_request",
		RequestID: "perm_1",
		Request: &controlRequestBody{Subtype: "can_use_tool", ToolName: "Bash"},
	})

	select {
	case ev := <-cs.events:
		if ev.Type != core.EventError {
			t.Fatalf("event type = %v, want EventError", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("non-resident control_request must emit an error, not vanish")
	}
}

// TestHandleControlRequest_UnknownSubtypeReplies verifies an unrecognised
// subtype is answered (with a denial) rather than ignored: any control_request
// the CLI is blocked on must get a response or the turn hangs.
func TestHandleControlRequest_UnknownSubtypeReplies(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true
	stdin := &captureStdin{}
	cs.stdin = stdin

	cs.handleControlRequest(&streamEvent{
		Type:      "control_request",
		RequestID: "perm_9",
		Request:   &controlRequestBody{Subtype: "something_else"},
	})

	frames := stdin.frames(t)
	if len(frames) != 1 {
		t.Fatalf("expected 1 reply frame, got %d: %v", len(frames), frames)
	}
	resp, _ := frames[0]["response"].(map[string]any)
	inner, _ := resp["response"].(map[string]any)
	if got := inner["behavior"]; got != "deny" {
		t.Errorf("behavior = %v, want deny", got)
	}
}

// TestRespondPermission_AllowFrame verifies the allow reply carries the
// original tool input as updatedInput (the CLI echoes it back as the tool's
// effective input, so dropping it would corrupt the call).
func TestRespondPermission_AllowFrame(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true
	stdin := &captureStdin{}
	cs.stdin = stdin

	err := cs.RespondPermission("perm_1", core.PermissionResult{
		Behavior:     "allow",
		UpdatedInput: map[string]any{"command": "ls"},
	})
	if err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}

	frames := stdin.frames(t)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d: %v", len(frames), frames)
	}
	f := frames[0]
	if got := f["type"]; got != "control_response" {
		t.Errorf("type = %v, want control_response", got)
	}
	resp, ok := f["response"].(map[string]any)
	if !ok {
		t.Fatalf("response is not an object: %v", f["response"])
	}
	if got := resp["subtype"]; got != "success" {
		t.Errorf("response.subtype = %v, want success", got)
	}
	// request_id must be echoed verbatim or the CLI cannot match the reply.
	if got := resp["request_id"]; got != "perm_1" {
		t.Errorf("response.request_id = %v, want perm_1", got)
	}
	inner, _ := resp["response"].(map[string]any)
	if got := inner["behavior"]; got != "allow" {
		t.Errorf("behavior = %v, want allow", got)
	}
	updated, _ := inner["updatedInput"].(map[string]any)
	if got, _ := updated["command"].(string); got != "ls" {
		t.Errorf("updatedInput.command = %q, want ls", got)
	}
}

// TestRespondPermission_DenyFrame verifies a denial always carries a message,
// since the CLI feeds it back to the model as the tool's result.
func TestRespondPermission_DenyFrame(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true
	stdin := &captureStdin{}
	cs.stdin = stdin

	if err := cs.RespondPermission("perm_2", core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}

	frames := stdin.frames(t)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d: %v", len(frames), frames)
	}
	resp, _ := frames[0]["response"].(map[string]any)
	inner, _ := resp["response"].(map[string]any)
	if got := inner["behavior"]; got != "deny" {
		t.Errorf("behavior = %v, want deny", got)
	}
	if msg, _ := inner["message"].(string); msg == "" {
		t.Error("deny reply must carry a non-empty message")
	}
}

// TestRespondPermission_NonResidentFails verifies the reply cannot be silently
// dropped when there is no stdin to write it to.
func TestRespondPermission_NonResidentFails(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = false

	if err := cs.RespondPermission("perm_1", core.PermissionResult{Behavior: "allow"}); err == nil {
		t.Error("RespondPermission must fail without a resident stdin")
	}
}

// TestNewCodeBuddySession_ForcesResidentMode pins the invariant the whole
// control_request fix rests on: CodeBuddy sessions are always resident, so a
// permission reply always has a stdin channel to travel over.
func TestAgent_AlwaysInterruptible(t *testing.T) {
	a, err := New(map[string]any{"work_dir": t.TempDir()})
	if err != nil {
		t.Skipf("codebuddy CLI not installed: %v", err)
	}
	agent, ok := a.(*Agent)
	if !ok {
		t.Fatalf("New returned %T, want *Agent", a)
	}
	if !agent.interruptible {
		t.Error("codebuddy must always run resident (interruptible) to answer control_request frames")
	}
	// The management API reads the capability through the Agent-level reporter
	// (core.AgentInterruptibleReporter), not the raw field. codebuddy never
	// reads an "interruptible" config option, so an option-snapshot probe would
	// wrongly report false — this pins the reporter to the field.
	if !agent.Interruptible() {
		t.Error("Interruptible() = false, want true (must expose the hard-coded capability)")
	}
}

// captureStdin records writes so tests can assert on emitted frames.
type captureStdin struct {
	mu   sync.Mutex
	data []byte
}

func (c *captureStdin) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = append(c.data, p...)
	return len(p), nil
}

func (c *captureStdin) Close() error { return nil }

func (c *captureStdin) frames(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(c.data)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdin line is not valid JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// ── background-task event family ────────────────────────────
//
// These cover the system/subtype=task_* family, which the CLI emits for work
// that outlives the turn that started it (Bash run_in_background, a
// background subagent, a workflow run). The event shapes below are taken
// verbatim from a real `codebuddy -p --input-format stream-json
// --output-format stream-json` session that launched `sleep 8 && echo DONE`
// with run_in_background: true.

// drainEvent reads one event from the session's channel, failing the test if
// none arrives. Used to assert on (or assert the absence of) emitted events.
func drainEvent(t *testing.T, cs *codebuddySession) core.Event {
	t.Helper()
	select {
	case ev := <-cs.events:
		return ev
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("no event emitted")
		return core.Event{}
	}
}

// assertNoEvent fails if the session emitted anything within the grace window.
func assertNoEvent(t *testing.T, cs *codebuddySession) {
	t.Helper()
	select {
	case ev := <-cs.events:
		t.Fatalf("unexpected event emitted: type=%v content=%q", ev.Type, ev.Content)
	case <-time.After(100 * time.Millisecond):
	}
}

// taskStartedFrame builds the task_started frame the CLI emits just after the
// tool call that spawned the background task.
func taskStartedFrame(taskID, description string) *streamEvent {
	return &streamEvent{
		Type:        "system",
		Subtype:     "task_started",
		TaskID:      taskID,
		Description: description,
		SessionID:   "sid-1",
	}
}

// taskUpdatedFrame builds a task_updated frame carrying a status transition
// inside `patch` (the CLI does not put status at the top level here).
func taskUpdatedFrame(taskID, status string) *streamEvent {
	patch, _ := json.Marshal(map[string]any{"status": status})
	return &streamEvent{
		Type:      "system",
		Subtype:   "task_updated",
		TaskID:    taskID,
		Patch:     patch,
		SessionID: "sid-1",
	}
}

// taskNotificationFrame builds the terminal task_notification frame, which is
// the authoritative completion signal and carries status + summary at the top
// level.
func taskNotificationFrame(taskID, status, summary string) *streamEvent {
	return &streamEvent{
		Type:      "system",
		Subtype:   "task_notification",
		TaskID:    taskID,
		Status:    status,
		Summary:   summary,
		SessionID: "sid-1",
	}
}

func TestHandleTaskEvent_StartAndRunningAreNotRelayed(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	// Mid-turn: task_started and a running task_updated are progress only.
	if !cs.handleTaskEvent(taskStartedFrame("task-1", "sleep 8 && echo DONE")) {
		t.Fatal("task_started should be claimed by handleTaskEvent")
	}
	assertNoEvent(t, cs)

	cs.handleTaskEvent(taskUpdatedFrame("task-1", "running"))
	assertNoEvent(t, cs)
}

func TestHandleTaskEvent_NotificationRelayedAfterTurn(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	cs.handleTaskEvent(taskStartedFrame("task-1", "sleep 8 && echo DONE"))
	cs.handleTaskEvent(taskUpdatedFrame("task-1", "running"))

	// Between turns: the terminal frame becomes a relayable EventResult so
	// the engine's unsolicited reader can push it to the platform.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleTaskEvent(taskNotificationFrame("task-1", "completed",
		`Background command "sleep 8 && echo DONE" completed`))

	ev := drainEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}
	if !ev.Done {
		t.Error("event should be marked Done")
	}
	if !strings.Contains(ev.Content, "已完成") {
		t.Errorf("content should report completion, got %q", ev.Content)
	}
	if !strings.Contains(ev.Content, "sleep 8") {
		t.Errorf("content should carry the task summary, got %q", ev.Content)
	}
}

func TestHandleTaskEvent_TerminalUpdatedIsRelayedOnce(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	cs.handleTaskEvent(taskStartedFrame("task-1", "some command"))

	// The CLI documents that a terminal transition may arrive as
	// task_updated WITHOUT a following task_notification.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleTaskEvent(taskUpdatedFrame("task-1", "completed"))
	ev := drainEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}
	if !strings.Contains(ev.Content, "some command") {
		t.Errorf("should fall back to task_started description, got %q", ev.Content)
	}

	// The authoritative notification for the same task must not produce a
	// duplicate message.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleTaskEvent(taskNotificationFrame("task-1", "completed", "done"))
	assertNoEvent(t, cs)
}

func TestHandleTaskEvent_NotificationThenUpdatedIsRelayedOnce(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	cs.handleTaskEvent(taskStartedFrame("task-1", "some command"))
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleTaskEvent(taskNotificationFrame("task-1", "completed", "done"))

	ev := drainEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}

	// A late task_updated for the same terminal task is a duplicate.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleTaskEvent(taskUpdatedFrame("task-1", "completed"))
	assertNoEvent(t, cs)
}

func TestHandleTaskEvent_StatusesRenderDistinctly(t *testing.T) {
	cases := []struct {
		status  string
		wantSub string
	}{
		{"completed", "已完成"},
		{"failed", "失败"},
		{"stopped", "已停止"},
		{"killed", "已停止"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			cs := newTestSession()
			defer cs.cancel()

			cs.turnFinishedSeq.Store(cs.turnSeq.Load())
			cs.handleTaskEvent(taskNotificationFrame("task-1", tc.status, "output"))
			ev := drainEvent(t, cs)
			if !strings.Contains(ev.Content, tc.wantSub) {
				t.Errorf("status %q: content = %q, want substring %q", tc.status, ev.Content, tc.wantSub)
			}
		})
	}
}

func TestHandleTaskEvent_UnknownSubtypeNotClaimed(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	ev := &streamEvent{Type: "system", Subtype: "api_retry", SessionID: "sid-1"}
	if cs.handleTaskEvent(ev) {
		t.Error("non-task subtype must not be claimed by handleTaskEvent")
	}
}

func TestHandleTaskEvent_WithoutTaskIDIsDropped(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	ev := &streamEvent{Type: "system", Subtype: "task_notification", Status: "completed"}
	// Claimed (it is a task frame) but not relayed.
	if !cs.handleTaskEvent(ev) {
		t.Error("task frame should be claimed even without a task_id")
	}
	assertNoEvent(t, cs)
}

func TestFormatTaskCompletion_SanitizesUntrustedText(t *testing.T) {
	// Newlines in CLI-supplied text would forge extra lines in an outgoing
	// chat message.
	got := formatTaskCompletion("task-1", "", "completed",
		"line one\nline two\r\nline three", "")
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("embedded newlines should be flattened, got %q", got)
	}

	// Long values are truncated rather than forwarded whole.
	long := strings.Repeat("x", taskDescriptionMaxLen*2)
	got = formatTaskCompletion("task-1", long, "completed", "", "")
	if utf8.RuneCountInString(got) > taskDescriptionMaxLen+64 {
		t.Errorf("long description should be truncated, got %d runes",
			utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated text should be marked, got %q", got)
	}
}

func TestFormatTaskCompletion_FallsBackToTaskID(t *testing.T) {
	got := formatTaskCompletion("task-abc", "", "completed", "", "")
	if !strings.Contains(got, "task-abc") {
		t.Errorf("should fall back to the task id when no text is available, got %q", got)
	}
}

func TestFormatTaskCompletion_IncludesOutputFile(t *testing.T) {
	got := formatTaskCompletion("task-1", "", "completed", "done", "/tmp/out.log")
	if !strings.Contains(got, "/tmp/out.log") {
		t.Errorf("should surface the output file path, got %q", got)
	}
}

func TestIsTerminalTaskStatus(t *testing.T) {
	terminal := []string{"completed", "failed", "stopped", "killed", "cancelled"}
	for _, s := range terminal {
		if !isTerminalTaskStatus(s) {
			t.Errorf("isTerminalTaskStatus(%q) = false, want true", s)
		}
	}
	nonTerminal := []string{"", "pending", "running", "paused"}
	for _, s := range nonTerminal {
		if isTerminalTaskStatus(s) {
			t.Errorf("isTerminalTaskStatus(%q) = true, want false", s)
		}
	}
}

func TestHandleTaskEvent_RelayedEvenAfterUserStartsNewTurn(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	// Turn 1 starts and spawns a long-running background task.
	cs.turnSeq.Add(1)
	cs.handleTaskEvent(taskStartedFrame("task-1", "npm run build"))

	// Turn 1 finishes; the task is still running.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())

	// The user gets impatient and sends another message, starting turn 2
	// while the task is still in flight.
	cs.turnSeq.Add(1)

	// The task now completes. Because it was spawned in turn 1 and that turn
	// has finished, it must still be relayed — the user asked for this work
	// and has to hear back, even though a later turn is in progress.
	cs.handleTaskEvent(taskNotificationFrame("task-1", "completed", "build finished"))

	ev := drainEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}
	if !strings.Contains(ev.Content, "build finished") {
		t.Errorf("content should carry the completion summary, got %q", ev.Content)
	}
}

func TestHandleTaskEvent_NotRelayedWhileItsOwnTurnRuns(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()

	// Turn 1 starts and spawns a task that finishes immediately, while turn 1
	// is still streaming. Relaying here would cut turn 1's reply short.
	cs.turnSeq.Add(1)
	cs.handleTaskEvent(taskStartedFrame("task-1", "quick command"))
	cs.handleTaskEvent(taskNotificationFrame("task-1", "completed", "done"))

	assertNoEvent(t, cs)

	// Once turn 1's own result arrives, the watermark catches up and the
	// pending notification becomes relayable.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleTaskEvent(taskNotificationFrame("task-1", "completed", "done"))

	ev := drainEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}
}

// ── multimodal content tests ────────────────────────────────
//
// These cover the image path: the CLI accepts message.content as either a
// string or an array of content blocks, and images must be delivered as base64
// blocks inside the SAME frame as the prompt.

// TestBuildUserContent_NoImagesIsPlainString pins the default: without images
// the content is the bare prompt string, not a single-element array. Existing
// text-only behaviour must not change shape.
func TestBuildUserContent_NoImagesIsPlainString(t *testing.T) {
	got := buildUserContent("hello", nil)
	s, ok := got.(string)
	if !ok {
		t.Fatalf("content type = %T, want string", got)
	}
	if s != "hello" {
		t.Errorf("content = %q, want %q", s, "hello")
	}
}

// TestBuildUserContent_SingleImage verifies image-before-text ordering and the
// base64 source block shape the CLI expects.
func TestBuildUserContent_SingleImage(t *testing.T) {
	data := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x01, 0x02}
	got := buildUserContent("what is this?", []core.ImageAttachment{
		{MimeType: "image/png", Data: data, FileName: "x.png"},
	})

	parts, ok := got.([]map[string]any)
	if !ok {
		t.Fatalf("content type = %T, want []map[string]any", got)
	}
	if len(parts) != 2 {
		t.Fatalf("parts len = %d, want 2", len(parts))
	}

	// Image block first.
	img := parts[0]
	if img["type"] != "image" {
		t.Errorf("parts[0].type = %v, want image", img["type"])
	}
	src, ok := img["source"].(map[string]any)
	if !ok {
		t.Fatalf("parts[0].source is not an object: %v", img["source"])
	}
	if src["type"] != "base64" {
		t.Errorf("source.type = %v, want base64", src["type"])
	}
	if src["media_type"] != "image/png" {
		t.Errorf("source.media_type = %v, want image/png", src["media_type"])
	}
	wantB64 := base64.StdEncoding.EncodeToString(data)
	if src["data"] != wantB64 {
		t.Errorf("source.data = %v, want %v", src["data"], wantB64)
	}

	// Text block last.
	txt := parts[1]
	if txt["type"] != "text" {
		t.Errorf("parts[1].type = %v, want text", txt["type"])
	}
	if txt["text"] != "what is this?" {
		t.Errorf("parts[1].text = %v, want %q", txt["text"], "what is this?")
	}
}

// TestBuildUserContent_MultipleImagesPreserveOrder verifies each image becomes
// its own block in the caller's order. Order matters: the model refers to
// "the first image" / "the second image".
func TestBuildUserContent_MultipleImagesPreserveOrder(t *testing.T) {
	got := buildUserContent("compare", []core.ImageAttachment{
		{MimeType: "image/png", Data: []byte("one")},
		{MimeType: "image/jpeg", Data: []byte("two")},
		{MimeType: "image/webp", Data: []byte("three")},
	})

	parts := got.([]map[string]any)
	if len(parts) != 4 { // 3 images + 1 text
		t.Fatalf("parts len = %d, want 4", len(parts))
	}

	wantMimes := []string{"image/png", "image/jpeg", "image/webp"}
	wantData := []string{"one", "two", "three"}
	for i := range wantMimes {
		src := parts[i]["source"].(map[string]any)
		if src["media_type"] != wantMimes[i] {
			t.Errorf("parts[%d].source.media_type = %v, want %v", i, src["media_type"], wantMimes[i])
		}
		want := base64.StdEncoding.EncodeToString([]byte(wantData[i]))
		if src["data"] != want {
			t.Errorf("parts[%d].source.data = %v, want %v", i, src["data"], want)
		}
	}
	if parts[3]["type"] != "text" {
		t.Errorf("last part type = %v, want text", parts[3]["type"])
	}
}

// TestBuildUserContent_DefaultsMimeType verifies an attachment with no mime
// type still produces a usable block rather than an empty media_type, which
// the model API would reject.
func TestBuildUserContent_DefaultsMimeType(t *testing.T) {
	got := buildUserContent("x", []core.ImageAttachment{{Data: []byte("d")}})
	src := got.([]map[string]any)[0]["source"].(map[string]any)
	if src["media_type"] != "image/png" {
		t.Errorf("media_type = %v, want image/png fallback", src["media_type"])
	}
}

// TestBuildUserContent_EmptyPromptStillHasTextBlock verifies an image-only
// message (no caption) still carries a text part, since some providers reject
// an image-only content array.
func TestBuildUserContent_EmptyPromptStillHasTextBlock(t *testing.T) {
	for _, prompt := range []string{"", "   ", "\n\t"} {
		got := buildUserContent(prompt, []core.ImageAttachment{{MimeType: "image/png", Data: []byte("d")}})
		parts := got.([]map[string]any)
		last := parts[len(parts)-1]
		if last["type"] != "text" {
			t.Fatalf("prompt %q: last part type = %v, want text", prompt, last["type"])
		}
		if s, _ := last["text"].(string); strings.TrimSpace(s) == "" {
			t.Errorf("prompt %q: text block is empty", prompt)
		}
	}
}

// TestSend_ResidentWithImages_WritesMultimodalFrame is the end-to-end check:
// a resident Send with images must put an array (not a string) in the frame's
// message.content, with the prompt inside it.
func TestSend_ResidentWithImages_WritesMultimodalFrame(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true
	stdin := &captureStdin{}
	cs.stdin = stdin

	err := cs.Send("describe this", []core.ImageAttachment{
		{MimeType: "image/png", Data: []byte("PNGDATA")},
	}, nil)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	frames := stdin.frames(t)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d: %v", len(frames), frames)
	}
	f := frames[0]
	if f["type"] != "user" {
		t.Errorf("frame type = %v, want user", f["type"])
	}

	msg, ok := f["message"].(map[string]any)
	if !ok {
		t.Fatalf("message is not an object: %v", f["message"])
	}
	if msg["role"] != "user" {
		t.Errorf("message.role = %v, want user", msg["role"])
	}

	// content MUST be an array here — a string would silently drop the image.
	parts, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("message.content type = %T, want array (image was dropped)", msg["content"])
	}
	if len(parts) != 2 {
		t.Fatalf("content parts = %d, want 2", len(parts))
	}
	first := parts[0].(map[string]any)
	if first["type"] != "image" {
		t.Errorf("first block type = %v, want image", first["type"])
	}
	last := parts[1].(map[string]any)
	if last["type"] != "text" || last["text"] != "describe this" {
		t.Errorf("last block = %v, want text %q", last, "describe this")
	}
}

// TestSend_ResidentWithoutImages_KeepsStringContent guards the regression that
// matters most: the existing text-only path must still emit a plain string.
func TestSend_ResidentWithoutImages_KeepsStringContent(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true
	stdin := &captureStdin{}
	cs.stdin = stdin

	if err := cs.Send("just text", nil, nil); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	frames := stdin.frames(t)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	msg := frames[0]["message"].(map[string]any)
	s, ok := msg["content"].(string)
	if !ok {
		t.Fatalf("message.content type = %T, want string", msg["content"])
	}
	if s != "just text" {
		t.Errorf("content = %q, want %q", s, "just text")
	}
}

// TestSend_ResidentWithImagesAndFiles verifies both paths compose: files are
// appended to the prompt as path references, and the result is still wrapped
// in the multimodal array.
func TestSend_ResidentWithImagesAndFiles(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true
	cs.workDir = t.TempDir()
	stdin := &captureStdin{}
	cs.stdin = stdin

	if err := cs.Send("look", []core.ImageAttachment{{MimeType: "image/png", Data: []byte("IMG")}},
		[]core.FileAttachment{{MimeType: "text/plain", Data: []byte("body"), FileName: "notes.txt"}}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	parts := stdin.frames(t)[0]["message"].(map[string]any)["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("content parts = %d, want 2", len(parts))
	}
	text := parts[1].(map[string]any)["text"].(string)
	if !strings.Contains(text, "look") {
		t.Errorf("text block lost the prompt: %q", text)
	}
	if !strings.Contains(text, "notes.txt") {
		t.Errorf("text block missing file path reference: %q", text)
	}
}

// TestSend_ResidentWithImages_TurnSeqBumped verifies the multimodal path still
// advances the turn counter — background-task attribution depends on it, and
// an early return in the image branch would silently break that.
func TestSend_ResidentWithImages_TurnSeqBumped(t *testing.T) {
	cs := newTestSession()
	defer cs.cancel()
	cs.interruptible = true
	cs.stdin = &captureStdin{}

	before := cs.turnSeq.Load()
	if err := cs.Send("x", []core.ImageAttachment{{MimeType: "image/png", Data: []byte("d")}}, nil); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if got := cs.turnSeq.Load(); got != before+1 {
		t.Errorf("turnSeq = %d, want %d", got, before+1)
	}
}

// TestAnnotateUnsupportedImages verifies the per-turn spawn path (which has no
// channel for content blocks) tells the model an image was attached instead of
// dropping it silently. Silently dropping is what produced the original
// "where is the image?" confusion.
func TestAnnotateUnsupportedImages(t *testing.T) {
	t.Run("no images leaves prompt untouched", func(t *testing.T) {
		got := annotateUnsupportedImages("look at this", nil)
		if got != "look at this" {
			t.Errorf("prompt = %q, want unchanged", got)
		}
	})

	t.Run("images disclose the limitation", func(t *testing.T) {
		got := annotateUnsupportedImages("look at this", []core.ImageAttachment{
			{MimeType: "image/png", Data: []byte("d")},
		})
		if !strings.Contains(got, "look at this") {
			t.Errorf("prompt lost its original text: %q", got)
		}
		if !strings.Contains(got, "cannot deliver image content") {
			t.Errorf("annotation missing the limitation note: %q", got)
		}
		if !strings.Contains(got, "1 image(s)") {
			t.Errorf("annotation missing the image count: %q", got)
		}
	})

	t.Run("count reflects multiple images", func(t *testing.T) {
		got := annotateUnsupportedImages("x", []core.ImageAttachment{
			{MimeType: "image/png", Data: []byte("a")},
			{MimeType: "image/png", Data: []byte("b")},
		})
		if !strings.Contains(got, "2 image(s)") {
			t.Errorf("annotation missing the image count: %q", got)
		}
	})
}
