package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// toolInputCacheMaxEntries caps toolInputByID growth; beyond this we evict
// roughly half the map (iteration order is arbitrary) to bound memory.
const toolInputCacheMaxEntries = 1000

type acpSession struct {
	workDir string
	events  chan core.Event
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	alive   atomic.Bool

	// closeOnce ensures s.events is closed exactly once, even if Close() is
	// called concurrently or multiple times (engine cleanup + Engine.Stop).
	// Closing the events channel is required so the engine's event loop can
	// detect session termination via the channelClosed path — without it,
	// a dead ACP session lingers in the interactiveStates map and its
	// subprocess is never reaped, leading to orphaned `codebuddy --acp`
	// processes (issue: orphaned --acp processes accumulating as PPID=1).
	closeOnce sync.Once

	cmd   *exec.Cmd
	tr    *transport
	stdin io.WriteCloser // retained for graceful shutdown (close stdin → agent exits)

	acpSessMu sync.RWMutex
	acpSessID string

	sendMu sync.Mutex

	permMu   sync.Mutex
	permByID map[string]permState

	toolInputMu   sync.Mutex
	toolInputByID map[string]string // toolCallId -> summarized tool input

	// modesMu guards availableModes and currentMode. Both fields are
	// populated on handshake (session/new or session/load response) and
	// updated whenever SetLiveMode succeeds or the server announces a
	// mode change via session/update.
	modesMu        sync.RWMutex
	availableModes []acpModeInfo
	currentMode    string

	// usageMu guards the latest usage_update snapshot received from
	// the server. Populated by maybeAbsorbUsageUpdate and read by
	// GetContextUsage to power the /usage command.
	usageMu       sync.RWMutex
	usageSnapshot *core.ContextUsage

	callbacks sessionCallbacks // may be nil (tests, integration harness)
}

type permState struct {
	RPCID   json.RawMessage
	Options []permissionOption
}

// acpSessionConfig bundles the inputs newACPSession needs. It's a
// struct rather than a long positional argument list because we keep
// adding optional knobs (initialMode, callbacks) and would otherwise
// break every call site each time.
type acpSessionConfig struct {
	command         string
	args            []string
	extraEnv        []string
	workDir         string
	resumeSessionID string
	authMethod      string
	initialMode     string           // if non-empty, applied via session/set_mode after session/new
	initialModel    string           // if non-empty, applied via session/set_model after session/new
	callbacks       sessionCallbacks // may be nil
}

