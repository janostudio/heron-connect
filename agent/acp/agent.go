package acp

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/janostudio/heron-connect/core"
)

func init() {
	core.RegisterAgent("acp", New)
}

// Agent runs an ACP (Agent Client Protocol) agent subprocess over stdio JSON-RPC.
type Agent struct {
	workDir     string
	command     string
	args        []string
	staticEnv   map[string]string
	extraEnv    []string
	sessionEnv  []string
	authMethod  string // optional, e.g. "cursor_login" for Cursor CLI (see authenticate RPC)
	displayName string // optional, for doctor (default "ACP")

	// mode is the pending permission mode to apply to new sessions.
	// When set, StartSession applies it via session/set_mode right after
	// session/new. Empty means "use whatever the agent selects by default".
	mode string

	// model is the pending model id to apply to new sessions. When set,
	// StartSession applies it via session/set_model after session/new.
	// Empty means "use whatever the agent defaults to".
	model string

	// listUnsupported caches a negative result after we probe the agent
	// for sessionCapabilities.list once. Eliminates spawn cost on
	// subsequent `/ls` invocations against agents that don't implement
	// session/list (e.g. some Copilot/OpenClaw builds).
	listUnsupported atomic.Bool

	// modesCache holds the latest `modes` block we observed via
	// session/new or session/load. It's populated by the session
	// handshake so that future PermissionModes() calls can reflect the
	// actual modes this specific ACP agent offers (rather than a
	// hard-coded fallback that may not match).
	modesMu      sync.RWMutex
	modesCache   []core.PermissionModeInfo
	modesCurrent string

	// modelsCache holds the latest available models observed via
	// session/new or session/load (either from configOptions with
	// category "model" or from the legacy "models" field). Populated
	// by the session handshake so AvailableModels() can answer
	// without spawning a probe process.
	modelsMu        sync.RWMutex
	modelsCache     []core.ModelOption
	modelsCurrent   string

	// localSessions tracks sessions started by heron-connect so /list can
	// still return something useful when the ACP server does not
	// implement session/list (e.g. CodeBuddy). Entries are populated
	// on StartSession and updated when the first agent_message_chunk
	// arrives (auto-extracted title).
	localSessionsMu sync.RWMutex
	localSessions   map[string]*localSessionInfo

	mu sync.RWMutex
}

// localSessionInfo is the bookkeeping entry for the local session
// fallback used by ListSessions when session/list is unsupported.
type localSessionInfo struct {
	SessionID string
	CWD       string
	Title     string
	UpdatedAt time.Time
}

// sessionCallbacks lets a running acpSession report what it learned
// during the handshake back to its parent Agent. The session is owned
// by heron-connect's engine (not the agent), so without this the agent
// would never see availableModes / capability advertisements.
type sessionCallbacks interface {
	reportModes(block acpModesBlock)
	reportModels(current string, available []core.ModelOption)
	reportListSupported(supported bool)
	recordSessionStart(sessionID, cwd string)
	recordSessionTitle(sessionID, title string)
}

// Ensure *Agent satisfies sessionCallbacks at compile time.
var _ sessionCallbacks = (*Agent)(nil)

// New builds an acp agent from project options.
// Required: options["command"] — executable name or path for the ACP agent.
// Optional: options["args"], options["env"], options["auth_method"],
// options["display_name"], options["mode"].
func New(opts map[string]any) (core.Agent, error) {
	workDir, _ := opts["work_dir"].(string)
	if workDir == "" {
		workDir = "."
	}
	cmdStr, _ := opts["command"].(string)
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return nil, fmt.Errorf("acp: agent option \"command\" is required (path or name of the ACP agent binary)")
	}
	if _, err := exec.LookPath(cmdStr); err != nil {
		return nil, fmt.Errorf("acp: command %q not found in PATH: %w", cmdStr, err)
	}

	args := parseStringSlice(opts["args"])
	staticEnv := envMapFromOpts(opts)
	extra := envPairsFromOpts(opts)
	authMethod, _ := opts["auth_method"].(string)
	authMethod = strings.TrimSpace(authMethod)
	displayName, _ := opts["display_name"].(string)
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "ACP"
	}
	mode, _ := opts["mode"].(string)
	mode = strings.TrimSpace(mode)

	return &Agent{
		workDir:       workDir,
		command:       cmdStr,
		args:          args,
		staticEnv:     staticEnv,
		extraEnv:      extra,
		authMethod:    authMethod,
		displayName:   displayName,
		mode:          mode,
		localSessions: make(map[string]*localSessionInfo),
	}, nil
}

