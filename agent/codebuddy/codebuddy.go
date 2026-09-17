// Package codebuddy integrates CodeBuddy Code CLI
// (https://codebuddy.ai/) as a first-class heron-connect agent.
//
// CodeBuddy Code supports headless mode via `codebuddy -p <prompt>
// --output-format stream-json`, matching the same per-turn spawn +
// JSON-Lines stdout pattern used by qoder, gemini, cursor, and kimi.
// This package wraps that CLI interface so that users can write
// `type = "codebuddy"` in their project config without worrying about
// the underlying flags.
//
// Authentication is delegated entirely to the local CodeBuddy Code
// CLI: the spawned `codebuddy` subprocess reads credentials from
// disk, so heron-connect never needs to see or forward any tokens.
package codebuddy

import (
	"context"
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
	core.RegisterAgent("codebuddy", New)
}

// Agent drives CodeBuddy Code CLI using the headless stream-json mode:
// `codebuddy -p <prompt> --output-format stream-json --dangerously-skip-permissions`.
type Agent struct {
	workDir    string
	model      string // user-selected model (pending until the next session starts)
	mode       string // "default" | "yolo" (--dangerously-skip-permissions)
	args       []string
	sessionEnv []string
	// interruptible switches the session to a resident process driven over
	// stream-json stdin, enabling mid-turn interruption. Absent/false keeps
	// the historical per-turn spawn behaviour exactly.
	interruptible bool

	// discoveredModel caches the account's real active model as reported by
	// the CLI over ACP (see probe.go). Refreshed by AvailableModels;
	// GetModel reads the cached value so the hot paths that render the
	// footer/status card stay O(1) and never spawn a probe.
	discoveredModel string

	mu sync.Mutex
}

func New(opts map[string]any) (core.Agent, error) {
	workDir, _ := opts["work_dir"].(string)
	if workDir == "" {
		workDir = "."
	}
	model, _ := opts["model"].(string)
	mode, _ := opts["mode"].(string)
	mode = normalizeMode(mode)
	args := parseStringSlice(opts["args"])
	interruptible, _ := opts["interruptible"].(bool)

	if _, err := exec.LookPath("codebuddy"); err != nil {
		return nil, fmt.Errorf("codebuddy: 'codebuddy' not found in PATH, install with: npm install -g @tencent-ai/codebuddy-code")
	}

	return &Agent{
		workDir:       workDir,
		model:         model,
		mode:          mode,
		args:          args,
		interruptible: interruptible,
	}, nil
}

// parseStringSlice normalises an "args" config value ([]string or []any)
// into a []string, mirroring agent/acp's helper so extra CLI arguments can
// be passed through to the spawned codebuddy process.
func parseStringSlice(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			switch t := e.(type) {
			case string:
				out = append(out, t)
			default:
				out = append(out, fmt.Sprint(t))
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yolo", "bypass", "dangerously-skip-permissions", "bypassPermissions":
		return "yolo"
	default:
		return "default"
	}
}

func (a *Agent) Name() string           { return "codebuddy" }
func (a *Agent) CLIBinaryName() string  { return "codebuddy" }
func (a *Agent) CLIDisplayName() string { return "CodeBuddy" }

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workDir = dir
	slog.Info("codebuddy: work_dir changed", "work_dir", dir)
}

func (a *Agent) GetWorkDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workDir
}

func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
	slog.Info("codebuddy: model changed", "model", model)
}

// GetModel returns the model the next session will use. A pending user
// selection wins; otherwise we report the model the CLI says is actually
// active for this account (captured by the last probe). Reporting the
// discovered model keeps us in sync with reality — the CLI can serve a
// different model than the one requested (e.g. server-side fallback), and
// echoing config alone would hide that.
func (a *Agent) GetModel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.model != "" {
		return a.model
	}
	return a.discoveredModel
}

