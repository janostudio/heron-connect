package codebuddy

// probe_test.go — hermetic tests for the one-shot ACP model probe.
//
// These do not touch the real codebuddy binary. Each test puts a fake
// executable named "codebuddy" on PATH that speaks just enough
// newline-delimited JSON-RPC to exercise the probe's parsing, and the
// probe is invoked by name so PATH resolution picks the fake up.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// writeFakeCLI installs a fake `codebuddy` on a temp PATH and returns the
// PATH value to use. The script body is a POSIX shell snippet.
func writeFakeCLI(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "codebuddy")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// modelUpdateLine builds the session/update notification the real CLI sends
// for the model config option.
func modelUpdateLine(current string, opts ...[2]string) string {
	var sb strings.Builder
	sb.WriteString(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"config_option_update","configOptions":[`)
	sb.WriteString(`{"type":"select","id":"mode","category":"mode","currentValue":"default","options":[]},`)
	sb.WriteString(`{"type":"select","id":"model","category":"model","currentValue":"` + current + `","options":[`)
	for i, o := range opts {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"value":"` + o[0] + `","name":"` + o[1] + `"}`)
	}
	sb.WriteString(`]}]}}}`)
	return sb.String()
}

func TestProbeCodeBuddyModels_ParsesModelList(t *testing.T) {
	update := modelUpdateLine("gpt-5.6-terra",
		[2]string{"claude-sonnet-5", "Claude-Sonnet-5"},
		[2]string{"gpt-5.6-terra", "GPT-5.6-Terra"},
		[2]string{"glm-5.3-ioa", "GLM-5.3"},
	)
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
printf '%s\n' '`+update+`'
sleep 0.3
`))

	cur, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)

	if cur != "gpt-5.6-terra" {
		t.Errorf("current = %q, want %q", cur, "gpt-5.6-terra")
	}
	want := []core.ModelOption{
		{Name: "claude-sonnet-5", Desc: "Claude-Sonnet-5"},
		{Name: "gpt-5.6-terra", Desc: "GPT-5.6-Terra"},
		{Name: "glm-5.3-ioa", Desc: "GLM-5.3"},
	}
	if len(models) != len(want) {
		t.Fatalf("got %d models, want %d: %+v", len(models), len(want), models)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Errorf("model[%d] = %+v, want %+v", i, models[i], want[i])
		}
	}
}

// The model selector is matched by category, but some builds set id only.
// Both must work, and a non-model select must not be mistaken for it.
func TestProbeCodeBuddyModels_MatchesByIdOrCategory(t *testing.T) {
	update := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"config_option_update","configOptions":[` +
		`{"id":"thought_level","category":"thought_level","currentValue":"enabled","options":[{"value":"max","name":"Max"}]},` +
		`{"id":"model","category":"other","currentValue":"m1","options":[{"value":"m1","name":"M1"}]}` +
		`]}}}`
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+update+`'
sleep 0.3
`))

	cur, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)
	if cur != "m1" || len(models) != 1 || models[0].Name != "m1" {
		t.Fatalf("got current=%q models=%+v, want m1 / [m1]", cur, models)
	}
}

// A thought_level-only payload must not be reported as a model list.
func TestProbeCodeBuddyModels_IgnoresNonModelOptions(t *testing.T) {
	update := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"config_option_update","configOptions":[` +
		`{"id":"thought_level","category":"thought_level","currentValue":"enabled","options":[{"value":"max","name":"Max"}]}` +
		`]}}}`
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+update+`'
sleep 0.3
`))

	cur, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)
	if cur != "" || len(models) != 0 {
		t.Fatalf("got current=%q models=%+v, want empty", cur, models)
	}
}

// Unrelated notifications must be skipped without derailing the scan.
func TestProbeCodeBuddyModels_SkipsUnrelatedNotifications(t *testing.T) {
	update := modelUpdateLine("m1", [2]string{"m1", "M1"})
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk"}}}'
printf '%s\n' 'not json at all'
printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"config_option_update","configOptions":[]}}}'
printf '%s\n' '`+update+`'
sleep 0.3
`))

	cur, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)
	if cur != "m1" || len(models) != 1 {
		t.Fatalf("got current=%q models=%+v, want m1 / 1 model", cur, models)
	}
}