func newACPSession(ctx context.Context, cfg acpSessionConfig) (*acpSession, error) {
	absWorkDir, err := filepath.Abs(cfg.workDir)
	if err != nil {
		absWorkDir = cfg.workDir
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	resumeID := cfg.resumeSessionID
	if resumeID == core.ContinueSession {
		resumeID = ""
	}
	s := &acpSession{
		workDir:       absWorkDir,
		events:        make(chan core.Event, 128),
		ctx:           sessionCtx,
		cancel:        cancel,
		permByID:      make(map[string]permState),
		toolInputByID: make(map[string]string),
		acpSessID:     resumeID,
		callbacks:     cfg.callbacks,
	}
	s.alive.Store(true)

	cmd := exec.CommandContext(sessionCtx, cfg.command, cfg.args...)
	cmd.Dir = absWorkDir
	cmd.Env = core.MergeEnv(os.Environ(), cfg.extraEnv)
	// Put the child in its own process group so we can kill the entire
	// group (including grandchildren the CLI may spawn) on shutdown.
	core.PrepareCmdForKill(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("acp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(&stderrBuf, os.Stderr)

	s.cmd = cmd
	s.tr = newTransport(stdout, stdin, s.onNotification, s.onServerRequest)
	s.stdin = stdin

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("acp: start %s: %w", cfg.command, err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.tr.readLoop(sessionCtx)
		waitErr := cmd.Wait()
		if waitErr != nil {
			msg := stderrBuf.String()
			if msg != "" {
				slog.Error("acp: process exited", "error", waitErr, "stderr", msg)
				s.emit(core.Event{Type: core.EventError, Error: fmt.Errorf("%s", strings.TrimSpace(msg))})
			} else {
				slog.Warn("acp: process exited", "error", waitErr)
			}
		} else {
			slog.Warn("acp: process exited cleanly", "session_id", s.currentACPSessionID())
		}
		s.alive.Store(false)
	}()

	if err := s.handshake(cfg.resumeSessionID, cfg.authMethod); err != nil {
		_ = s.Close()
		return nil, err
	}

	// Record this session for local /list fallback.
	if s.callbacks != nil {
		s.callbacks.recordSessionStart(s.currentACPSessionID(), s.workDir)
	}

	// Apply the agent-level mode preference now that we have a session
	// id. If set_mode fails (e.g. modeId unknown to this backend) we
	// log and carry on with whatever mode the server defaulted to —
	// the alternative would be to reject the session entirely, which
	// is worse UX for a non-critical control.
	if strings.TrimSpace(cfg.initialMode) != "" {
		if ok := s.SetLiveMode(cfg.initialMode); !ok {
			slog.Warn("acp: initial mode could not be applied",
				"mode", cfg.initialMode,
				"session_id", s.currentACPSessionID(),
			)
		}
	}

	// Apply the agent-level model preference. session/set_model is a
	// CodeBuddy extension (and an older ACP draft RPC) that changes
	// the active model on the running session. Failure is logged but
	// non-fatal — the server will keep using its default model.
	if strings.TrimSpace(cfg.initialModel) != "" {
		if err := s.SetModel(cfg.initialModel); err != nil {
			slog.Warn("acp: initial model could not be applied",
				"model", cfg.initialModel,
				"session_id", s.currentACPSessionID(),
				"error", err,
			)
		}
	}

	return s, nil
}

// handshake runs initialize → optional authenticate → session/load or
// session/new, and caches any modes/models the server advertises so
// SetLiveMode / PermissionModes / SetModel / AvailableModels can
// answer correctly.
func (s *acpSession) handshake(resumeSessionID string, authMethod string) error {
	initParams := map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
			"terminal": false,
		},
		"clientInfo": map[string]any{
			"name":    "cc-connect",
			"title":   "cc-connect",
			"version": "1.4.6",
		},
	}
	res, err := s.tr.call(s.ctx, "initialize", initParams)
	if err != nil {
		return fmt.Errorf("acp: initialize: %w", err)
	}

	var initOut acpInitializeResult
	if err := json.Unmarshal(res, &initOut); err != nil {
		return fmt.Errorf("acp: parse initialize result: %w", err)
	}
	listSupported := len(initOut.AgentCapabilities.SessionCapabilities.List) > 0
	slog.Debug("acp: initialized",
		"protocol", initOut.ProtocolVersion,
		"load_session", initOut.AgentCapabilities.LoadSession,
		"list_sessions", listSupported,
	)
	if s.callbacks != nil {
		s.callbacks.reportListSupported(listSupported)
	}

	if strings.TrimSpace(authMethod) != "" {
		if _, err := s.tr.call(s.ctx, "authenticate", map[string]any{
			"methodId": authMethod,
		}); err != nil {
			return fmt.Errorf("acp: authenticate (%s): %w", authMethod, err)
		}
		slog.Debug("acp: authenticated", "method_id", authMethod)
	}

	wantResume := resumeSessionID != "" && resumeSessionID != core.ContinueSession
	if wantResume && initOut.AgentCapabilities.LoadSession {
		loadParams := map[string]any{
			"sessionId":  resumeSessionID,
			"cwd":        s.workDir,
			"mcpServers": []any{},
		}
		loadRes, err := s.tr.call(s.ctx, "session/load", loadParams)
		if err != nil {
			slog.Warn("acp: session/load failed, starting new session", "error", err)
		} else {
			var lr struct {
				SessionID     string            `json:"sessionId"`
				Modes         *acpModesBlock    `json:"modes"`
				ConfigOptions []acpConfigOption `json:"configOptions"`
				Models        *acpModelsBlock   `json:"models"`
			}
			if json.Unmarshal(loadRes, &lr) == nil && lr.SessionID != "" {
				s.setACPSessionID(lr.SessionID)
				s.absorbModes(lr.Modes)
				s.absorbConfigOptions(lr.ConfigOptions, lr.Models)
				return nil
			}
		}
	}

	newParams := map[string]any{
		"cwd":        s.workDir,
		"mcpServers": []any{},
	}
	newRes, err := s.tr.call(s.ctx, "session/new", newParams)
	if err != nil {
		return fmt.Errorf("acp: session/new: %w", err)
	}
	var sn struct {
		SessionID     string            `json:"sessionId"`
		Modes         *acpModesBlock    `json:"modes"`
		ConfigOptions []acpConfigOption `json:"configOptions"`
		Models        *acpModelsBlock   `json:"models"`
	}
	if err := json.Unmarshal(newRes, &sn); err != nil {
		return fmt.Errorf("acp: parse session/new: %w", err)
	}
	if sn.SessionID == "" {
		return fmt.Errorf("acp: session/new: empty sessionId")
	}
	s.setACPSessionID(sn.SessionID)
	s.absorbModes(sn.Modes)
	s.absorbConfigOptions(sn.ConfigOptions, sn.Models)
	return nil
}

// absorbConfigOptions parses both the new configOptions (category:
// "model") and the legacy models block from session/new/session/load
// responses, and reports the model list to the parent agent via
// callbacks. Either source may be present; when both are,
// configOptions wins.
func (s *acpSession) absorbConfigOptions(configOptions []acpConfigOption, legacy *acpModelsBlock) {
	current, available := parseModels(configOptions, legacy)
	if len(available) == 0 && current == "" {
		return
	}
	if s.callbacks != nil {
		s.callbacks.reportModels(current, available)
	}
}

// absorbModes copies a modes block into the session's cache and fans
// it out to the parent agent callbacks (if any). Both the session and
// the agent need the information: the session uses it to validate
// SetLiveMode inputs; the agent uses it to render `/mode` menus in IM.
func (s *acpSession) absorbModes(block *acpModesBlock) {
	if block == nil || len(block.AvailableModes) == 0 {
		return
	}
	s.modesMu.Lock()
	s.availableModes = append(s.availableModes[:0], block.AvailableModes...)
	if block.CurrentModeID != "" {
		s.currentMode = block.CurrentModeID
	}
	s.modesMu.Unlock()
	if s.callbacks != nil {
		s.callbacks.reportModes(*block)
	}
}

func (s *acpSession) setACPSessionID(id string) {
	s.acpSessMu.Lock()
	s.acpSessID = id
	s.acpSessMu.Unlock()
}

func (s *acpSession) currentACPSessionID() string {
	s.acpSessMu.RLock()
	defer s.acpSessMu.RUnlock()
	return s.acpSessID
}

// CurrentMode returns the ACP modeId most recently applied or reported
// for this session. Empty when the server never sent a modes block.
func (s *acpSession) CurrentMode() string {
	s.modesMu.RLock()
	defer s.modesMu.RUnlock()
	return s.currentMode
}

// SetLiveMode applies a permission mode change to the running session
// via `session/set_mode`. Returns true on success, false if the mode
// is unknown / the call errors / the session is closed.
//
// This is the implementation of core.LiveModeSwitcher for ACP
// sessions; the engine invokes it when the user runs `/mode <x>`,
// `/plan`, `/bypass`, etc. while a session is active.
//
// Client-side validation is important because at least one ACP server
// (devin acp in 2026.4.9) silently accepts unknown modeIds without
// any error, so a server-only check would let typos go undetected.
func (s *acpSession) SetLiveMode(mode string) bool {
	if !s.alive.Load() {
		return false
	}
	sid := s.currentACPSessionID()
	if sid == "" {
		return false
	}
	modeID := s.matchAvailableMode(mode)
	if modeID == "" {
		slog.Debug("acp: SetLiveMode rejected unknown mode",
			"mode", mode,
			"session_id", sid,
		)
		return false
	}
	if _, err := s.tr.call(s.ctx, "session/set_mode", map[string]any{
		"sessionId": sid,
		"modeId":    modeID,
	}); err != nil {
		slog.Warn("acp: session/set_mode failed",
			"mode", modeID,
			"session_id", sid,
			"error", err,
		)
		return false
	}
	s.modesMu.Lock()
	s.currentMode = modeID
	s.modesMu.Unlock()
	if s.callbacks != nil {
		// Re-publish current modeId so Agent.GetMode stays in sync.
		s.modesMu.RLock()
		available := append([]acpModeInfo(nil), s.availableModes...)
		s.modesMu.RUnlock()
		s.callbacks.reportModes(acpModesBlock{
			CurrentModeID:  modeID,
			AvailableModes: available,
		})
	}
	slog.Info("acp: live mode applied", "mode", modeID, "session_id", sid)
	return true
}

// SetModel changes the active model on the running session via
// `session/set_model` (a CodeBuddy extension / older ACP draft RPC).
// Returns an error if the call fails; nil means the server accepted
// the change. If the server returns method-not-found we try the
// newer `session/set_config_option` (configId: "model") as a fallback.
func (s *acpSession) SetModel(modelID string) error {
	if !s.alive.Load() {
		return fmt.Errorf("acp: session closed")
	}
	sid := s.currentACPSessionID()
	if sid == "" {
		return fmt.Errorf("acp: no agent session id")
	}
	params := map[string]any{
		"sessionId": sid,
		"modelId":   modelID,
	}
	if _, err := s.tr.call(s.ctx, "session/set_model", params); err == nil {
		slog.Info("acp: model applied via session/set_model",
			"model", modelID, "session_id", sid)
		if s.callbacks != nil {
			s.callbacks.reportModels(modelID, nil)
		}
		return nil
	} else if rpcErr, ok := err.(*rpcErrPayload); ok && (rpcErr.Code == -32601 || rpcErr.Code == -32600) {
		// Method not found — fall through to session/set_config_option.
		slog.Debug("acp: session/set_model unsupported, trying session/set_config_option",
			"error", err)
	} else {
		return fmt.Errorf("acp: session/set_model: %w", err)
	}

	// Fallback: session/set_config_option (configId: "model")
	params2 := map[string]any{
		"sessionId": sid,
		"configId":  "model",
		"value":     modelID,
	}
	if _, err := s.tr.call(s.ctx, "session/set_config_option", params2); err != nil {
		return fmt.Errorf("acp: session/set_config_option (model): %w", err)
	}
	slog.Info("acp: model applied via session/set_config_option",
		"model", modelID, "session_id", sid)
	if s.callbacks != nil {
		s.callbacks.reportModels(modelID, nil)
	}
	return nil
}

// matchAvailableMode resolves a user-typed mode string to a known ACP
// modeId from the cached availableModes list. Matching is case-
// insensitive on both id and display name to accommodate IM input.
// Returns "" if nothing matches or if modes are unknown (first session
// hasn't handshaked yet).
func (s *acpSession) matchAvailableMode(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	lower := strings.ToLower(input)
	s.modesMu.RLock()
	defer s.modesMu.RUnlock()
	for _, m := range s.availableModes {
		if strings.ToLower(m.ID) == lower || strings.ToLower(m.Name) == lower {
			return m.ID
		}
	}
	return ""
}

func (s *acpSession) onNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		slog.Debug("acp: notification", "method", method)
		return
	}
	s.cacheToolCallInput(params)
	s.maybeAbsorbCurrentModeUpdate(params)
	s.maybeAbsorbConfigOptionUpdate(params)
	s.maybeAbsorbSessionInfoUpdate(params)
	s.maybeAbsorbUsageUpdate(params)
	s.maybeExtractSessionTitle(params)
	sid := s.currentACPSessionID()
	for _, ev := range mapSessionUpdate(sid, params) {
		s.emit(ev)
	}
}