// AvailableModels returns the models this account can actually run.
//
// The account's real capability lives behind the login and changes as the
// service adds or retires models, so a static list goes stale and silently
// misreports the current model (which breaks the /model picker highlight).
// We therefore discover it live from the CLI over ACP, then layer the
// user's own models.json on top:
//
//  1. probe the CLI for the account's model list (authoritative base)
//  2. merge custom entries from user/project models.json by id (override)
//  3. apply the models.json availableModels allow-list, if any
//  4. ensure the current model is present so the picker can highlight it
//
// Offline fallbacks, in order: models.json alone, then a small built-in
// list, so the picker is never empty.
func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	a.mu.Lock()
	workDir := a.workDir
	extraArgs := append([]string(nil), a.args...)
	extraEnv := append([]string(nil), a.sessionEnv...)
	a.mu.Unlock()

	current, discovered := probeCodeBuddyModels(ctx, "codebuddy", workDir, extraArgs, extraEnv)

	user, project, projectHasFile := core.LoadCodeBuddyModelsConfig(workDir)
	configuredEmpty := core.CodeBuddyModelsConfigEmpty(user, project)

	var models []core.ModelOption
	switch {
	case len(discovered) > 0:
		// CLI is authoritative; models.json customises on top.
		models = core.MergeCodeBuddyModels(user, project, projectHasFile, discovered)
	case !configuredEmpty:
		models = core.MergeCodeBuddyModels(user, project, projectHasFile)
	default:
		models = codeBuddyFallbackModels()
	}

	// Refresh the cached discovery so GetModel can answer without probing.
	a.mu.Lock()
	if current != "" {
		a.discoveredModel = current
	}
	a.mu.Unlock()

	return ensureModelPresent(models, a.GetModel())
}

// ensureModelPresent guarantees the effective current model appears in the
// list, so the /model picker always has a row to highlight. A model that
// the CLI didn't advertise (custom endpoint, or one retired from the list)
// is prepended rather than dropped.
func ensureModelPresent(models []core.ModelOption, current string) []core.ModelOption {
	if current == "" {
		return models
	}
	for _, m := range models {
		if m.Name == current {
			return models
		}
	}
	return append([]core.ModelOption{{Name: current}}, models...)
}

// codeBuddyFallbackModels is used only when both the live probe and
// models.json yield nothing (e.g. CLI offline). It is a last resort so the
// picker is not empty, not a source of truth.
func codeBuddyFallbackModels() []core.ModelOption {
	return []core.ModelOption{
		{Name: "claude-sonnet-5", Desc: "Claude Sonnet 5"},
		{Name: "claude-sonnet-4-6", Desc: "Claude Sonnet 4.6"},
		{Name: "claude-opus-4-8", Desc: "Claude Opus 4.8"},
		{Name: "gemini-3.1-pro-preview", Desc: "Gemini 3.1 Pro Preview"},
		{Name: "gpt-5.4", Desc: "GPT-5.4"},
		{Name: "deepseek-v4-pro", Desc: "DeepSeek V4 Pro"},
	}
}

func (a *Agent) SetSessionEnv(env []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionEnv = env
}

func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.Lock()
	mode := a.mode
	model := a.model
	args := append([]string{}, a.args...)
	extraEnv := append([]string{}, a.sessionEnv...)
	interruptible := a.interruptible
	a.mu.Unlock()

	return newCodeBuddySession(ctx, a.workDir, model, mode, sessionID, args, extraEnv, interruptible)
}

func (a *Agent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}

func (a *Agent) Stop() error { return nil }

// ── ModeSwitcher ─────────────────────────────────────────────

func (a *Agent) SetMode(mode string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = normalizeMode(mode)
	slog.Info("codebuddy: mode changed", "mode", a.mode)
}

func (a *Agent) GetMode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: "default", Name: "Default", NameZh: "默认", Desc: "Standard permissions", DescZh: "标准权限模式"},
		{Key: "yolo", Name: "YOLO", NameZh: "全自动", Desc: "Skip all permission checks (--dangerously-skip-permissions)", DescZh: "跳过所有权限检查"},
	}
}

// ── SkillProvider ────────────────────────────────────────────

func (a *Agent) SkillDirs() []string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	dirs := []string{filepath.Join(absDir, ".codebuddy", "skills")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".codebuddy", "skills"))
	}
	return dirs
}

// ── CommandProvider ──────────────────────────────────────────

// CommandDirs implements core.CommandProvider by pointing at CodeBuddy
// Code's own custom-command directories (<workDir>/.codebuddy/commands
// and ~/.codebuddy/commands), mirroring the SkillDirs convention above so
// commands defined there (e.g. via CodeBuddy Code's own /commands
// workflow) also surface as heron-connect slash commands.
func (a *Agent) CommandDirs() []string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	dirs := []string{filepath.Join(absDir, ".codebuddy", "commands")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".codebuddy", "commands"))
	}
	return dirs
}

// ── ContextCompressor ────────────────────────────────────────

func (a *Agent) CompressCommand() string { return "/compact" }

// ── MemoryFileProvider ───────────────────────────────────────

func (a *Agent) ProjectMemoryFile() string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	// CodeBuddy reads CODEBUDDY.md or AGENTS.md as project memory
	return filepath.Join(absDir, "AGENTS.md")
}

func (a *Agent) GlobalMemoryFile() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".codebuddy", "CODEBUDDY.md")
}
