package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/janostudio/heron-connect/core"
)

func TestHandleResultParsesUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs := &claudeSession{
		events: make(chan core.Event, 8),
		ctx:    ctx,
	}
	cs.sessionID.Store("test-session")
	cs.alive.Store(true)

	raw := map[string]any{
		"type":       "result",
		"result":     "done",
		"session_id": "test-session",
		"usage": map[string]any{
			"input_tokens":  float64(150000),
			"output_tokens": float64(2000),
		},
	}

	cs.handleResult(raw)

	evt := <-cs.events
	if evt.InputTokens != 150000 {
		t.Errorf("InputTokens = %d, want 150000", evt.InputTokens)
	}
	if evt.OutputTokens != 2000 {
		t.Errorf("OutputTokens = %d, want 2000", evt.OutputTokens)
	}
}

func TestHandleResultNoUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs := &claudeSession{
		events: make(chan core.Event, 8),
		ctx:    ctx,
	}
	cs.sessionID.Store("test-session")
	cs.alive.Store(true)

	raw := map[string]any{
		"type":   "result",
		"result": "done",
	}

	cs.handleResult(raw)

	evt := <-cs.events
	if evt.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", evt.InputTokens)
	}
	if evt.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0", evt.OutputTokens)
	}
}

func TestReadLoop_ChildHoldsStdoutPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
	})

	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(pw, `{"type":"system","session_id":"test-pipe"}`+"\n")
		writeDone <- err
	}()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	cs := &claudeSession{
		cmd:    cmd,
		events: make(chan core.Event, 64),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	cs.alive.Store(true)
	go cs.readLoop(pr, &stderrBuf)

	timeout := time.After(5 * time.Second)
	gotEvent := false
	for {
		select {
		case err := <-writeDone:
			if err != nil {
				t.Fatal(err)
			}
			writeDone = nil
		case evt, ok := <-cs.events:
			if !ok {
				if !gotEvent {
					t.Fatal("events closed but system event lost")
				}
				return
			}
			if evt.SessionID == "test-pipe" {
				gotEvent = true
			}
		case <-timeout:
			t.Fatal("HANG: events not closed within 5s - readLoop stuck in scanner.Scan()")
		}
	}
}

func TestReadLoop_CtxCancelClosesChannels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
	})

	// "err-then-sleep" emits stderr before sleeping so that ctx cancel
	// produces a non-empty stderrBuf in readLoop's defer — exercising the
	// `case <-cs.ctx.Done()` select branch in finishReadLoop.
	cmd := helperCommand(ctx, "err-then-sleep")
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	cs := &claudeSession{
		cmd:    cmd,
		events: make(chan core.Event, 64),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	cs.alive.Store(true)
	go cs.readLoop(pr, &stderrBuf)

	time.Sleep(200 * time.Millisecond)
	cancel()

	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-cs.events:
			if !ok {
				goto closed
			}
		case <-timeout:
			t.Fatal("HANG: events not closed within 5s after ctx cancel")
		}
	}
closed:
	select {
	case <-cs.done:
	case <-timeout:
		t.Fatal("HANG: done not closed within 5s after ctx cancel")
	}
}

func TestClaudeSessionClose_IdempotentNoPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := helperCommand(ctx, "stdin-eof-exit")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	cs := &claudeSession{
		cmd:                 cmd,
		stdin:               stdin,
		ctx:                 ctx,
		cancel:              cancel,
		done:                done,
		gracefulStopTimeout: 200 * time.Millisecond,
	}
	cs.alive.Store(true)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Close panicked: %v", r)
		}
	}()

	if err := cs.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := cs.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestShellJoinArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"empty", nil, ""},
		{"single_plain", []string{"--verbose"}, "--verbose"},
		{"multiple_plain", []string{"--verbose", "--model", "opus"}, "--verbose --model opus"},
		{"arg_with_space", []string{"--prompt", "hello world"}, "--prompt 'hello world'"},
		{"arg_with_tab", []string{"a\tb"}, "'a\tb'"},
		{"arg_with_newline", []string{"line1\nline2"}, "'line1\nline2'"},
		{"arg_with_single_quote", []string{"it's"}, "'it'\\''s'"},
		{"arg_with_double_quote", []string{`say "hi"`}, `'say "hi"'`},
		{"arg_with_backslash", []string{`path\to`}, `'path\to'`},
		{"mixed", []string{"--flag", "has space", "plain", "it's here"}, "--flag 'has space' plain 'it'\\''s here'"},
		{"empty_string_arg", []string{""}, ""},
		{"long_prompt", []string{"--append-system-prompt", "You are a helpful assistant.\nBe concise."}, "--append-system-prompt 'You are a helpful assistant.\nBe concise.'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellJoinArgs(tt.args)
			if got != tt.want {
				t.Errorf("shellJoinArgs(%v)\n  got  = %q\n  want = %q", tt.args, got, tt.want)
			}
		})
	}
}

