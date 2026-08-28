package core

// engine_setup.go — post-construction configuration setters and related helpers.
//
// Covers:
//   - SetMultiWorkspace, SetWorkspaceIdleTimeout, runIdleReaper, reapIdleWorkspaces
//   - SetHooks, SetSpeechConfig, SetTTSConfig, SetTTSSaveFunc
//   - SetDisplayConfig, SetInstantReply, SetReferenceConfig
//   - estimateTokens, estimateTokensWithPendingAssistant
//   - SetAutoCompressConfig, SetResetOnIdle
//   - SetShowContextIndicator, SetReplyFooterEnabled, SetFilterExternalSessions
//   - SetWebSetupFunc, SetWebStatusFunc, SetInjectSender, SetAttachmentSendEnabled
//   - SetObserveConfig, SetLanguageSaveFunc, findObserverTarget
//   - SetProvider* funcs, SetModelSaveFunc, AddPlatform
//   - SetCronScheduler, SetHeartbeatScheduler
//   - SetCommandSave*, SetDisplaySaveFunc, ConfigReloadResult, SetConfigReloadFunc
//   - GetAgent, GetSessions
//   - AddCommand, ClearCommands, RemoveCommand, AddAlias, ClearAliases, SetAlias*
//   - resolveDisabledCmds, GetDisabledCommands, SetDisabledCommands
//   - SetUserRoles, SetAdminFrom, privilegedCommands, isAdmin
//   - SetBannedWords, SetRateLimitCfg, SetOutgoingRateLimitCfg, checkRateLimit
//   - SetStreamPreviewCfg, SetEventIdleTimeout, SetMaxQueuedMessages
//   - SetRelayManager, RelayManager, SetDirHistory, SetBaseWorkDir, SetProjectStateStore
//   - ProjectName, ListSkills, SkillDirs, AgentTypeName, ActiveSessionKeys
//
// All methods remain func (e *Engine) receivers in package core.

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// DefaultWorkspaceIdleTimeout is the default time a workspace can be idle
// before the reaper reclaims it.
const DefaultWorkspaceIdleTimeout = 15 * time.Minute

// SetMultiWorkspace enables multi-workspace mode for the engine.
func (e *Engine) SetMultiWorkspace(baseDir, bindingStorePath string) {
	e.multiWorkspace = true
	e.baseDir = baseDir
	e.workspaceBindings = NewWorkspaceBindingManager(bindingStorePath)
	e.workspacePool = newWorkspacePool(DefaultWorkspaceIdleTimeout)
	e.initFlows = make(map[string]*workspaceInitFlow)
	go e.runIdleReaper()
}

// SetWorkspaceIdleTimeout overrides the workspace idle reaper timeout.
// Must be called after SetMultiWorkspace. A zero value disables reaping.
func (e *Engine) SetWorkspaceIdleTimeout(d time.Duration) {
	if e.workspacePool != nil {
		e.workspacePool.mu.Lock()
		e.workspacePool.idleTimeout = d
		e.workspacePool.mu.Unlock()
	}
}

func (e *Engine) runIdleReaper() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.reapIdleWorkspaces()
		}
	}
}

func (e *Engine) reapIdleWorkspaces() {
	if e.workspacePool == nil {
		return
	}

	reaped := e.workspacePool.ReapIdle()
	if len(reaped) == 0 {
		return
	}

	reapedSet := make(map[string]struct{}, len(reaped))
	for _, ws := range reaped {
		reapedSet[ws] = struct{}{}
	}

	type cleanupTarget struct {
		key   string
		state *interactiveState
	}

	var targets []cleanupTarget
	e.interactiveMu.Lock()
	for key, state := range e.interactiveStates {
		if _, ok := reapedSet[state.workspaceDir]; ok {
			targets = append(targets, cleanupTarget{key: key, state: state})
		}
	}
	e.interactiveMu.Unlock()

	for _, target := range targets {
		e.cleanupInteractiveState(target.key, target.state)
	}
	for _, ws := range reaped {
		slog.Info("workspace idle-reaped", "workspace", ws)
	}
}