// Server-initiated requests must be declined, otherwise the CLI can stall
// waiting for a reply and the probe would hit its timeout.
func TestProbeCodeBuddyModels_DeclinesServerRequests(t *testing.T) {
	update := modelUpdateLine("m1", [2]string{"m1", "M1"})
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '{"jsonrpc":"2.0","id":99,"method":"fs/read_text_file","params":{}}'
printf '%s\n' '`+update+`'
sleep 0.3
`))

	cur, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)
	if cur != "m1" || len(models) != 1 {
		t.Fatalf("got current=%q models=%+v, want m1 / 1 model", cur, models)
	}
}

// A CLI that emits nothing must not hang the caller: the probe's own
// timeout is what saves us, so shrink it for the test.
func TestProbeCodeBuddyModels_TimesOutQuietly(t *testing.T) {
	orig := codebuddyModelProbeTimeout
	codebuddyModelProbeTimeout = 700 * time.Millisecond
	t.Cleanup(func() { codebuddyModelProbeTimeout = orig })

	t.Setenv("PATH", writeFakeCLI(t, "sleep 30\n"))

	start := time.Now()
	cur, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)
	elapsed := time.Since(start)

	if cur != "" || models != nil {
		t.Fatalf("got current=%q models=%+v, want empty", cur, models)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("probe took %s, expected to give up near the 700ms timeout", elapsed)
	}
}

// A missing binary must fail fast and quietly rather than panicking.
func TestProbeCodeBuddyModels_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cur, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)
	if cur != "" || models != nil {
		t.Fatalf("got current=%q models=%+v, want empty", cur, models)
	}
}

// An already-cancelled context must return promptly.
func TestProbeCodeBuddyModels_RespectsCancelledContext(t *testing.T) {
	t.Setenv("PATH", writeFakeCLI(t, "sleep 30\n"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cur, models := probeCodeBuddyModels(ctx, "codebuddy", t.TempDir(), nil, nil)
		if cur != "" || models != nil {
			t.Errorf("got current=%q models=%+v, want empty", cur, models)
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("probe did not return after context cancellation")
	}
}

// currentValue may be a JSON boolean for non-string option types on some
// builds; the probe must not choke on it.
func TestProbeCodeBuddyModels_ToleratesBooleanCurrentValue(t *testing.T) {
	update := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"config_option_update","configOptions":[` +
		`{"id":"multitask","category":"other","currentValue":true,"options":[]},` +
		`{"id":"model","category":"model","currentValue":"m1","options":[{"value":"m1","name":"M1"}]}` +
		`]}}}`
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+update+`'
sleep 0.3
`))

	cur, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)
	if cur != "m1" || len(models) != 1 {
		t.Fatalf("got current=%q models=%+v, want m1 / 1 model", cur, models)
	}
}

func TestProbeCodeBuddyModels_SkipsEntriesWithEmptyValue(t *testing.T) {
	update := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"config_option_update","configOptions":[` +
		`{"id":"model","category":"model","currentValue":"m1","options":[` +
		`{"value":"","name":"Broken"},{"value":"m1","name":"M1"}]}` +
		`]}}}`
	t.Setenv("PATH", writeFakeCLI(t, `
read -r line
read -r line
printf '%s\n' '`+update+`'
sleep 0.3
`))

	_, models := probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)
	if len(models) != 1 || models[0].Name != "m1" {
		t.Fatalf("got models=%+v, want only m1", models)
	}
}

// The probe must not leave the child process running: a leaked CLI per
// /model open would accumulate. The fake records its own PID, and the test
// asserts that PID is gone once the probe returns.
//
// Note the probe escalates to SIGKILL on the whole process group (via
// core.ForceKillProcessGroup), which a shell trap cannot intercept — so
// this checks for actual termination rather than a graceful signal.
func TestProbeCodeBuddyModels_ReapsChildProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("PATH", writeFakeCLI(t, `
printf '%s' "$$" > "`+pidFile+`"
read -r line
read -r line
printf '%s\n' '`+modelUpdateLine("m1", [2]string{"m1", "M1"})+`'
while true; do sleep 0.1; done
`))

	probeCodeBuddyModels(context.Background(), "codebuddy", t.TempDir(), nil, nil)

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("fake CLI never recorded its pid: %v", err)
	}
	pid := strings.TrimSpace(string(raw))
	if pid == "" {
		t.Fatal("empty pid recorded")
	}

	// The probe returns only after teardown, so the process must already be
	// gone (or a zombie awaiting reap by its parent).
	if processRunning(t, pid) {
		t.Fatalf("probe left child pid %s running", pid)
	}
}

// processRunning reports whether pid is still alive. signal 0 performs the
// permission/existence check without delivering a signal.
func processRunning(t *testing.T, pid string) bool {
	t.Helper()
	p, err := os.FindProcess(atoi(t, pid))
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// ESRCH means gone; EPERM would mean alive but not ours.
	return errors.Is(err, syscall.EPERM)
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("bad pid %q: %v", s, err)
	}
	return n
}