func helperCommand(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess", "--", mode)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

// TestHelperProcess lets this test binary act as a tiny external command for
// cases that need a process with controlled lifetime semantics.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "err-then-sleep":
		_, _ = os.Stderr.WriteString("helper: starting up\n")
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "stdin-eof-exit":
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

// ──────────────────────────────────────────────────────────────
// CancelTurn / interrupt tests
// ──────────────────────────────────────────────────────────────

// capturingStdin collects everything written to stdin so tests can assert on the
// exact JSON frames CancelTurn produces.
type capturingStdin struct {
	mu   sync.Mutex
	data []byte
}

func (c *capturingStdin) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = append(c.data, p...)
	return len(p), nil
}

func (c *capturingStdin) Close() error { return nil }

func (c *capturingStdin) frames(t *testing.T) []map[string]any {
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

func newCancelTestSession(stdin io.WriteCloser) *claudeSession {
	cs := &claudeSession{
		stdin:   stdin,
		events:  make(chan core.Event, 8),
		ctx:     context.Background(),
		cancel:  func() {},
		done:    make(chan struct{}),
		workDir: ".",
	}
	cs.alive.Store(true)
	return cs
}

// TestCancelTurn_SendsInterruptControlRequest is the core contract: CancelTurn
// must emit a well-formed control_request/interrupt frame on stdin.
func TestCancelTurn_SendsInterruptControlRequest(t *testing.T) {
	stdin := &capturingStdin{}
	cs := newCancelTestSession(stdin)

	cs.CancelTurn()

	frames := stdin.frames(t)
	if len(frames) != 1 {
		t.Fatalf("expected exactly 1 frame, got %d: %v", len(frames), frames)
	}
	f := frames[0]

	if got := f["type"]; got != "control_request" {
		t.Errorf("type = %v, want control_request", got)
	}
	if got, _ := f["request_id"].(string); got == "" {
		t.Error("request_id must be non-empty")
	}
	req, ok := f["request"].(map[string]any)
	if !ok {
		t.Fatalf("request is not an object: %v", f["request"])
	}
	if got := req["subtype"]; got != "interrupt" {
		t.Errorf("request.subtype = %v, want interrupt", got)
	}
}

// TestCancelTurn_RequestIDsAreUnique verifies repeated interrupts are
// distinguishable, which the response correlation depends on.
func TestCancelTurn_RequestIDsAreUnique(t *testing.T) {
	stdin := &capturingStdin{}
	cs := newCancelTestSession(stdin)

	cs.CancelTurn()
	cs.CancelTurn()
	cs.CancelTurn()

	frames := stdin.frames(t)
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(frames))
	}
	seen := map[string]bool{}
	for _, f := range frames {
		id, _ := f["request_id"].(string)
		if seen[id] {
			t.Errorf("duplicate request_id %q", id)
		}
		seen[id] = true
	}
}

// TestCancelTurn_DeadSession_Noop verifies a dead session does not write
// anything — the engine falls back to its queueing path.
func TestCancelTurn_DeadSession_Noop(t *testing.T) {
	stdin := &capturingStdin{}
	cs := newCancelTestSession(stdin)
	cs.alive.Store(false)

	cs.CancelTurn()

	if frames := stdin.frames(t); len(frames) != 0 {
		t.Errorf("dead session wrote %d frames, want 0", len(frames))
	}
}

// TestInterruptible_ReflectsConfig verifies the capability flag is reported
// verbatim so the engine can gate the interrupt path.
func TestInterruptible_ReflectsConfig(t *testing.T) {
	for _, want := range []bool{true, false} {
		cs := newCancelTestSession(&capturingStdin{})
		cs.interruptible = want
		if got := cs.Interruptible(); got != want {
			t.Errorf("Interruptible() = %v, want %v", got, want)
		}
	}
}

