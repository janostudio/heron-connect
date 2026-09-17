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
