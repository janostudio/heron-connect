// Package heron integrates the Heron AI Flow engine as a heron-connect agent.
//
// The adapter starts a long-lived Heron CLI process and communicates with it
// over newline-delimited JSON-RPC 2.0 on stdin/stdout. The Heron process owns
// FlowSession persistence; this package only translates the CLI protocol into
// core.AgentSession events.
package heron

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/janostudio/heron-connect/core"
)

func init() {
	core.RegisterAgent("heron", New)
}

// Agent starts the Heron CLI configured by command + args.
type Agent struct {
	workDir    string
	command    string
	args       []string
	model      string
	extraEnv   []string
	sessionEnv []string

	// serveURL switches the adapter from "one CLI process per conversation"
	// to "one long-lived engine serving every conversation". Empty means the
	// stdio mode, unchanged.
	serveURL string
	// serveToken is the bearer token the engine's --serve requires when it
	// listens beyond loopback.
	serveToken string
	// serveManage makes this connector start (and stop) the engine itself.
	// Off by default: an operator who already runs `heron-ai --serve` under a
	// supervisor does not want a second copy appearing next to it.
	serveManage bool
	// serverCmd is the engine this connector started, nil when it did not.
	serverCmd *exec.Cmd

	mu sync.RWMutex
}

// New builds a Heron CLI agent.
//
// Supported options:
//   - work_dir: process working directory
//   - command: Heron binary path/name, default "heron"
//   - model: default model override (passed as `--model`; heron-ai resolves it
//     against its own .agents/models.json)
//   - args: extra arguments; --json-rpc is added when absent
//   - env: map of environment overrides
//   - serve: base URL of an already-running `heron-ai --serve`. When set, the
//     adapter talks HTTP instead of starting a CLI process per conversation;
//     empty keeps the stdio behaviour. Serve mode is what makes a scheduled
//     wake-up reachable: the engine outlives its turns, and the session's
//     event stream stays open, so a turn it starts on its own still arrives.
//   - serve_token: bearer token for that server, when it requires one.
func New(opts map[string]any) (core.Agent, error) {
	workDir, _ := opts["work_dir"].(string)
	if strings.TrimSpace(workDir) == "" {
		workDir = "."
	}

	command, _ := opts["command"].(string)
	if strings.TrimSpace(command) == "" {
		command = "heron"
	}
	command = strings.TrimSpace(command)

	model, _ := opts["model"].(string)
	model = strings.TrimSpace(model)

	serveURL, _ := opts["serve"].(string)
	serveURL = strings.TrimSpace(serveURL)
	serveToken, _ := opts["serve_token"].(string)
	serveToken = strings.TrimSpace(serveToken)
	serveManage := false
	if value, ok := opts["serve_manage"].(bool); ok {
		serveManage = value
	}

	if serveURL != "" {
		// No CLI process is started in this mode, so the binary does not have
		// to be on this machine's PATH at all.
		return &Agent{
			workDir:     workDir,
			command:     command,
			model:       model,
			extraEnv:    envPairsFromOpts(opts["env"]),
			serveURL:    serveURL,
			serveToken:  serveToken,
			serveManage: serveManage,
		}, nil
	}

	args := parseStringSlice(opts["args"])
	if !hasArg(args, "--json-rpc") {
		args = append([]string{"--json-rpc"}, args...)
	}

	if _, err := exec.LookPath(command); err != nil {
		return nil, fmt.Errorf("heron: %q CLI not found in PATH: %w", command, err)
	}

	return &Agent{
		workDir:  workDir,
		command:  command,
		args:     args,
		model:    model,
		extraEnv: envPairsFromOpts(opts["env"]),
	}, nil
}

func (a *Agent) Name() string           { return "heron" }
func (a *Agent) CLIBinaryName() string  { return a.command }
func (a *Agent) CLIDisplayName() string { return "Heron AI" }

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workDir = dir
	slog.Info("heron: work_dir changed", "work_dir", dir)
}

func (a *Agent) GetWorkDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workDir
}

func (a *Agent) SetSessionEnv(env []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionEnv = append([]string(nil), env...)
}

// SetModel sets the default-model override passed to heron-ai via --model.
// The change takes effect on the next session. heron-ai resolves the name
// against .agents/models.json, so this only overrides the default model and
// never disturbs per-agent model assignments inside a flow.
func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = strings.TrimSpace(model)
	slog.Info("heron: model changed", "model", a.model)
}

// GetModel returns the current default-model override (empty = use heron-ai's
// own models.json default).
func (a *Agent) GetModel() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// AvailableModels reads the model list from <workDir>/.agents/models.json —
// the same source heron-ai itself uses. heron-ai owns its models.json (base_url,
// api_key, multi-model routing), so heron-connect only surfaces the selectable
// names here rather than managing providers.
func (a *Agent) AvailableModels(_ context.Context) []core.ModelOption {
	a.mu.RLock()
	workDir := a.workDir
	a.mu.RUnlock()

	path := filepath.Join(workDir, ".agents", "models.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}

	out := make([]core.ModelOption, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		name := strings.TrimSpace(m.ID)
		if name == "" {
			name = strings.TrimSpace(m.Name)
		}
		if name == "" {
			continue
		}
		out = append(out, core.ModelOption{Name: name, Desc: strings.TrimSpace(m.Name)})
	}
	return out
}

func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.RLock()
	serveURL := a.serveURL
	serveToken := a.serveToken
	a.mu.RUnlock()
	if serveURL != "" {
		// The engine owns the conversation; this handle is a client of it. The
		// session id is the engine's, so a restart of this connector resumes
		// the same conversation.
		if err := a.ensureServeProcess(ctx); err != nil {
			return nil, err
		}
		return newHTTPSession(serveURL, serveToken, sessionID), nil
	}

	a.mu.RLock()
	workDir := a.workDir
	command := a.command
	args := append([]string(nil), a.args...)
	model := a.model
	extraEnv := append([]string(nil), a.extraEnv...)
	extraEnv = append(extraEnv, a.sessionEnv...)
	a.mu.RUnlock()

	// Pass the default-model override to heron-ai. heron-ai resolves the name
	// against .agents/models.json (the "model" field), leaving multi-model
	// flows untouched — only the default model changes.
	if model != "" && !hasArg(args, "--model") {
		args = append(args, "--model", model)
	}

	return newHeronSession(ctx, command, args, workDir, sessionID, extraEnv)
}

func (a *Agent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}

// Stop asks an engine this connector started to exit. The engine's shutdown
// path releases the port; --exit-with-parent covers the case where this
// process is killed before reaching here.
func (a *Agent) Stop() error {
	a.stopServeProcess()
	return nil
}

func parseStringSlice(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case []string:
		return append([]string(nil), v...)
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			result = append(result, fmt.Sprint(item))
		}
		return result
	default:
		return nil
	}
}

func envPairsFromOpts(value any) []string {
	switch env := value.(type) {
	case map[string]string:
		result := make([]string, 0, len(env))
		for key, value := range env {
			result = append(result, key+"="+value)
		}
		return result
	case map[string]any:
		result := make([]string, 0, len(env))
		for key, value := range env {
			result = append(result, fmt.Sprintf("%s=%v", key, value))
		}
		return result
	default:
		return nil
	}
}

func hasArg(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}

var _ core.ModelSwitcher = (*Agent)(nil)
