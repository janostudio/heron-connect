package codebuddy

// probe.go — discover CodeBuddy Code's real, post-login model list.
//
// Why not a static list: the models a given CodeBuddy account can use are
// decided by the backend at login time and change as the service adds or
// retires models (e.g. `gpt-5.6-terra`). A hardcoded list goes stale and
// silently misreports which model the user is actually running, which then
// breaks the /model picker's "current model" highlight.
//
// The authoritative source is the ACP handshake: `codebuddy --acp` answers
// `session/new` with a `config_option_update` notification carrying a
// `configOptions` entry whose id is "model", listing every selectable model
// as {value, name, description} plus the currently active `currentValue`.
//
// This is a one-shot, read-only probe. It spawns the CLI with
// --no-session-persistence so it does not create a session record on disk
// (otherwise every /model open would append a stray entry to the user's
// session list).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// codebuddyModelProbeTimeout bounds the one-shot ACP handshake used to
// discover models. Measured at roughly 1s against a warm CLI; the margin
// covers a cold node start. A variable (not a const) so tests can shorten
// it, matching the convention in agent/acp/list_sessions.go.
var codebuddyModelProbeTimeout = 15 * time.Second

// acpProbeConfigOption mirrors one entry of the ACP `configOptions` array.
// currentValue is typed as RawMessage because some CodeBuddy builds emit a
// bare JSON boolean for type:"boolean" options (e.g. "multitask"), while
// the spec says string.
type acpProbeConfigOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	Type         string `json:"type"`
	CurrentValue any    `json:"currentValue"`
	Options      []struct {
		Value       string `json:"value"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	} `json:"options"`
}

// currentValueString stringifies currentValue across the encodings the CLI
// is known to emit (string / bool / number). Returns "" for null or absent.
func (o acpProbeConfigOption) currentValueString() string {
	switch v := o.CurrentValue.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return fmt.Sprintf("%v", v)
	case nil:
		return ""
	default:
		// Unknown shape (object/array): fall back to raw JSON text rather
		// than dropping the value, so callers can still echo something.
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// probeCodeBuddyModels spawns `codebuddy --acp --no-session-persistence`,
// performs initialize + session/new, and returns the model list advertised
// for this account along with the currently active model id.
//
// Returns ("", nil) on any failure: the caller falls back to models.json
// and then to a static list, so a probe failure must never be fatal or
// block the /model menu.
func probeCodeBuddyModels(ctx context.Context, binary, workDir string, extraArgs, extraEnv []string) (current string, models []core.ModelOption) {
	probeCtx, cancel := context.WithTimeout(ctx, codebuddyModelProbeTimeout)
	defer cancel()

	// --no-session-persistence keeps the probe from leaving a session file
	// behind. It does not affect the config_option_update payload.
	args := []string{"--acp", "--no-session-persistence"}
	args = append(args, extraArgs...)

	cmd := exec.CommandContext(probeCtx, binary, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = core.MergeEnv(os.Environ(), extraEnv)
	// Own process group so teardown can reap the CLI and anything it spawned.
	core.PrepareCmdForKill(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		slog.Warn("codebuddy: model probe stdin pipe", "error", err)
		return "", nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		slog.Warn("codebuddy: model probe stdout pipe", "error", err)
		return "", nil
	}
	// os/exec copies the child's stderr on its own goroutine, which can still
	// be running when we read the buffer if teardown had to abandon its
	// wait. Guard it so the diagnostic read is race-free.
	var stderrBuf lockedBuffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		slog.Warn("codebuddy: model probe start failed", "error", err)
		return "", nil
	}
	defer teardownProbe(cmd, stdin)

	current, models = readModelOption(probeCtx, stdin, stdout)
	if len(models) == 0 {
		slog.Debug("codebuddy: model probe found no model list",
			"stderr", truncateProbeLog(stderrBuf.String()))
	}
	return current, models
}

// lockedBuffer is a bytes.Buffer safe for concurrent Write/String, used for
// the probe child's stderr where the writer (os/exec) may outlive our reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// readModelOption drives the JSON-RPC exchange and scans the incoming
// stream for the model config option. Responses to our own requests are
// written by the CLI, and the model list arrives as a `session/update`
// notification, so the scan is notification-driven rather than a plain
// request/response call.
func readModelOption(ctx context.Context, stdin io.Writer, stdout io.Reader) (string, []core.ModelOption) {
	enc := json.NewEncoder(stdin)

	writeReq := func(id int, method string, params map[string]any) bool {
		if err := enc.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params":  params,
		}); err != nil {
			slog.Warn("codebuddy: model probe write failed", "method", method, "error", err)
			return false
		}
		return true
	}

	if !writeReq(1, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{
			"name":    "heron-connect",
			"title":   "heron-connect",
			"version": core.CurrentVersion,
		},
	}) {
		return "", nil
	}

	cwd := ""
	if cmdDir, err := os.Getwd(); err == nil {
		cwd = cmdDir
	}
	if !writeReq(2, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}) {
		return "", nil
	}

	// Answer any server-initiated request with method-not-found so the CLI
	// never stalls waiting on us; we only want to observe notifications.
	respondUnimplemented := func(enc *json.Encoder, id json.RawMessage) {
		_ = enc.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]any{"code": -32601, "message": "probe: method not implemented"},
		})
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	type scanResult struct {
		current string
		models  []core.ModelOption
	}
	done := make(chan scanResult, 1)

	go func() {
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var env struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					Update struct {
						SessionUpdate string                 `json:"sessionUpdate"`
						ConfigOptions []acpProbeConfigOption `json:"configOptions"`
					} `json:"update"`
				} `json:"params"`
			}
			if err := json.Unmarshal(line, &env); err != nil {
				continue
			}

			// Server-initiated request (has both method and id): decline it.
			if env.Method != "" && len(env.ID) > 0 && !bytes.Equal(bytes.TrimSpace(env.ID), []byte("null")) {
				respondUnimplemented(enc, env.ID)
				continue
			}

			if env.Params.Update.SessionUpdate != "config_option_update" {
				continue
			}
			for _, opt := range env.Params.Update.ConfigOptions {
				// The model selector is identified by category rather than a
				// literal id across CodeBuddy builds, so accept either.
				if opt.Category != "model" && opt.ID != "model" {
					continue
				}
				models := make([]core.ModelOption, 0, len(opt.Options))
				for _, v := range opt.Options {
					if v.Value == "" {
						continue
					}
					models = append(models, core.ModelOption{Name: v.Value, Desc: v.Name})
				}
				done <- scanResult{current: opt.currentValueString(), models: models}
				return
			}
		}
		done <- scanResult{}
	}()

	select {
	case <-ctx.Done():
		return "", nil
	case res := <-done:
		return res.current, res.models
	}
}

// teardownProbe reaps the probe process. Closing stdin makes a cooperative
// CLI exit on its own; if it doesn't, the whole process group is killed so
// no orphaned codebuddy process is left behind on every /model open.
func teardownProbe(cmd *exec.Cmd, stdin io.Closer) {
	_ = stdin.Close()

	waited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waited)
	}()

	select {
	case <-waited:
		return
	case <-time.After(2 * time.Second):
	}

	_ = core.ForceKillProcessGroup(cmd)
	select {
	case <-waited:
	case <-time.After(1 * time.Second):
		// Last resort: abandon the wait goroutine rather than block.
	}
}

func truncateProbeLog(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