// SetHooks configures the lifecycle event hook manager.
func (e *Engine) SetHooks(hm *HookManager) {
	e.hooks = hm
}

func (e *Engine) SetSpeechConfig(cfg SpeechCfg) {
	e.speech = cfg
}

// SetTTSConfig configures the text-to-speech subsystem.
func (e *Engine) SetTTSConfig(cfg *TTSCfg) {
	e.tts = cfg
}

// SetTTSSaveFunc registers a callback that persists TTS mode changes.
func (e *Engine) SetTTSSaveFunc(fn func(mode string) error) {
	e.ttsSaveFunc = fn
}

// SetDisplayConfig overrides the default truncation settings.
func (e *Engine) SetDisplayConfig(cfg DisplayCfg) {
	e.display = cfg
}

// SetPlatformDisplayOverrides registers per-platform display overrides,
// keyed by platform name (matched case-insensitively against msg.Platform /
// [[projects.platforms]].type at resolution time — see
// resolveDisplayForPlatform). Call after SetDisplayConfig at startup and on
// every config reload. Passing nil or an empty map clears all overrides
// (every platform falls back to the engine default display), preserving
// full backward compatibility for projects that don't use
// [projects.platforms.display] blocks.
func (e *Engine) SetPlatformDisplayOverrides(overrides map[string]DisplayCfg) {
	e.platformDisplayOverrides = overrides
}

// resolveDisplayForPlatform returns the effective DisplayCfg for the given
// platform name (case-insensitive), falling back to the engine default
// e.display when platformName is empty or has no registered override. Cheap
// map lookup; safe to call once per turn from processInteractiveEvents.
func (e *Engine) resolveDisplayForPlatform(platformName string) DisplayCfg {
	if platformName == "" || len(e.platformDisplayOverrides) == 0 {
		return e.display
	}
	if cfg, ok := e.platformDisplayOverrides[strings.ToLower(strings.TrimSpace(platformName))]; ok {
		return cfg
	}
	return e.display
}

// SetInstantReply configures the immediate confirmation reply.
func (e *Engine) SetInstantReply(cfg InstantReplyCfg) {
	e.instantReply = cfg
}

// SetReferenceConfig configures local reference normalization/rendering.
func (e *Engine) SetReferenceConfig(cfg ReferenceRenderCfg) {
	e.references = normalizeReferenceRenderCfg(cfg)
}

// estimateTokens provides a rough token estimate for a set of history entries.
func estimateTokens(entries []HistoryEntry) int {
	return estimateTokensWithPendingAssistant(entries, "")
}

// estimateTokensWithPendingAssistant is like estimateTokens but includes an assistant
// message not yet written to history (used at EventResult before AddHistory).
func estimateTokensWithPendingAssistant(entries []HistoryEntry, pendingAssistant string) int {
	// Heuristic: ~1 token per 4 characters in mixed English/Chinese.
	count := 0
	for _, h := range entries {
		count += len([]rune(h.Content))
	}
	if pendingAssistant != "" {
		count += len([]rune(pendingAssistant))
	}
	if count == 0 {
		return 0
	}
	return (count + 3) / 4
}

// SetAutoCompressConfig configures automatic context compression.
func (e *Engine) SetAutoCompressConfig(enabled bool, maxTokens int, minGap time.Duration) {
	e.autoCompressEnabled = enabled
	e.autoCompressMaxTokens = maxTokens
	if minGap <= 0 {
		minGap = 30 * time.Minute
	}
	e.autoCompressMinGap = minGap
}

// SetResetOnIdle configures automatic session rotation after prolonged inactivity.
// A zero or negative duration disables the behavior.
func (e *Engine) SetResetOnIdle(d time.Duration) {
	if d <= 0 {
		e.resetOnIdle = 0
		return
	}
	e.resetOnIdle = d
}