// maybeExtractSessionTitle watches the first agent_message_chunk for
// a session and uses it as the session title for the local /list
// fallback. Subsequent chunks are ignored — we only want the first
// message to act as an auto-generated summary.
func (s *acpSession) maybeExtractSessionTitle(params json.RawMessage) {
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &wrap) != nil || len(wrap.Update) == 0 {
		return
	}
	var head struct {
		Kind string `json:"sessionUpdate"`
	}
	if json.Unmarshal(wrap.Update, &head) != nil || head.Kind != "agent_message_chunk" {
		return
	}
	var u struct {
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(wrap.Update, &u) != nil {
		return
	}
	text := strings.TrimSpace(u.Content.Text)
	if text == "" || s.callbacks == nil {
		return
	}
	// Truncate to ~60 runes for a readable /list entry.
	r := []rune(text)
	if len(r) > 60 {
		text = string(r[:60]) + "…"
	}
	s.callbacks.recordSessionTitle(s.currentACPSessionID(), text)
}

// maybeAbsorbConfigOptionUpdate watches for config_option_update
// notifications and re-publishes model/mode changes so the Agent
// caches stay in sync. Only the "model" category is mapped today;
// "mode" is already handled by current_mode_update above.
func (s *acpSession) maybeAbsorbConfigOptionUpdate(params json.RawMessage) {
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &wrap) != nil || len(wrap.Update) == 0 {
		return
	}
	var head struct {
		Kind          string            `json:"sessionUpdate"`
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(wrap.Update, &head) != nil || head.Kind != "config_option_update" {
		return
	}
	current, available := parseModels(head.ConfigOptions, nil)
	if current == "" && len(available) == 0 {
		return
	}
	if s.callbacks != nil {
		s.callbacks.reportModels(current, available)
	}
}