func envMapFromOpts(opts map[string]any) map[string]string {
	raw, ok := opts["env"]
	if !ok || raw == nil {
		return nil
	}
	switch m := raw.(type) {
	case map[string]string:
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = fmt.Sprint(v)
		}
		return out
	default:
		return nil
	}
}

func envPairsFromOpts(opts map[string]any) []string {
	raw, ok := opts["env"]
	if !ok || raw == nil {
		return nil
	}
	switch m := raw.(type) {
	case map[string]string:
		var out []string
		for k, v := range m {
			out = append(out, k+"="+v)
		}
		return out
	case map[string]any:
		var out []string
		for k, v := range m {
			out = append(out, fmt.Sprintf("%s=%v", k, v))
		}
		return out
	default:
		return nil
	}
}

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

func (a *Agent) Name() string { return "acp" }

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	a.workDir = dir
	a.mu.Unlock()
	slog.Info("acp: work_dir changed", "work_dir", dir)
}

func (a *Agent) GetWorkDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workDir
}

func (a *Agent) WorkspaceAgentOptions() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()

	opts := map[string]any{
		"command": a.command,
	}
	if len(a.args) > 0 {
		opts["args"] = append([]string(nil), a.args...)
	}
	if len(a.staticEnv) > 0 {
		env := make(map[string]string, len(a.staticEnv))
		for k, v := range a.staticEnv {
			env[k] = v
		}
		opts["env"] = env
	}
	if a.authMethod != "" {
		opts["auth_method"] = a.authMethod
	}
	if a.displayName != "" {
		opts["display_name"] = a.displayName
	}
	return opts
}

func (a *Agent) SetSessionEnv(env []string) {
	a.mu.Lock()
	a.sessionEnv = env
	a.mu.Unlock()
}

func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.RLock()
	command := a.command
	args := a.args
	workDir := a.workDir
	authMethod := a.authMethod
	pendingMode := a.mode
	pendingModel := a.model
	extra := append([]string(nil), a.extraEnv...)
	extra = append(extra, a.sessionEnv...)
	a.mu.RUnlock()

	return newACPSession(ctx, acpSessionConfig{
		command:         command,
		args:            args,
		extraEnv:        extra,
		workDir:         workDir,
		resumeSessionID: sessionID,
		authMethod:      authMethod,
		initialMode:     pendingMode,
		initialModel:    pendingModel,
		callbacks:       a,
	})
}

func (a *Agent) Stop() error { return nil }

// -- AgentDoctorInfo --

func (a *Agent) CLIBinaryName() string {
	a.mu.RLock()
	cmd := a.command
	a.mu.RUnlock()
	return filepath.Base(cmd)
}

func (a *Agent) CLIDisplayName() string {
	a.mu.RLock()
	n := a.displayName
	a.mu.RUnlock()
	if n == "" {
		return "ACP"
	}
	return n
}

// -- ModeSwitcher --
//
// heron-connect's engine treats ModeSwitcher as the point of truth for
// both displaying `/mode` options and applying a mode selection. For
// the generic ACP adapter we keep the Key == ACP modeId so downstream
// `session/set_mode` calls don't need any translation.