// SetPlatformResetOnIdleOverrides registers per-platform reset-on-idle
// overrides, keyed by lowercase platform name/type (e.g. "web", "wecom").
// resolveResetOnIdleForPlatform falls back to the engine-wide value for any
// platform not in the map, so entries only need to be present where an override
// is desired. Call after SetResetOnIdle at startup and on hot-reload. A zero
// entry disables rotation for that platform. Pass nil/empty to clear all
// overrides.
func (e *Engine) SetPlatformResetOnIdleOverrides(overrides map[string]time.Duration) {
	e.resetOnIdleByPlatform = overrides
}

// resolveResetOnIdleForPlatform returns the effective reset-on-idle duration
// for the given platform, applying a per-platform override when present.
func (e *Engine) resolveResetOnIdleForPlatform(platformName string) time.Duration {
	if platformName == "" || len(e.resetOnIdleByPlatform) == 0 {
		return e.resetOnIdle
	}
	key := strings.ToLower(strings.TrimSpace(platformName))
	if d, ok := e.resetOnIdleByPlatform[key]; ok {
		return d
	}
	return e.resetOnIdle
}

// SetShowContextIndicator controls whether assistant replies include the [ctx: ~N%] suffix.
func (e *Engine) SetShowContextIndicator(show bool) {
	e.showContextIndicator = show
}

// SetReplyFooterEnabled controls whether assistant replies include a Codex-like
// footer line with model / reasoning / usage / workdir metadata when available.
func (e *Engine) SetReplyFooterEnabled(show bool) {
	e.replyFooterEnabled = show
}

// SetFilterExternalSessions controls whether /list, /switch, /delete, etc.
// hide sessions created by direct CLI usage in the same work_dir.
// Default false = show all sessions from the agent.
func (e *Engine) SetFilterExternalSessions(v bool) {
	e.filterExternalSessions = v
}

// SetAutoSessionTitle controls whether placeholder-named sessions get an
// auto-generated title after their first completed turn. The project config
// default is true; the engine zero value is false so unwired tests keep the
// legacy behavior.
func (e *Engine) SetAutoSessionTitle(v bool) {
	e.autoSessionTitle = v
}

// SetQueuedMessagesMode controls how messages queued while the agent is busy
// are submitted after the current turn completes: "merge" (default, empty
// string) combines the whole queue into one prompt and one turn;
// "serial" submits one message per turn (historical behavior).
func (e *Engine) SetQueuedMessagesMode(mode string) {
	e.queuedMessagesMerge = strings.ToLower(strings.TrimSpace(mode)) != "serial"
}

func (e *Engine) SetWebSetupFunc(fn func() (int, string, bool, error)) { e.webSetupFunc = fn }
func (e *Engine) SetWebStatusFunc(fn func() string)                    { e.webStatusFunc = fn }

// SetInjectSender controls whether sender identity (platform and user ID) is
// prepended to each message before forwarding it to the agent. When enabled,
// the agent receives a preamble line like:
//
//	[heron-connect sender_id=ou_abc123 platform=feishu]
//
// This allows the agent to identify who sent the message and adjust behavior
// accordingly (e.g. personal task views, role-based access control).
func (e *Engine) SetInjectSender(v bool) {
	e.injectSender = v
}

// SetAttachmentSendEnabled controls whether side-channel image/file delivery is allowed.
func (e *Engine) SetAttachmentSendEnabled(v bool) {
	e.attachmentSendEnabled = v
}

// SetObserveConfig enables terminal session observation.
// projectDir is the Claude Code project directory containing session JSONL files.
// sessionKey identifies the Slack channel to forward messages to.
func (e *Engine) SetObserveConfig(projectDir, sessionKey string) {
	e.observeEnabled = true
	e.observeProjectDir = projectDir
	e.observeSessionKey = sessionKey
}

func (e *Engine) SetLanguageSaveFunc(fn func(Language) error) {
	e.i18n.SetSaveFunc(fn)
}

// findObserverTarget returns the first platform that implements ObserverTarget,
// or nil if none do.
func (e *Engine) findObserverTarget() ObserverTarget {
	for _, p := range e.platforms {
		if ot, ok := p.(ObserverTarget); ok {
			return ot
		}
	}
	return nil
}

func (e *Engine) SetProviderSaveFunc(fn func(providerName string) error) {
	e.providerSaveFunc = fn
}