// maybeAbsorbSessionInfoUpdate watches for session_info_update
// notifications and re-publishes the title so the locally tracked
// session list stays in sync with what the server reports.
func (s *acpSession) maybeAbsorbSessionInfoUpdate(params json.RawMessage) {
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &wrap) != nil || len(wrap.Update) == 0 {
		return
	}
	var head struct {
		Kind  string `json:"sessionUpdate"`
		Title string `json:"title"`
	}
	if json.Unmarshal(wrap.Update, &head) != nil || head.Kind != "session_info_update" {
		return
	}
	if head.Title == "" || s.callbacks == nil {
		return
	}
	s.callbacks.recordSessionTitle(s.currentACPSessionID(), head.Title)
}

// maybeAbsorbUsageUpdate watches for usage_update notifications and
// caches the latest snapshot so GetContextUsage can power /usage.
// ACP's usage_update carries used/size token counts and an optional
// cost; we map used → UsedTokens and size → ContextWindow.
func (s *acpSession) maybeAbsorbUsageUpdate(params json.RawMessage) {
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &wrap) != nil || len(wrap.Update) == 0 {
		return
	}
	var head struct {
		Kind string `json:"sessionUpdate"`
		Used int    `json:"used"`
		Size int    `json:"size"`
	}
	if json.Unmarshal(wrap.Update, &head) != nil || head.Kind != "usage_update" {
		return
	}
	s.usageMu.Lock()
	s.usageSnapshot = &core.ContextUsage{
		UsedTokens:    head.Used,
		ContextWindow: head.Size,
		TotalTokens:   head.Used,
	}
	s.usageMu.Unlock()
}