// TestEmit_StampsTurnEpoch verifies every emitted event carries the epoch the
// engine set, so leftovers of an interrupted turn can be filtered out.
func TestEmit_StampsTurnEpoch(t *testing.T) {
	cs := newCancelTestSession(&capturingStdin{})

	cs.SetTurnEpoch(99)
	cs.emit(core.Event{Type: core.EventText, Content: "hi"})

	select {
	case ev := <-cs.events:
		if ev.TurnEpoch != 99 {
			t.Errorf("TurnEpoch = %d, want 99", ev.TurnEpoch)
		}
	case <-time.After(time.Second):
		t.Fatal("emit did not deliver the event")
	}
}

// TestEmit_UnstampedBeforeSetTurnEpoch is the compatibility guard: before the
// engine stamps an epoch, events carry 0 (accepted unconditionally).
func TestEmit_UnstampedBeforeSetTurnEpoch(t *testing.T) {
	cs := newCancelTestSession(&capturingStdin{})

	cs.emit(core.Event{Type: core.EventText, Content: "hi"})

	select {
	case ev := <-cs.events:
		if ev.TurnEpoch != 0 {
			t.Errorf("TurnEpoch = %d, want 0 for an unstamped session", ev.TurnEpoch)
		}
	case <-time.After(time.Second):
		t.Fatal("emit did not deliver the event")
	}
}

// ── background-task event family ────────────────────────────
//
// Covers the system/subtype=task_* family: how a background task's
// completion (Bash run_in_background, background subagent, Monitor watch)
// gets relayed after the turn that started it has ended. Before this was
// handled, handleSystem read only session_id and ignored subtype, so the
// whole family was invisible to the user.

// newTaskTestSession builds a minimal session for task-event tests.
func newTaskTestSession() *claudeSession {
	ctx, cancel := context.WithCancel(context.Background())
	cs := &claudeSession{
		events: make(chan core.Event, 8),
		ctx:    ctx,
		cancel: cancel,
	}
	cs.sessionID.Store("sid-1")
	cs.alive.Store(true)
	return cs
}

func drainTaskEvent(t *testing.T, cs *claudeSession) core.Event {
	t.Helper()
	select {
	case ev := <-cs.events:
		return ev
	case <-time.After(200 * time.Millisecond):
		t.Fatal("no event emitted")
		return core.Event{}
	}
}

func assertNoTaskEvent(t *testing.T, cs *claudeSession) {
	t.Helper()
	select {
	case ev := <-cs.events:
		t.Fatalf("unexpected event emitted: type=%v content=%q", ev.Type, ev.Content)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHandleSystem_TaskNotificationRelayedAfterTurn(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	cs.turnFinishedSeq.Store(0)
	cs.handleSystem(map[string]any{
		"type": "system", "subtype": "task_started",
		"task_id": "task-1", "description": "sleep 8 && echo DONE",
		"session_id": "sid-1",
	})

	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleSystem(map[string]any{
		"type": "system", "subtype": "task_notification",
		"task_id": "task-1", "status": "completed",
		"summary": `Background command "sleep 8 && echo DONE" completed`,
		"session_id": "sid-1",
	})

	// The init-style EventText must not be emitted for a task frame, so the
	// first event on the channel is the terminal result.
	ev := drainTaskEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v (task frames must not emit the init EventText)", ev.Type, core.EventResult)
	}
	if !ev.Done {
		t.Error("event should be marked Done")
	}
	if !strings.Contains(ev.Content, "已完成") {
		t.Errorf("content should report completion, got %q", ev.Content)
	}
	if !strings.Contains(ev.Content, "sleep 8") {
		t.Errorf("content should carry the summary, got %q", ev.Content)
	}
}

func TestHandleSystem_TaskFrameDoesNotEmitInitText(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	// A task frame must not be funnelled through the session-id branch,
	// which would emit a content-less EventText for the engine to append to
	// the reply.
	cs.handleSystem(map[string]any{
		"type": "system", "subtype": "task_progress",
		"task_id": "task-1", "description": "running",
		"session_id": "sid-1",
	})
	assertNoTaskEvent(t, cs)
}

func TestHandleSystem_InitStillEmitsText(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	cs.handleSystem(map[string]any{
		"type": "system", "subtype": "init", "session_id": "sid-1",
	})

	ev := drainTaskEvent(t, cs)
	if ev.Type != core.EventText {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventText)
	}
	if ev.SessionID != "sid-1" {
		t.Errorf("SessionID = %q, want sid-1", ev.SessionID)
	}
}