func (e *Engine) SetProviderAddSaveFunc(fn func(ProviderConfig) error) {
	e.providerAddSaveFunc = fn
}

func (e *Engine) SetProviderRemoveSaveFunc(fn func(string) error) {
	e.providerRemoveSaveFunc = fn
}

func (e *Engine) SetProviderModelSaveFunc(fn func(providerName, model string) error) {
	e.providerModelSaveFunc = fn
}

func (e *Engine) SetProviderRefsSaveFunc(fn func(refs []string) error) {
	e.providerRefsSaveFunc = fn
}

func (e *Engine) SetListGlobalProvidersFunc(fn func(agentType string) ([]ProviderConfig, error)) {
	e.listGlobalProvidersFunc = fn
}

func (e *Engine) SetModelSaveFunc(fn func(model string) error) {
	e.modelSaveFunc = fn
}

// AddPlatform appends a platform to the engine after construction.
// The platform is started and wired during the next Engine.Start call,
// or if the engine is already running, it is started immediately.
func (e *Engine) AddPlatform(p Platform) {
	e.platforms = append(e.platforms, p)
}

func (e *Engine) SetCronScheduler(cs *CronScheduler) {
	e.cronScheduler = cs
}

func (e *Engine) SetHeartbeatScheduler(hs *HeartbeatScheduler) {
	e.heartbeatScheduler = hs
}

func (e *Engine) SetCommandSaveAddFunc(fn func(name, description, prompt, exec, workDir string) error) {
	e.commandSaveAddFunc = fn
}

func (e *Engine) SetCommandSaveDelFunc(fn func(name string) error) {
	e.commandSaveDelFunc = fn
}

func (e *Engine) SetDisplaySaveFunc(fn func(mode *string, thinkingMessages *bool, thinkingMaxLen, toolMaxLen *int, toolMessages *bool) error) {
	e.displaySaveFunc = fn
}

// ConfigReloadResult describes what was updated by a config reload.
type ConfigReloadResult struct {
	DisplayUpdated   bool
	ProvidersUpdated int
	CommandsUpdated  int
}

func (e *Engine) SetConfigReloadFunc(fn func() (*ConfigReloadResult, error)) {
	e.configReloadFunc = fn
}

// GetAgent returns the engine's agent (for type assertions like ProviderSwitcher).
func (e *Engine) GetAgent() Agent {
	return e.agent
}

// GetSessions returns the Engine's session manager (for testing).
func (e *Engine) GetSessions() *SessionManager {
	return e.sessions
}

// AddCommand registers a custom slash command.
func (e *Engine) AddCommand(name, description, prompt, exec, workDir, source string) {
	e.commands.Add(name, description, prompt, exec, workDir, source)
}

// ClearCommands removes all commands from the given source.
func (e *Engine) ClearCommands(source string) {
	e.commands.ClearSource(source)
}

// AddAlias registers a command alias.
func (e *Engine) AddAlias(name, command string) {
	e.aliasMu.Lock()
	defer e.aliasMu.Unlock()
	e.aliases[name] = command
}

func (e *Engine) SetAliasSaveAddFunc(fn func(name, command string) error) {
	e.aliasSaveAddFunc = fn
}

func (e *Engine) SetAliasSaveDelFunc(fn func(name string) error) {
	e.aliasSaveDelFunc = fn
}

// ClearAliases removes all aliases (for config reload).
func (e *Engine) ClearAliases() {
	e.aliasMu.Lock()
	defer e.aliasMu.Unlock()
	e.aliases = make(map[string]string)
}

// resolveDisabledCmds resolves a list of command names (including "*" wildcard)
// to a set of canonical command IDs.
func resolveDisabledCmds(cmds []string) map[string]bool {
	m := make(map[string]bool, len(cmds))
	for _, c := range cmds {
		c = strings.ToLower(strings.TrimPrefix(c, "/"))
		if c == "*" {
			for _, bc := range builtinCommands {
				m[bc.id] = true
			}
			return m
		}
		if id := matchPrefix(c, builtinCommands); id != "" {
			m[id] = true
		} else {
			m[c] = true
		}
	}
	return m
}