// GetContextUsage implements core.ContextUsageReporter so the engine's
// /usage command can surface server-reported token usage for ACP
// sessions. Returns nil if the server hasn't sent a usage_update yet.
func (s *acpSession) GetContextUsage() *core.ContextUsage {
	s.usageMu.RLock()
	defer s.usageMu.RUnlock()
	if s.usageSnapshot == nil {
		return nil
	}
	out := *s.usageSnapshot
	return &out
}

// maybeAbsorbCurrentModeUpdate watches session/update notifications
// for `current_mode_update` (server-driven mode switch, e.g. when the
// user toggles modes via the Windsurf/IDE UI while cc-connect is
// connected). Keeping currentMode in sync here means the IM `/mode`
// indicator reflects the true server state rather than the last
// client-initiated value.
func (s *acpSession) maybeAbsorbCurrentModeUpdate(params json.RawMessage) {
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &wrap) != nil || len(wrap.Update) == 0 {
		return
	}
	var head struct {
		Kind          string `json:"sessionUpdate"`
		CurrentModeID string `json:"currentModeId"`
	}
	if json.Unmarshal(wrap.Update, &head) != nil {
		return
	}
	if head.Kind != "current_mode_update" || head.CurrentModeID == "" {
		return
	}
	s.modesMu.Lock()
	s.currentMode = head.CurrentModeID
	available := append([]acpModeInfo(nil), s.availableModes...)
	s.modesMu.Unlock()
	if s.callbacks != nil {
		s.callbacks.reportModes(acpModesBlock{
			CurrentModeID:  head.CurrentModeID,
			AvailableModes: available,
		})
	}
}

// cacheToolCallInput extracts and caches rawInput from tool_call and tool_call_update
// session updates so that handlePermissionRequest can look it up by toolCallId.
// OpenCode ACP bug (#7370): rawInput is empty in tool_call and request_permission,
// but populated in tool_call_update. We cache from both sources.
func (s *acpSession) evictToolInputCacheIfNeededLocked() {
	if len(s.toolInputByID) < toolInputCacheMaxEntries {
		return
	}
	target := toolInputCacheMaxEntries / 2
	for k := range s.toolInputByID {
		if len(s.toolInputByID) <= target {
			break
		}
		delete(s.toolInputByID, k)
	}
}