func TestHandleTaskEvent_TerminalUpdatedFromPatch(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_started",
		"task_id": "task-1", "description": "some command",
	})

	// A terminal transition may arrive as task_updated carrying the status
	// inside `patch`, without a following task_notification.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_updated",
		"task_id": "task-1",
		"patch":   map[string]any{"status": "completed"},
	})

	ev := drainTaskEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}
	if !strings.Contains(ev.Content, "some command") {
		t.Errorf("should fall back to task_started description, got %q", ev.Content)
	}
}

func TestHandleTaskEvent_DeduplicatesTerminalFrames(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_started",
		"task_id": "task-1", "description": "some command",
	})
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())

	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_notification",
		"task_id": "task-1", "status": "completed", "summary": "done",
	})
	if ev := drainTaskEvent(t, cs); ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}

	// Terminal task_updated for the same task is a duplicate, not a second
	// platform message.
	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_updated",
		"task_id": "task-1",
		"patch":   map[string]any{"status": "completed"},
	})
	assertNoTaskEvent(t, cs)
}

func TestHandleTaskEvent_UnknownSubtypeNotClaimed(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	if cs.handleTaskEvent(map[string]any{"subtype": "api_retry"}) {
		t.Error("non-task subtype must not be claimed by handleTaskEvent")
	}
}

func TestHandleResult_MarksTurnFinished(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	if cs.turnFinishedSeq.Load() != 0 {
		t.Fatal("turnFinishedSeq should start at 0")
	}

	cs.handleResult(map[string]any{
		"type": "result", "result": "done", "session_id": "sid-1",
	})

	if cs.turnFinishedSeq.Load() != cs.turnSeq.Load() {
		t.Error("handleResult should raise the finished-turn watermark so between-turn task frames relay")
	}
}

func TestFormatTaskCompletion_ClaudeSanitizesUntrustedText(t *testing.T) {
	got := formatTaskCompletion("task-1", "", "completed", "a\nb", "")
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("embedded newlines should be flattened, got %q", got)
	}

	long := strings.Repeat("x", taskDescriptionMaxLen*2)
	got = formatTaskCompletion("task-1", long, "completed", "", "")
	if utf8.RuneCountInString(got) > taskDescriptionMaxLen+64 {
		t.Errorf("long description should be truncated, got %d runes",
			utf8.RuneCountInString(got))
	}
}

func TestIsTerminalTaskStatus_Claude(t *testing.T) {
	for _, s := range []string{"completed", "failed", "stopped", "killed", "cancelled"} {
		if !isTerminalTaskStatus(s) {
			t.Errorf("isTerminalTaskStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "pending", "running", "paused"} {
		if isTerminalTaskStatus(s) {
			t.Errorf("isTerminalTaskStatus(%q) = true, want false", s)
		}
	}
}

func TestHandleTaskEvent_RelayedEvenAfterUserStartsNewTurn(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	// Turn 1 starts and spawns a long-running background task.
	cs.turnSeq.Add(1)
	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_started",
		"task_id": "task-1", "description": "npm run build",
	})

	// Turn 1 finishes; the task is still running.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())

	// The user sends another message, starting turn 2 while the task is in
	// flight.
	cs.turnSeq.Add(1)

	// The task completes. It was spawned in turn 1, which has finished, so it
	// must still be relayed despite turn 2 being in progress.
	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_notification",
		"task_id": "task-1", "status": "completed", "summary": "build finished",
	})

	ev := drainTaskEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}
	if !strings.Contains(ev.Content, "build finished") {
		t.Errorf("content should carry the completion summary, got %q", ev.Content)
	}
}

func TestHandleTaskEvent_NotRelayedWhileItsOwnTurnRuns(t *testing.T) {
	cs := newTaskTestSession()
	defer cs.cancel()

	// Turn 1 spawns a task that finishes immediately, while turn 1 is still
	// streaming. Relaying here would cut turn 1's reply short.
	cs.turnSeq.Add(1)
	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_started",
		"task_id": "task-1", "description": "quick command",
	})
	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_notification",
		"task_id": "task-1", "status": "completed", "summary": "done",
	})

	assertNoTaskEvent(t, cs)

	// Once turn 1's own result arrives, the watermark catches up.
	cs.turnFinishedSeq.Store(cs.turnSeq.Load())
	cs.handleTaskEvent(map[string]any{
		"type": "system", "subtype": "task_notification",
		"task_id": "task-1", "status": "completed", "summary": "done",
	})

	ev := drainTaskEvent(t, cs)
	if ev.Type != core.EventResult {
		t.Fatalf("event type = %v, want %v", ev.Type, core.EventResult)
	}
}