// GetDisabledCommands returns the list of disabled command IDs for this project.
func (e *Engine) GetDisabledCommands() []string {
	e.userRolesMu.RLock()
	defer e.userRolesMu.RUnlock()
	out := make([]string, 0, len(e.disabledCmds))
	for k := range e.disabledCmds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SetDisabledCommands sets the list of command IDs that are disabled for this project.
func (e *Engine) SetDisabledCommands(cmds []string) {
	e.userRolesMu.Lock()
	defer e.userRolesMu.Unlock()
	e.disabledCmds = resolveDisabledCmds(cmds)
}

// SetUserRoles configures per-user role-based policies. Pass nil to disable.
func (e *Engine) SetUserRoles(urm *UserRoleManager) {
	e.userRolesMu.Lock()
	defer e.userRolesMu.Unlock()
	if e.userRoles != nil {
		e.userRoles.Stop()
	}
	e.userRoles = urm
}

// SetAdminFrom sets the admin allowlist for privileged commands.
// "*" means all users who pass allow_from are admins.
// Empty string means privileged commands are denied for everyone.
func (e *Engine) SetAdminFrom(adminFrom string) {
	e.userRolesMu.Lock()
	e.adminFrom = strings.TrimSpace(adminFrom)
	af := e.adminFrom
	shellDisabled := e.disabledCmds["shell"]
	e.userRolesMu.Unlock()
	if af == "" && !shellDisabled {
		slog.Warn("admin_from is not set — privileged commands (/shell, /show, /dir, /restart, /upgrade) are blocked. "+
			"Set admin_from in config to enable them, or use disabled_commands to hide them.",
			"project", e.name)
	}
}

// privilegedCommands are commands that require admin_from authorization.
var privilegedCommands = map[string]bool{
	"shell":   true,
	"show":    true,
	"dir":     true,
	"restart": true,
	"upgrade": true,
	"web":     true,
	"diff":    true,
}

// isAdmin checks whether the given user ID is authorized for privileged commands.
// Unlike AllowList, empty adminFrom means deny-all (fail-closed).
func (e *Engine) isAdmin(userID string) bool {
	e.userRolesMu.RLock()
	af := e.adminFrom
	e.userRolesMu.RUnlock()
	if af == "" {
		return false
	}
	if af == "*" {
		return true
	}
	for _, id := range strings.Split(af, ",") {
		if strings.EqualFold(strings.TrimSpace(id), userID) {
			return true
		}
	}
	return false
}

// SetBannedWords replaces the banned words list.
func (e *Engine) SetBannedWords(words []string) {
	e.bannedMu.Lock()
	defer e.bannedMu.Unlock()
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	e.bannedWords = lower
}

// SetRateLimitCfg configures per-session message rate limiting.
// It stops the previous rate limiter's background goroutine before replacing it.
func (e *Engine) SetRateLimitCfg(cfg RateLimitCfg) {
	if e.rateLimiter != nil {
		e.rateLimiter.Stop()
	}
	e.rateLimiter = NewRateLimiter(cfg.MaxMessages, cfg.Window)
}

// SetOutgoingRateLimitCfg configures per-platform outgoing message throttling.
func (e *Engine) SetOutgoingRateLimitCfg(defaults OutgoingRateLimitCfg, overrides map[string]OutgoingRateLimitCfg) {
	e.outgoingRL = NewOutgoingRateLimiter(defaults, overrides)
}

// checkRateLimit returns true if the message is allowed, false if rate-limited.
// It checks per-user role-based limits first, then falls back to the global limiter.
func (e *Engine) checkRateLimit(msg *Message) bool {
	e.userRolesMu.RLock()
	urm := e.userRoles
	e.userRolesMu.RUnlock()

	// Try role-specific rate limit first
	if urm != nil {
		// Use userID if available, else fall back to sessionKey for unidentified users.
		// NOTE: sessionKey fallback means anonymous users get separate buckets per
		// session, which is less strict than per-user limiting. Platforms should
		// provide UserID for effective rate limiting.
		rateKey := msg.UserID
		if rateKey == "" {
			rateKey = msg.SessionKey
			slog.Debug("rate limit: no UserID, falling back to sessionKey", "session_key", msg.SessionKey)
		}
		allowed, handled := urm.AllowRate(rateKey)
		if handled {
			return allowed
		}
		// Role has no rate_limit config — fall through to global, keyed by user
	}
	// Global rate limiter
	if e.rateLimiter == nil {
		return true
	}
	// When users config active: key by userID (per-user); otherwise sessionKey (legacy)
	key := msg.SessionKey
	if urm != nil && msg.UserID != "" {
		key = msg.UserID
	}
	return e.rateLimiter.Allow(key)
}

// SetStreamPreviewCfg configures the streaming preview behavior.
func (e *Engine) SetStreamPreviewCfg(cfg StreamPreviewCfg) {
	e.streamPreview = cfg
}

// SetEventIdleTimeout sets the maximum time to wait between consecutive agent events.
// 0 disables the timeout entirely.
func (e *Engine) SetEventIdleTimeout(d time.Duration) {
	e.eventIdleTimeout = d
}

// SetMaxQueuedMessages sets the per-session message queue depth.
// Values <= 0 are ignored.
func (e *Engine) SetMaxQueuedMessages(n int) {
	if n > 0 {
		e.maxQueuedMessages = n
	}
}

func (e *Engine) SetRelayManager(rm RelayManagerAPI) {
	e.relayManager = rm
}

func (e *Engine) RelayManager() RelayManagerAPI {
	return e.relayManager
}

func (e *Engine) SetDirHistory(dh *DirHistory) {
	e.dirHistory = dh
}

func (e *Engine) SetBaseWorkDir(dir string) {
	e.baseWorkDir = dir
}

func (e *Engine) SetProjectStateStore(store *ProjectStateStore) {
	e.projectState = store
}

// RemoveCommand removes a custom command by name. Returns false if not found.
func (e *Engine) RemoveCommand(name string) bool {
	return e.commands.Remove(name)
}

func (e *Engine) ProjectName() string {
	return e.name
}

// ListSkills returns all discovered skills for this engine's project.
func (e *Engine) ListSkills() []*Skill {
	return e.skills.ListAll()
}

// SkillDirs returns the configured skill directories for this engine.
func (e *Engine) SkillDirs() []string {
	return e.skills.Dirs()
}

// AgentTypeName returns the agent type name (e.g. "claudecode", "codex").
func (e *Engine) AgentTypeName() string {
	if e.agent != nil {
		return e.agent.Name()
	}
	return ""
}

// ActiveSessionKeys returns the session keys of all active interactive sessions.
func (e *Engine) ActiveSessionKeys() []string {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()
	var keys []string
	for key, state := range e.interactiveStates {
		if state.platform != nil {
			keys = append(keys, key)
		}
	}
	return keys
}

// ── Public accessors for management package ──────────────────────────────────

// Platforms returns a snapshot of the platforms registered with this engine.
func (e *Engine) Platforms() []Platform {
	out := make([]Platform, len(e.platforms))
	copy(out, e.platforms)
	return out
}

// I18n returns the engine's i18n instance.
func (e *Engine) I18n() *I18n {
	return e.i18n
}

// GetAdminFrom returns the configured admin_from value under a read lock.
func (e *Engine) GetAdminFrom() string {
	e.userRolesMu.RLock()
	defer e.userRolesMu.RUnlock()
	return e.adminFrom
}

// GetUserRoleManager returns the active UserRoleManager under a read lock.
func (e *Engine) GetUserRoleManager() *UserRoleManager {
	e.userRolesMu.RLock()
	defer e.userRolesMu.RUnlock()
	return e.userRoles
}

// AllInteractiveSessionKeys returns all keys present in the interactive session map.
func (e *Engine) AllInteractiveSessionKeys() []string {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()
	keys := make([]string, 0, len(e.interactiveStates))
	for k := range e.interactiveStates {
		keys = append(keys, k)
	}
	return keys
}

// InteractiveSessionPlatformMap returns a map of sessionKey → platform name
// for all currently active interactive sessions.
func (e *Engine) InteractiveSessionPlatformMap() map[string]string {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()
	m := make(map[string]string, len(e.interactiveStates))
	for key, state := range e.interactiveStates {
		pName := ""
		if state.platform != nil {
			pName = state.platform.Name()
		}
		m[key] = pName
	}
	return m
}

// IsSessionLive reports whether the given session key has an active interactive state.
func (e *Engine) IsSessionLive(sessionKey string) bool {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()
	_, ok := e.interactiveStates[sessionKey]
	return ok
}

// SessionTurnState describes whether the agent is actively working for a
// session, exposed to the management API so the Web admin UI can surface
// per-session execution status (multiple sessions may run in parallel).
type SessionTurnState struct {
	// Running is true while a foreground turn is in progress (cancelCh set)
	// or the unsolicited background reader is consuming agent events.
	Running bool
	// WaitingPermission is true while the turn is blocked on a user
	// permission/AskUserQuestion response.
	WaitingPermission bool
	// TurnStartedAt is when the current foreground turn began (zero when no
	// foreground turn has started in this interactive state).
	TurnStartedAt time.Time
}

// InteractiveSessionTurnStates returns the per-session-key turn state for all
// live interactive states. Lock order: interactiveMu is taken first to snapshot
// the state map, then each state.mu is taken individually after release —
// never nested, so this cannot deadlock against paths that take state.mu first.
func (e *Engine) InteractiveSessionTurnStates() map[string]SessionTurnState {
	e.interactiveMu.Lock()
	states := make([]*interactiveState, 0, len(e.interactiveStates))
	keys := make([]string, 0, len(e.interactiveStates))
	for key, state := range e.interactiveStates {
		keys = append(keys, key)
		states = append(states, state)
	}
	e.interactiveMu.Unlock()

	m := make(map[string]SessionTurnState, len(states))
	for i, state := range states {
		state.mu.Lock()
		m[keys[i]] = SessionTurnState{
			Running:           state.cancelCh != nil || state.unsolicitedCancel != nil,
			WaitingPermission: state.pending != nil,
			TurnStartedAt:     state.turnStartTime,
		}
		state.mu.Unlock()
	}
	return m
}

// ReloadConfig invokes the registered config-reload callback.
// Returns an error if no callback is configured.
func (e *Engine) ReloadConfig() (*ConfigReloadResult, error) {
	if e.configReloadFunc == nil {
		return nil, nil
	}
	return e.configReloadFunc()
}

// ResetAllSessions is a public wrapper for the internal resetAllSessions method.
func (e *Engine) ResetAllSessions() {
	e.resetAllSessions()
}

// SwitchModel is a public wrapper for the internal switchModel method.
func (e *Engine) SwitchModel(target string) (string, error) {
	return e.switchModel(target)
}

// ApplyProviderSave calls the registered provider-save callback.
func (e *Engine) ApplyProviderSave(name string) error {
	if e.providerSaveFunc == nil {
		return nil
	}
	return e.providerSaveFunc(name)
}

// ApplyProviderRemoveSave calls the registered provider-remove callback.
func (e *Engine) ApplyProviderRemoveSave(name string) error {
	if e.providerRemoveSaveFunc == nil {
		return nil
	}
	return e.providerRemoveSaveFunc(name)
}

// ApplyProviderAddSave calls the registered provider-add callback.
func (e *Engine) ApplyProviderAddSave(cfg ProviderConfig) error {
	if e.providerAddSaveFunc == nil {
		return nil
	}
	return e.providerAddSaveFunc(cfg)
}

// SetSessions replaces the engine's session manager. Intended for test setup only.
func (e *Engine) SetSessions(sm *SessionManager) {
	e.sessions = sm
}

// ── Public Accessors for Package Extraction ────────────────────
//
// These methods expose private Engine fields and methods to enable
// extraction of core subsystems (ws_bridge_caps, relay, webhook, local_api)
// into separate packages while maintaining proper encapsulation.

// Commands returns the engine's command registry.
// Enables cross-package access to custom commands for bridge capabilities.
func (e *Engine) Commands() *CommandRegistry {
	return e.commands
}

// HandleRelayRequest processes a relay message synchronously and is the public
// interface to the private HandleRelay method. It starts or resumes a dedicated
// relay session, sends the message to the agent, and blocks until the complete
// response is collected (or the relay context times out).
// This method is exposed to enable the relay package to coordinate bot-to-bot messaging.
func (e *Engine) HandleRelayRequest(ctx context.Context, fromProject, chatID, message string) (string, error) {
	return e.HandleRelay(ctx, fromProject, chatID, message)
}

// SendMessage sends content to a platform via the private send() method.
// Enables cross-package access for webhooks and other external APIs that need
// to send messages through platforms.
func (e *Engine) SendMessage(p Platform, replyCtx any, content string) {
	e.send(p, replyCtx, content)
}

// ProcessInteractiveMessage processes an interactive message (button click, form submission)
// on a platform. It is the public interface to the private processInteractiveMessage method.
// This enables the webhook and local API packages to handle interactive events.
func (e *Engine) ProcessInteractiveMessage(p Platform, msg *Message, session *Session) {
	e.processInteractiveMessage(p, msg, session)
}

// HandleMessage is a public wrapper for the private handleMessage method.
// Its signature satisfies core.MessageHandler so the bridge package can pass it
// to BridgePlatform.Start without importing internal Engine state.
func (e *Engine) HandleMessage(p Platform, msg *Message) {
	e.handleMessage(p, msg)
}

// HandleCardNav is a public wrapper for the private handleCardNav method.
// Its signature satisfies core.CardNavigationHandler so the bridge package can
// pass engine.HandleCardNav to BridgePlatform.SetCardNavigationHandler.
func (e *Engine) HandleCardNav(action, sessionKey string) *Card {
	return e.handleCardNav(action, sessionKey)
}

// PublishedCommand is a command that a bridge control-plane client can safely
// expose as a slash command.
type PublishedCommand struct {
	Name              string
	Description       string
	Source            string
	RequiresWorkspace bool
	ArgsMode          string
}

const (
	publishedCommandArgsModeText  = "text"
	publishedCommandSourceBuiltin = "builtin"
	publishedCommandSourceCustom  = "custom"
)

// GetBridgePublishedCommands returns the subset of commands a bridge
// control-plane client can safely expose as slash commands. It intentionally
// excludes skills and other richer command models until the bridge protocol
// grows beyond the single free-form "args" text bucket.
func (e *Engine) GetBridgePublishedCommands() []PublishedCommand {
	e.userRolesMu.RLock()
	disabledCmds := e.disabledCmds
	e.userRolesMu.RUnlock()

	seen := make(map[string]bool)
	var commands []PublishedCommand

	for _, c := range builtinCommands {
		if len(c.names) == 0 || disabledCmds[c.id] {
			continue
		}
		if seen[c.id] {
			continue
		}
		seen[c.id] = true
		commands = append(commands, PublishedCommand{
			Name:              c.id,
			Description:       e.i18n.T(MsgKey(c.id)),
			Source:            publishedCommandSourceBuiltin,
			RequiresWorkspace: false,
			ArgsMode:          publishedCommandArgsModeText,
		})
	}

	customCommands := e.Commands().ListAll()
	sort.Slice(customCommands, func(i, j int) bool {
		return strings.ToLower(customCommands[i].Name) < strings.ToLower(customCommands[j].Name)
	})
	for _, c := range customCommands {
		lowerName := strings.ToLower(strings.TrimSpace(c.Name))
		if lowerName == "" || seen[lowerName] || disabledCmds[lowerName] {
			continue
		}
		seen[lowerName] = true

		desc := strings.TrimSpace(c.Description)
		if desc == "" {
			desc = "Custom command"
		}

		commands = append(commands, PublishedCommand{
			Name:              c.Name,
			Description:       desc,
			Source:            publishedCommandSourceCustom,
			RequiresWorkspace: false,
			ArgsMode:          publishedCommandArgsModeText,
		})
	}

	return commands
}