func (s *acpSession) cacheToolCallInput(params json.RawMessage) {
	var wrap struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &wrap) != nil || len(wrap.Update) == 0 {
		return
	}
	var head struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	if json.Unmarshal(wrap.Update, &head) != nil {
		return
	}
	switch head.SessionUpdate {
	case "tool_call":
		var tc struct {
			ToolCallID string          `json:"toolCallId"`
			Kind       string          `json:"kind"`
			RawInput   json.RawMessage `json:"rawInput"`
		}
		if json.Unmarshal(wrap.Update, &tc) != nil || tc.ToolCallID == "" || len(tc.RawInput) == 0 {
			return
		}
		s.toolInputMu.Lock()
		s.evictToolInputCacheIfNeededLocked()
		input := summarizeACPToolInput(tc.Kind, tc.RawInput)
		s.toolInputByID[tc.ToolCallID] = input
		s.toolInputMu.Unlock()
		slog.Info("acp: cached tool_call input", "toolCallId", tc.ToolCallID, "kind", tc.Kind, "input", input)
	case "tool_call_update":
		var tc struct {
			ToolCallID string          `json:"toolCallId"`
			RawInput   json.RawMessage `json:"rawInput"`
		}
		if json.Unmarshal(wrap.Update, &tc) != nil || tc.ToolCallID == "" || len(tc.RawInput) == 0 {
			return
		}
		input := summarizeACPToolInput("", tc.RawInput)
		if input == "" {
			return
		}
		s.toolInputMu.Lock()
		s.evictToolInputCacheIfNeededLocked()
		s.toolInputByID[tc.ToolCallID] = input
		s.toolInputMu.Unlock()
		slog.Info("acp: cached tool_call_update input", "toolCallId", tc.ToolCallID, "input", input)
	}
}

func (s *acpSession) onServerRequest(method string, id json.RawMessage, params json.RawMessage) {
	switch method {
	case "session/request_permission":
		s.handlePermissionRequest(id, params)
	case "cursor/ask_question", "cursor/create_plan", "cursor/update_todos", "cursor/task", "cursor/generate_image":
		// Cursor CLI extensions — acknowledge so tool flows do not block; IM UX is limited for these.
		slog.Debug("acp: cursor extension request (no-op ack)", "method", method)
		_ = s.tr.respondSuccess(id, map[string]any{})
	default:
		if strings.HasPrefix(method, "cursor/") {
			slog.Debug("acp: unknown cursor extension, ack empty", "method", method)
			_ = s.tr.respondSuccess(id, map[string]any{})
			return
		}
		slog.Info("acp: unhandled server request", "method", method)
		_ = s.tr.respondError(id, -32601, "method not implemented")
	}
}

func (s *acpSession) handlePermissionRequest(id json.RawMessage, params json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			Kind       string          `json:"kind"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []permissionOption `json:"options"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		_ = s.tr.respondError(id, -32602, "invalid params")
		return
	}
	slog.Debug("acp: permission request raw params", "params", string(params))
	reqKey := jsonIDKey(id)
	toolName := p.ToolCall.Title
	if toolName == "" {
		toolName = p.ToolCall.Kind
	}
	if toolName == "" {
		toolName = "permission"
	}

	s.permMu.Lock()
	s.permByID[reqKey] = permState{RPCID: id, Options: p.Options}
	s.permMu.Unlock()

	rawTool := map[string]any{}
	_ = json.Unmarshal(params, &rawTool)

	// OpenCode ACP bug (#7370): rawInput in request_permission is always {},
	// but tool_call_update (which arrives right after) has the real input.
	// Emit in a goroutine so we don't block the read loop, and wait briefly
	// for tool_call_update to populate the cache.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for i := 0; i < 10; i++ {
			s.toolInputMu.Lock()
			toolInput := s.toolInputByID[p.ToolCall.ToolCallID]
			s.toolInputMu.Unlock()
			if toolInput != "" {
				break
			}
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
		s.toolInputMu.Lock()
		toolInput := s.toolInputByID[p.ToolCall.ToolCallID]
		s.toolInputMu.Unlock()
		if toolInput == "" {
			toolInput = summarizeACPToolInput(p.ToolCall.Kind, p.ToolCall.RawInput)
		}
		if toolInput == "" {
			toolInput = p.ToolCall.Title
		}
		if toolInput == "" {
			toolInput = p.ToolCall.ToolCallID
		}

		slog.Info("acp: permission request", "request_id", reqKey, "tool", toolName, "input", toolInput)
		s.emit(core.Event{
			Type:         core.EventPermissionRequest,
			RequestID:    reqKey,
			ToolName:     toolName,
			ToolInput:    toolInput,
			ToolInputRaw: rawTool,
			SessionID:    s.currentACPSessionID(),
		})
	}()
}