// SetMode stores a permission mode to apply to future sessions started
// via StartSession. If the caller-provided mode matches a known cached
// mode id (case-insensitive), it is normalised to that id. Otherwise
// it is stored as-is — some IM users may configure modes before the
// agent has started any session and thus advertised its mode list.
func (a *Agent) SetMode(mode string) {
	normalised := mode
	if m := a.matchModeID(mode); m != "" {
		normalised = m
	}
	a.mu.Lock()
	a.mode = normalised
	a.mu.Unlock()
	slog.Info("acp: mode changed for future sessions", "mode", normalised)
}

// GetMode returns the mode heron-connect will treat as "current" when
// rendering the `/mode` picker or applying SetLiveMode.
//
// Precedence: the most recent explicit SetMode wins (that's the user's
// intent — `/mode plan` should immediately be reflected in the next
// `/mode` listing even before the session/set_mode RPC has returned).
// Only if no one has ever called SetMode for this Agent do we fall
// back to whatever the server advertised as currentModeId during the
// last handshake.
func (a *Agent) GetMode() string {
	a.mu.RLock()
	pending := a.mode
	a.mu.RUnlock()
	if pending != "" {
		return pending
	}
	a.modesMu.RLock()
	defer a.modesMu.RUnlock()
	return a.modesCurrent
}

// PermissionModes returns the modes this ACP agent offers. The list is
// populated from the latest `modes.availableModes` observed on
// session/new or session/load; before the first successful handshake
// it returns an empty slice, and the engine will hide the mode picker.
//
// ACP doesn't send per-mode Desc/NameZh, so Description (if the server
// sent one) maps to Desc for both locales. IM-side translators are
// free to map well-known ids to localised strings later.
func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	a.modesMu.RLock()
	defer a.modesMu.RUnlock()
	out := make([]core.PermissionModeInfo, len(a.modesCache))
	copy(out, a.modesCache)
	return out
}

// matchModeID returns the canonical mode id for a user-typed string
// (case-insensitive match on id or display name). Empty string if no
// match or if we haven't observed modes yet.
func (a *Agent) matchModeID(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	lower := strings.ToLower(input)
	a.modesMu.RLock()
	defer a.modesMu.RUnlock()
	for _, m := range a.modesCache {
		if strings.ToLower(m.Key) == lower || strings.ToLower(m.Name) == lower {
			return m.Key
		}
	}
	return ""
}

// -- sessionCallbacks impl --

func (a *Agent) reportModes(block acpModesBlock) {
	infos := make([]core.PermissionModeInfo, 0, len(block.AvailableModes))
	for _, m := range block.AvailableModes {
		infos = append(infos, core.PermissionModeInfo{
			Key:    m.ID,
			Name:   m.Name,
			NameZh: m.Name,
			Desc:   m.Description,
			DescZh: m.Description,
		})
	}
	a.modesMu.Lock()
	a.modesCache = infos
	a.modesCurrent = block.CurrentModeID
	a.modesMu.Unlock()
}

func (a *Agent) reportListSupported(supported bool) {
	if !supported {
		a.listUnsupported.Store(true)
	} else {
		a.listUnsupported.Store(false)
	}
}

// recordSessionStart logs a session ID + cwd so /list can still return
// something useful when the ACP server does not implement
// session/list (e.g. CodeBuddy). Safe to call multiple times for the
// same id — only the UpdatedAt is refreshed.
func (a *Agent) recordSessionStart(sessionID, cwd string) {
	if sessionID == "" {
		return
	}
	a.localSessionsMu.Lock()
	defer a.localSessionsMu.Unlock()
	if a.localSessions == nil {
		a.localSessions = make(map[string]*localSessionInfo)
	}
	if info, ok := a.localSessions[sessionID]; ok {
		info.CWD = cwd
		info.UpdatedAt = time.Now()
		return
	}
	a.localSessions[sessionID] = &localSessionInfo{
		SessionID: sessionID,
		CWD:       cwd,
		UpdatedAt: time.Now(),
	}
}

