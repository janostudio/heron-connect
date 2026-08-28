// Package heron integrates the Heron AI Flow engine as a heron-connect agent.
//
// The adapter starts a long-lived Heron CLI process and communicates with it
// over newline-delimited JSON-RPC 2.0 on stdin/stdout. The Heron process owns
// FlowSession persistence; this package only translates the CLI protocol into
// core.AgentSession events.
package heron

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
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
	extraEnv   []string
	sessionEnv []string

	mu sync.RWMutex
}

// New builds a Heron CLI agent.
//
// Supported options:
//   - work_dir: process working directory
//   - command: Heron binary path/name, default "heron"
//   - args: extra arguments; --json-rpc is added when absent
//   - env: map of environment overrides
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

func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.RLock()
	workDir := a.workDir
	command := a.command
	args := append([]string(nil), a.args...)
	extraEnv := append([]string(nil), a.extraEnv...)
	extraEnv = append(extraEnv, a.sessionEnv...)
	a.mu.RUnlock()

	return newHeronSession(ctx, command, args, workDir, sessionID, extraEnv)
}

func (a *Agent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}

func (a *Agent) Stop() error { return nil }

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