func (s *acpSession) emit(ev core.Event) {
	if ev.SessionID == "" {
		ev.SessionID = s.currentACPSessionID()
	}
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

func (s *acpSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("acp: session closed")
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	filePaths := core.SaveFilesToDisk(s.workDir, files)
	prompt = core.AppendFileRefs(prompt, filePaths)
	if len(images) > 0 {
		prompt = s.appendImageRefs(prompt, images)
	}

	sid := s.currentACPSessionID()
	if sid == "" {
		return fmt.Errorf("acp: no agent session id")
	}

	promptBlocks := []any{
		map[string]any{"type": "text", "text": prompt},
	}
	params := map[string]any{
		"sessionId": sid,
		"prompt":    promptBlocks,
	}

	slog.Info("acp: session/prompt start", "session_id", sid, "prompt_len", len(prompt), "images", len(images), "files", len(files))
	_, err := s.tr.call(s.ctx, "session/prompt", params)
	if err != nil {
		slog.Error("acp: session/prompt failed", "session_id", sid, "error", err)
		s.emit(core.Event{Type: core.EventError, Error: err})
		return fmt.Errorf("acp: session/prompt: %w", err)
	}
	slog.Info("acp: session/prompt done", "session_id", sid)

	// Text was streamed via session/update; engine aggregates EventText.
	s.emit(core.Event{
		Type:      core.EventResult,
		SessionID: sid,
		Done:      true,
	})
	return nil
}

func (s *acpSession) appendImageRefs(prompt string, images []core.ImageAttachment) string {
	attachDir := filepath.Join(s.workDir, ".cc-connect-qhn", "attachments")
	if err := os.MkdirAll(attachDir, 0o755); err != nil {
		slog.Warn("acp: mkdir attachments failed", "error", err)
		return prompt
	}
	var paths []string
	for i, img := range images {
		ext := ".bin"
		switch img.MimeType {
		case "image/jpeg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		case "image/png", "":
			ext = ".png"
		}
		fname := fmt.Sprintf("img_%d_%d%s", time.Now().UnixMilli(), i, ext)
		fpath := filepath.Join(attachDir, fname)
		if err := os.WriteFile(fpath, img.Data, 0o644); err != nil {
			slog.Error("acp: save image failed", "error", err)
			continue
		}
		paths = append(paths, fpath)
	}
	if len(paths) == 0 {
		return prompt
	}
	if prompt == "" {
		prompt = "User sent image(s)."
	}
	return prompt + "\n\n(Image files saved locally: " + strings.Join(paths, ", ") + ")"
}

func (s *acpSession) RespondPermission(requestID string, result core.PermissionResult) error {
	if !s.alive.Load() {
		return fmt.Errorf("acp: session closed")
	}

	s.permMu.Lock()
	st, ok := s.permByID[requestID]
	if ok {
		delete(s.permByID, requestID)
	}
	s.permMu.Unlock()
	if !ok {
		return fmt.Errorf("acp: unknown permission request %q", requestID)
	}

	allow := strings.EqualFold(result.Behavior, "allow")
	optID := pickPermissionOptionID(allow, st.Options)
	if allow && optID == "" {
		slog.Warn("acp: allow requested but agent sent no options", "request_id", requestID)
		return s.tr.respondError(st.RPCID, -32603, "no permission options from agent")
	}
	res := buildPermissionResult(allow, optID)

	slog.Debug("acp: permission response", "request_id", requestID, "allow", allow, "option_id", optID)
	return s.tr.respondSuccess(st.RPCID, res)
}

func (s *acpSession) Events() <-chan core.Event {
	return s.events
}

func (s *acpSession) CurrentSessionID() string {
	return s.currentACPSessionID()
}

// RotatesSessionIDOnSpawn implements core.SessionIDRotator.
// ACP backends assign a fresh session/thread id at spawn time that should
// replace any stale resume id persisted locally.
func (s *acpSession) RotatesSessionIDOnSpawn() bool {
	return true
}

func (s *acpSession) Alive() bool {
	return s.alive.Load()
}

// CancelTurn sends a session/cancel notification to abort the current
// in-progress turn. The session remains alive and can accept the next prompt.
func (s *acpSession) CancelTurn() {
	if !s.alive.Load() {
		return
	}
	sid := s.currentACPSessionID()
	if sid == "" {
		return
	}
	_ = s.tr.notify("session/cancel", map[string]any{
		"sessionId": sid,
	})
	slog.Info("acp: sent session/cancel (cancel turn)", "session_id", sid)
}

func (s *acpSession) Close() error {
	if !s.alive.Load() {
		// Already dead: ensure events channel is still closed (idempotent
		// via closeOnce) so the engine's channelClosed path can observe
		// termination even if Close() was never called before the process
		// died on its own.
		s.closeEvents()
		return nil
	}
	s.alive.Store(false)

	// ── Phase 0: Send session/cancel notification ──
	// This tells the ACP agent (e.g. codebuddy) to gracefully abort
	// the current operation: cancel the LLM stream, write results, and
	// run Stop hooks.  We send a notification (no id, no response
	// expected) because session/cancel is fire-and-forget — we don't
	// need to wait for the agent to acknowledge.
	sid := s.currentACPSessionID()
	if sid != "" {
		_ = s.tr.notify("session/cancel", map[string]any{
			"sessionId": sid,
		})
		slog.Info("acp: sent session/cancel", "session_id", sid)
	}

	// ── Phase 1: Close stdin ──
	// Closing stdin signals EOF to the agent process. A well-behaved
	// ACP agent exits on stdin EOF, running any Stop hooks first.
	if s.stdin != nil {
		_ = s.stdin.Close()
	}

	// Wait for the process to exit after stdin close.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("acpSession: exited cleanly after session/cancel + stdin close")
		s.cancel()
		s.closeEvents()
		return nil
	case <-time.After(8 * time.Second):
		slog.Warn("acpSession: graceful stop timed out, sending SIGTERM")
	}

	// ── Phase 2: SIGTERM the whole process group ──
	// Signal the group so grandchildren (shell, npm, git, ...) also get
	// the terminate signal.
	if s.cmd != nil && s.cmd.Process != nil {
		_ = core.SignalProcessGroup(s.cmd, syscall.SIGTERM)
	}
	select {
	case <-done:
		slog.Info("acpSession: exited after SIGTERM")
		s.cancel()
		s.closeEvents()
		return nil
	case <-time.After(5 * time.Second):
		slog.Warn("acpSession: SIGTERM timed out, sending SIGKILL")
	}

	// ── Phase 3: SIGKILL the whole process group ──
	if s.cmd != nil && s.cmd.Process != nil {
		_ = core.ForceKillProcessGroup(s.cmd)
	}
	s.cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		slog.Error("acpSession: SIGKILL timed out, abandoning goroutine; events channel will close when readLoop finally exits")
		// Even if the goroutine is stuck (e.g. process in D-state), defer
		// the events channel close to when the goroutine eventually exits.
		// Without this, the engine's channelClosed path would never fire
		// and the interactiveState would leak.
		go func() {
			<-done
			s.closeEvents()
		}()
		return nil
	}
	s.closeEvents()
	return nil
}

// closeEvents closes the events channel exactly once. Safe to call from
// multiple goroutines and multiple times. After this returns, consumers
// reading from Events() will observe a closed channel and can run their
// channelClosed cleanup path.
func (s *acpSession) closeEvents() {
	s.closeOnce.Do(func() {
		close(s.events)
	})
}

// summarizeACPToolInput extracts a human-readable summary from ACP tool rawInput.
func summarizeACPToolInput(kind string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	if len(m) == 0 {
		return ""
	}
	switch strings.ToLower(kind) {
	case "bash", "shell", "terminal", "execute":
		if cmd, ok := m["command"].(string); ok {
			if desc, ok := m["description"].(string); ok && desc != "" {
				return "# " + desc + "\n" + cmd
			}
			return cmd
		}
	case "read", "write", "edit":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
		if fp, ok := m["path"].(string); ok {
			return fp
		}
	}
	// Fallback: try extracting command with description before formatting JSON.
	if cmd, ok := m["command"].(string); ok {
		if desc, ok := m["description"].(string); ok && desc != "" {
			return "# " + desc + "\n" + cmd
		}
		return cmd
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return string(b)
}