// recordSessionTitle fills in an auto-extracted title for a session
// entry created by recordSessionStart. Called when the first
// agent_message_chunk arrives. No-op if the session is unknown to us
// (e.g. created externally) or the title is already set.
func (a *Agent) recordSessionTitle(sessionID, title string) {
	if sessionID == "" || title == "" {
		return
	}
	a.localSessionsMu.Lock()
	defer a.localSessionsMu.Unlock()
	if a.localSessions == nil {
		return
	}
	info, ok := a.localSessions[sessionID]
	if !ok || info.Title != "" {
		return
	}
	info.Title = title
	info.UpdatedAt = time.Now()
}

// listLocalSessions returns a copy of the locally tracked sessions,
// optionally filtered by cwd. Used as a fallback when the ACP server
// does not implement session/list.
func (a *Agent) listLocalSessions(cwdFilter string) []core.AgentSessionInfo {
	a.localSessionsMu.RLock()
	defer a.localSessionsMu.RUnlock()
	out := make([]core.AgentSessionInfo, 0, len(a.localSessions))
	for _, info := range a.localSessions {
		if cwdFilter != "" && info.CWD != "" &&
			!strings.EqualFold(filepath.Clean(info.CWD), filepath.Clean(cwdFilter)) {
			continue
		}
		out = append(out, core.AgentSessionInfo{
			ID:         info.SessionID,
			Summary:    info.Title,
			ModifiedAt: info.UpdatedAt,
		})
	}
	return out
}

// -- ModelSwitcher impl --

// SetModel stores a model id to apply to future sessions started via
// StartSession. The engine's switchModelOnAgent calls this, then
// closes the current session; the next StartSession will apply the
// new model via session/set_model after handshake.
func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	a.model = strings.TrimSpace(model)
	a.mu.Unlock()
	slog.Info("acp: model changed for future sessions", "model", model)
}

// GetModel returns the model heron-connect will treat as "current" for
// the next StartSession. Falls back to whatever the server advertised
// as currentModelId during the last handshake.
func (a *Agent) GetModel() string {
	a.mu.RLock()
	pending := a.model
	a.mu.RUnlock()
	if pending != "" {
		return pending
	}
	a.modelsMu.RLock()
	defer a.modelsMu.RUnlock()
	return a.modelsCurrent
}

// AvailableModels returns the models this ACP agent offers. The list
// is populated from the latest configOptions(models)/models observed
// on session/new or session/load. If the ACP server hasn't reported
// anything yet (no handshake yet, or the server doesn't advertise a
// model config option at all) and the underlying CLI is a known one
// with its own local model config, we fall back to reading that
// config directly — e.g. `codebuddy --acp` still reads
// .codebuddy/models.json even though ACP's session/new response may
// not surface it as a configOptions/models block.
func (a *Agent) AvailableModels(_ context.Context) []core.ModelOption {
	a.modelsMu.RLock()
	out := make([]core.ModelOption, len(a.modelsCache))
	copy(out, a.modelsCache)
	a.modelsMu.RUnlock()
	if len(out) > 0 {
		return out
	}

	if acpCLIBaseName(a.command) == "codebuddy" {
		a.mu.RLock()
		workDir := a.workDir
		a.mu.RUnlock()
		if models := core.CodeBuddyConfiguredModels(workDir); len(models) > 0 {
			return models
		}
	}
	return out
}

// reportModels caches the model list reported by the server in
// session/new or session/load responses so AvailableModels can answer
// without a probe.
func (a *Agent) reportModels(current string, available []core.ModelOption) {
	a.modelsMu.Lock()
	a.modelsCurrent = current
	if available != nil {
		a.modelsCache = append(a.modelsCache[:0], available...)
	}
	a.modelsMu.Unlock()
}
