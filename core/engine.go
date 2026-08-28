package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxPlatformMessageLen = 4000
const telegramBotCommandLimit = 100
const defaultMaxQueuedMessages = 5 // default cap for queued messages per session

const (
	defaultThinkingMaxLen = 300
	defaultToolMaxLen     = 500
)

// Slow-operation thresholds. Operations exceeding these durations produce a
// slog.Warn so operators can quickly pinpoint bottlenecks.
const (
	slowPlatformSend    = 2 * time.Second  // platform Reply / Send
	slowAgentStart      = 5 * time.Second  // agent.StartSession
	slowAgentClose      = 3 * time.Second  // agentSession.Close
	slowAgentSend       = 2 * time.Second  // agentSession.Send
	slowAgentFirstEvent = 15 * time.Second // time from send to first agent event
)

const (
	replyFooterUsageTimeout  = 1500 * time.Millisecond
	replyFooterUsageCacheTTL = 30 * time.Second
)

const (
	messageRecallCheckTimeout = 2 * time.Second
	messageRecallPollInterval = 2 * time.Second
	recalledStopLockWait      = 2 * time.Second
)

// VersionInfo is set by main at startup so that /version works.
var VersionInfo string

// CurrentVersion is the semver tag (e.g. "v1.2.0-beta.1"), set by main.
var CurrentVersion string

// ErrAttachmentSendDisabled indicates that side-channel image/file delivery is disabled by config.
var ErrAttachmentSendDisabled = errors.New("attachment send is disabled by config")

// RestartRequest carries info needed to send a post-restart notification.
type RestartRequest struct {
	SessionKey string `json:"session_key"`
	Platform   string `json:"platform"`
}

type replyFooterUsageCache struct {
	text      string
	fetchedAt time.Time
}

// SaveRestartNotify persists restart info so the new process can send
// a "restart successful" message after startup.
func SaveRestartNotify(dataDir string, req RestartRequest) error {
	dir := filepath.Join(dataDir, "run")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("SaveRestartNotify: mkdir failed", "dir", dir, "error", err)
	}
	data, _ := json.Marshal(req)
	return os.WriteFile(filepath.Join(dir, "restart_notify"), data, 0o644)
}

// ConsumeRestartNotify reads and deletes the restart notification file.
// Returns nil if no notification is pending.
func ConsumeRestartNotify(dataDir string) *RestartRequest {
	p := filepath.Join(dataDir, "run", "restart_notify")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	os.Remove(p)
	var req RestartRequest
	if json.Unmarshal(data, &req) != nil {
		return nil
	}
	return &req
}

// Name returns the engine's project name.
func (e *Engine) Name() string { return e.name }

// SendRestartNotification sends a "restart successful" message to the
// platform/session that initiated the restart.
func (e *Engine) SendRestartNotification(platformName, sessionKey string) {
	for _, p := range e.platforms {
		if p.Name() != platformName {
			continue
		}
		rc, ok := p.(ReplyContextReconstructor)
		if !ok {
			slog.Debug("restart notify: platform does not support ReconstructReplyCtx", "platform", platformName)
			return
		}
		rctx, err := rc.ReconstructReplyCtx(sessionKey)
		if err != nil {
			slog.Debug("restart notify: reconstruct failed", "error", err)
			return
		}
		text := e.i18n.T(MsgRestartSuccess)
		if CurrentVersion != "" {
			text += fmt.Sprintf(" (%s)", CurrentVersion)
		}
		if err := e.waitOutgoing(p); err != nil {
			slog.Debug("restart notify: outgoing wait cancelled or limited", "platform", platformName, "error", err)
			return
		}
		if err := p.Send(e.ctx, rctx, text); err != nil {
			slog.Debug("restart notify: send failed", "error", err)
		}
		return
	}
}

// RestartCh is signaled when /restart is invoked. main listens on it
// to perform a graceful shutdown followed by syscall.Exec.
var RestartCh = make(chan RestartRequest, 1)

// DisplayCfg controls how intermediate messages are surfaced.
// A value of -1 means "use default", 0 means "no truncation".
type DisplayCfg struct {
	Mode             string // "full" (default), "compact", "quiet", or "stream" — thinking/tool visibility
	CardMode         string // "legacy" (default) or "rich" (Card 2.0 Feishu)
	ThinkingMessages bool
	ThinkingMaxLen   int // max runes for thinking preview; 0 = no truncation
	ToolMaxLen       int // max runes for tool use preview; 0 = no truncation
	ToolMessages     bool
}

const (
	displayModeFull    = "full"
	displayModeCompact = "compact"
	displayModeQuiet   = "quiet"
	displayModeStream  = "stream"
)

// InstantReplyCfg controls the immediate confirmation reply sent when a message
// is received, before the agent starts processing.
type InstantReplyCfg struct {
	Enabled bool
	Content string // custom reply text; empty = use i18n MsgStarting default
}

// RateLimitCfg controls per-session message rate limiting.
type RateLimitCfg struct {
	MaxMessages int           // max messages per window; 0 = disabled
	Window      time.Duration // sliding window size
}

// Engine routes messages between platforms and the agent for a single project.
type Engine struct {
	name                  string
	agent                 Agent
	platforms             []Platform
	sessions              *SessionManager
	ctx                   context.Context
	cancel                context.CancelFunc
	i18n                  *I18n
	speech                SpeechCfg
	tts                   *TTSCfg
	display               DisplayCfg
	// platformDisplayOverrides holds per-platform-name overrides of display,
	// keyed by lowercase platform name (e.g. "web", "feishu"). Populated once
	// at startup/reload via SetPlatformDisplayOverrides; read per-turn via
	// resolveDisplayForPlatform. nil/empty map means "no overrides configured"
	// — every platform falls back to display, identical to pre-existing
	// behavior for projects that don't use [projects.platforms.display].
	platformDisplayOverrides map[string]DisplayCfg
	injectSender             bool
	attachmentSendEnabled    bool
	startedAt                time.Time

	providerSaveFunc        func(providerName string) error
	providerAddSaveFunc     func(p ProviderConfig) error
	providerRemoveSaveFunc  func(name string) error
	providerModelSaveFunc   func(providerName, model string) error
	providerRefsSaveFunc    func(refs []string) error
	listGlobalProvidersFunc func(agentType string) ([]ProviderConfig, error)
	modelSaveFunc           func(model string) error

	ttsSaveFunc func(mode string) error

	commandSaveAddFunc func(name, description, prompt, exec, workDir string) error
	commandSaveDelFunc func(name string) error

	displaySaveFunc  func(mode *string, thinkingMessages *bool, thinkingMaxLen, toolMaxLen *int, toolMessages *bool) error
	configReloadFunc func() (*ConfigReloadResult, error)

	hooks              *HookManager
	cronScheduler      *CronScheduler
	heartbeatScheduler *HeartbeatScheduler

	commands *CommandRegistry
	skills   *SkillRegistry
	aliases  map[string]string // trigger → command (e.g. "帮助" → "/help")
	aliasMu  sync.RWMutex

	aliasSaveAddFunc func(name, command string) error
	aliasSaveDelFunc func(name string) error

	bannedWords []string
	bannedMu    sync.RWMutex

	disabledCmds map[string]bool
	adminFrom    string           // comma-separated user IDs for privileged commands; "*" = all allowed users; "" = deny
	userRoles    *UserRoleManager // nil = legacy mode (no per-user policies)
	userRolesMu  sync.RWMutex     // protects userRoles, disabledCmds, and adminFrom

	rateLimiter       *RateLimiter
	outgoingRL        *OutgoingRateLimiter
	streamPreview     StreamPreviewCfg
	instantReply      InstantReplyCfg
	references        ReferenceRenderCfg
	relayManager      RelayManagerAPI
	eventIdleTimeout  time.Duration
	maxQueuedMessages int
	// queuedMessagesMerge: when true, the whole pendingMessages queue is
	// submitted to the agent as a single turn after the current turn
	// completes (IM-friendly: consecutive messages form one request and one
	// reply). False = serial, one message per turn (historical behavior).
	// Zero value false keeps legacy tests unchanged; production default
	// merge is wired from project config in main.go.
	queuedMessagesMerge bool
	dirHistory          *DirHistory
	baseWorkDir         string
	projectState        *ProjectStateStore

	// Auto-compress settings
	autoCompressEnabled   bool
	autoCompressMaxTokens int
	autoCompressMinGap    time.Duration
	resetOnIdle           time.Duration
	// resetOnIdleByPlatform holds per-platform overrides of resetOnIdle,
	// keyed by lowercase platform name/type (e.g. "web", "wecom"), matching
	// the platformDisplayOverrides convention. When a platform has an entry,
	// it takes precedence over resetOnIdle for that platform. A zero entry
	// disables rotation for that platform. nil/empty means "no overrides".
	resetOnIdleByPlatform map[string]time.Duration

	// When true, append [ctx: ~N%] (or model self-report) to assistant replies shown on platforms.
	showContextIndicator bool
	replyFooterEnabled   bool

	// When true, /list etc. only show sessions tracked by heron-connect,
	// hiding sessions created by direct CLI usage in the same work_dir.
	// Default false = show all sessions.
	filterExternalSessions bool

	// When true, placeholder-named sessions get an auto-generated title
	// after their first completed turn (agent summary preferred, first
	// user message snippet as fallback). Wired from project config where
	// the default is true; zero value false keeps legacy tests unchanged.
	autoSessionTitle bool

	// Multi-workspace mode
	multiWorkspace    bool
	baseDir           string
	workspaceBindings *WorkspaceBindingManager
	workspacePool     *workspacePool
	initFlows         map[string]*workspaceInitFlow // workspace channel key → init state
	initFlowsMu       sync.Mutex

	// Terminal observation (--observe)
	observeEnabled    bool
	observeProjectDir string // ~/.claude/projects/{projectKey}
	observeSessionKey string // e.g. "slack:C123:U456" — target for forwarding
	observeCancel     context.CancelFunc

	// Interactive agent session management
	interactiveMu     sync.Mutex
	interactiveStates map[string]*interactiveState // key = sessionKey

	// Session reaper: periodically scans interactiveStates and kills sessions
	// that have been idle (no agent events) for longer than resetOnIdle.
	sessionReapCancel context.CancelFunc

	// turnWg tracks all in-flight processInteractiveMessageWith goroutines.
	// Engine.Stop() waits on it (bounded) so that turn goroutines finish
	// their session.Save() / session.Unlock() before the engine tears down
	// — without this, t.TempDir() cleanup can race with a late Save() and
	// report "directory not empty".
	turnWg sync.WaitGroup

	platformLifecycleMu sync.Mutex
	platformReady       map[Platform]bool
	stopping            bool
	replyFooterMu       sync.Mutex
	replyFooterUsage    replyFooterUsageCache

	// /web command callbacks
	webSetupFunc  func() (port int, token string, needRestart bool, err error)
	webStatusFunc func() (url string)
}

// workspaceInitFlow tracks a channel that is being onboarded to a workspace.
type workspaceInitFlow struct {
	state       string // "awaiting_url", "awaiting_confirm"
	repoURL     string
	cloneTo     string
	channelName string
}

// queuedMessage holds a message that arrived while the session was busy.
// The message is NOT sent to agent stdin at queue time; the event loop
// sends it after the current turn completes to avoid mid-turn interference.
type queuedMessage struct {
	messageID     string
	platform      Platform
	replyCtx      any
	content       string
	images        []ImageAttachment
	files         []FileAttachment
	fromVoice     bool
	userID        string
	userName      string // sender's display name for sender injection
	msgPlatform   string // platform name for sender injection
	msgSessionKey string // session key for extracting chat ID
	channelKey    string // platform-provided channel identifier (preferred over sessionKey extraction)
}

// interactiveState tracks a running interactive agent session and its permission state.
type interactiveState struct {
	agentSession AgentSession
	platform     Platform
	// platformName is the logical platform name (e.g. "web", "feishu") used
	// to look up per-platform display overrides. Distinct from platform's
	// Name() because bridge-routed platforms (BridgePlatform) always report
	// Name()=="bridge" regardless of the underlying registered adapter (web
	// admin UI, or any future bridge-routed IM platform) — only the
	// per-message Message.Platform string (mirrored here) carries that
	// granularity. Empty means "no specific platform identity known", which
	// resolveDisplayForPlatform treats as "use the engine default".
	platformName     string
	replyCtx         any
	currentMessageID string
	workspaceDir     string
	agent            Agent
	mu               sync.Mutex
	stopCh           chan struct{}
	stopped          bool
	// cancelCh signals cmdCancel that the running foreground turn should stop
	// relaying further events to the platform. Created fresh at the start of
	// each processInteractiveEvents call, cleared (under mu) when the turn
	// ends. nil means no turn is currently in progress. Unlike stopCh (which
	// tears down the whole interactive state), cancelCh is scoped to a single
	// turn so the session stays alive for the next message.
	cancelCh               chan struct{}
	pending                *pendingPermission
	pendingMessages        []queuedMessage // messages queued while session was busy
	approveAll             bool            // when true, auto-approve all permission requests for this session
	fromVoice              bool            // true if current turn originated from voice transcription
	sideText               string
	lastTurnMessageID      string
	deleteMode             *deleteModeState
	modelSwitch            *modelSwitchState
	pendingProviderAdd     *pendingProviderAddState
	lastAutoCompressAt     time.Time
	lastAutoCompressTokens int

	// Unsolicited event reader: a background goroutine that consumes agent
	// events between user-initiated turns (e.g. background task completions).
	// Cancel unsolicitedCancel to stop the reader; wait on unsolicitedDone
	// to confirm it has exited before starting a new foreground turn.
	unsolicitedCancel context.CancelFunc // nil when no reader is running
	unsolicitedDone   chan struct{}      // closed when the reader goroutine exits

	// eventsNeedResync is true when buffered events should be drained before
	// the next turn (e.g. after an abnormal exit). Defaults to true (safe);
	// cleared to false only after a clean EventResult.
	eventsNeedResync bool

	// lastEventTime records when the last agent event was received for this
	// session. Used by the session reaper to detect and kill sessions that
	// have been idle (no agent output) for too long.
	lastEventTime time.Time

	// turnStartTime is set when a new foreground turn begins. Used as a
	// fallback for lastEventTime when no events have been received yet.
	turnStartTime time.Time
}

type pendingProviderAddState struct {
	phase            string // "preset" = waiting for API key; "other" = waiting for name api_key base_url [model]
	name             string
	baseURL          string
	model            string
	inviteURL        string
	codexWireAPI     string
	codexHTTPHeaders map[string]string
}

type deleteModeState struct {
	page        int
	selectedIDs map[string]struct{}
	phase       string
	hint        string
	result      string
}

type modelSwitchState struct {
	phase  string
	target string
	result string
}

// pendingPermission represents a permission request waiting for user response.
type pendingPermission struct {
	RequestID       string
	ToolName        string
	ToolInput       map[string]any
	InputPreview    string
	Questions       []UserQuestion // non-nil for AskUserQuestion
	Answers         map[int]string // collected answers keyed by question index
	CurrentQuestion int            // index of the question currently being asked
	Resolved        chan struct{}  // closed when user responds
	resolveOnce     sync.Once
}

func (s *interactiveState) stopSignal() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopCh == nil {
		s.stopCh = make(chan struct{})
		if s.stopped {
			close(s.stopCh)
		}
	}
	return s.stopCh
}

func (s *interactiveState) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

func (s *interactiveState) markStopped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	if s.stopCh == nil {
		s.stopCh = make(chan struct{})
	}
	close(s.stopCh)
}

// resolve safely closes the Resolved channel exactly once.
func (pp *pendingPermission) resolve() {
	pp.resolveOnce.Do(func() { close(pp.Resolved) })
}

func NewEngine(name string, ag Agent, platforms []Platform, sessionStorePath string, lang Language) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		name:                  name,
		agent:                 ag,
		platforms:             platforms,
		sessions:              NewSessionManager(sessionStorePath),
		ctx:                   ctx,
		cancel:                cancel,
		i18n:                  NewI18n(lang),
		attachmentSendEnabled: true,
		display:               DisplayCfg{Mode: "full", ThinkingMessages: true, ThinkingMaxLen: defaultThinkingMaxLen, ToolMaxLen: defaultToolMaxLen, ToolMessages: true, CardMode: "legacy"},
		commands:              NewCommandRegistry(),
		skills:                NewSkillRegistry(),
		aliases:               make(map[string]string),
		interactiveStates:     make(map[string]*interactiveState),
		platformReady:         make(map[Platform]bool),
		startedAt:             time.Now(),
		streamPreview:         DefaultStreamPreviewCfg(),
		references:            DefaultReferenceRenderCfg(),
		eventIdleTimeout:      defaultEventIdleTimeout,
		maxQueuedMessages:     defaultMaxQueuedMessages,
		showContextIndicator:  true,
	}

	if ag != nil {
		e.sessions.InvalidateForAgent(ag.Name())
	}

	if cp, ok := ag.(CommandProvider); ok {
		e.commands.SetAgentDirs(cp.CommandDirs())
	}
	if sp, ok := ag.(SkillProvider); ok {
		e.skills.SetDirs(sp.SkillDirs())
	}

	return e
}

// ExecuteHeartbeat runs a heartbeat check by injecting a synthetic message
// into the main session, similar to cron but designed for periodic awareness.
func (e *Engine) ExecuteHeartbeat(sessionKey, prompt string, silent bool) error {
	platformName := ""
	if idx := strings.Index(sessionKey, ":"); idx > 0 {
		platformName = sessionKey[:idx]
	}

	var targetPlatform Platform
	for _, p := range e.platforms {
		if p.Name() == platformName {
			targetPlatform = p
			break
		}
	}
	// Fallback: in multi-workspace mode the stored session key may be prefixed
	// with the workspace path (e.g. "/home/user/project:slack:C123:U456").
	// Search for a known platform name within the key and strip the prefix.
	if targetPlatform == nil {
		for _, p := range e.platforms {
			needle := ":" + p.Name() + ":"
			if idx := strings.Index(sessionKey, needle); idx >= 0 {
				targetPlatform = p
				platformName = p.Name()
				sessionKey = sessionKey[idx+1:] // strip workspace prefix
				break
			}
		}
	}
	if targetPlatform == nil {
		return fmt.Errorf("platform %q not found for session %q", platformName, sessionKey)
	}

	rc, ok := targetPlatform.(ReplyContextReconstructor)
	if !ok {
		return fmt.Errorf("platform %q does not support proactive messaging (heartbeat)", platformName)
	}

	replyCtx, err := rc.ReconstructReplyCtx(sessionKey)
	if err != nil {
		return fmt.Errorf("reconstruct reply context: %w", err)
	}

	if !silent {
		e.send(targetPlatform, replyCtx, "💓 heartbeat")
	}

	msg := &Message{
		SessionKey: sessionKey,
		Platform:   platformName,
		UserID:     "heartbeat",
		UserName:   "heartbeat",
		Content:    prompt,
		ReplyCtx:   replyCtx,
	}

	session := e.sessions.GetOrCreateActive(sessionKey)
	if !session.TryLock() {
		return fmt.Errorf("session %q is busy", sessionKey)
	}

	e.processInteractiveMessage(targetPlatform, msg, session)
	return nil
}

func (e *Engine) Start() error {
	var startErrs []error
	readyCount := 0
	pendingCount := 0
	for _, p := range e.platforms {
		_, isAsync := p.(AsyncRecoverablePlatform)
		if async, ok := p.(AsyncRecoverablePlatform); ok {
			async.SetLifecycleHandler(e)
		}
		if err := p.Start(e.handleMessage); err != nil {
			slog.Warn("platform start failed", "project", e.name, "platform", p.Name(), "error", err)
			startErrs = append(startErrs, fmt.Errorf("[%s] start platform %s: %w", e.name, p.Name(), err))
			continue
		}
		if isAsync {
			pendingCount++
			slog.Info("platform recovery loop started", "project", e.name, "platform", p.Name())
			continue
		}
		e.onPlatformReady(p)
		readyCount++
	}

	// Log summary
	if len(startErrs) > 0 || pendingCount > 0 {
		slog.Warn("engine started with partial readiness",
			"project", e.name,
			"agent", e.agent.Name(),
			"ready", readyCount,
			"pending", pendingCount,
			"failed", len(startErrs))
	} else {
		slog.Info("engine started", "project", e.name, "agent", e.agent.Name(), "platforms", len(e.platforms))
	}

	// Only return error if ALL platforms failed
	if len(startErrs) == len(e.platforms) && len(e.platforms) > 0 {
		return startErrs[0] // Return first error
	}

	e.startObserver()
	e.startSessionReaper()
	return nil
}

func (e *Engine) Stop() error {
	e.platformLifecycleMu.Lock()
	e.stopping = true
	e.platformLifecycleMu.Unlock()

	// Cancel first so late lifecycle callbacks observe shutdown immediately.
	e.cancel()

	if e.observeCancel != nil {
		e.observeCancel()
	}

	// Stop platforms after cancellation so they can unwind against the closed context.
	var errs []error
	for _, p := range e.platforms {
		if err := p.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stop platform %s: %w", p.Name(), err))
		}
	}

	e.interactiveMu.Lock()
	states := make(map[string]*interactiveState, len(e.interactiveStates))
	for k, v := range e.interactiveStates {
		states[k] = v
		delete(e.interactiveStates, k)
	}
	e.interactiveMu.Unlock()

	// Close all agent sessions in parallel. Each close may take up to 130s
	// (Stop hooks + SIGTERM + SIGKILL); serialising them would make restart
	// unacceptably slow and risk leaving subprocesses alive when
	// syscall.Exec fires (they'd become orphans with PPID=1).
	//
	// We bound the whole batch with an overall timeout so a single stuck
	// session can't block shutdown indefinitely. Sessions that don't
	// finish in time are abandoned — their goroutines continue in the
	// background but the process will be replaced by syscall.Exec.
	const stopBatchTimeout = 180 * time.Second
	var wg sync.WaitGroup
	for key, state := range states {
		if state.agentSession == nil {
			continue
		}
		wg.Add(1)
		go func(k string, st *interactiveState) {
			defer wg.Done()
			slog.Debug("engine.Stop: closing agent session", "session", k)
			// Stop the unsolicited reader and mark stopped so any
			// goroutine waiting on stopCh/pending unblocks promptly.
			e.stopUnsolicitedReader(st)
			st.markStopped()
			st.mu.Lock()
			pending := st.pending
			st.pending = nil
			st.mu.Unlock()
			if pending != nil {
				pending.resolve()
			}
			e.closeAgentSessionWithTimeout(k, st.agentSession)
		}(key, state)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopBatchTimeout):
		slog.Error("engine.Stop: close all agent sessions timed out, abandoning",
			"timeout", stopBatchTimeout, "sessions", len(states))
	}

	// Wait for in-flight turn goroutines to finish. Closing agent sessions
	// (above) causes their event loops to exit via channelClosed / ctx.Done,
	// but the goroutine may still be in session.Save() or session.Unlock().
	// Waiting a short, bounded time prevents races where TempDir cleanup
	// (or the next test) collides with a late Save().
	turnDone := make(chan struct{})
	go func() {
		e.turnWg.Wait()
		close(turnDone)
	}()
	select {
	case <-turnDone:
	case <-time.After(10 * time.Second):
		slog.Warn("engine.Stop: turn goroutines did not finish in 10s, abandoning")
	}

	if e.rateLimiter != nil {
		e.rateLimiter.Stop()
	}
	e.userRolesMu.Lock()
	if e.userRoles != nil {
		e.userRoles.Stop()
	}
	e.userRolesMu.Unlock()

	if err := e.agent.Stop(); err != nil {
		errs = append(errs, fmt.Errorf("stop agent %s: %w", e.agent.Name(), err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("engine stop errors: %v", errs)
	}
	return nil
}

// OnPlatformReady marks an async platform as ready and initializes platform-level
// capabilities once per ready cycle.
func (e *Engine) OnPlatformReady(p Platform) {
	e.onPlatformReady(p)
}

// OnPlatformUnavailable marks an async platform as unavailable.
func (e *Engine) OnPlatformUnavailable(p Platform, err error) {
	if !e.markPlatformUnavailable(p) {
		return
	}
	slog.Warn("platform unavailable", "project", e.name, "platform", p.Name(), "error", err)
}

// ReceiveMessage delivers a message from a platform to the engine.
// This is a public wrapper for use in integration tests and external callers.
func (e *Engine) ReceiveMessage(p Platform, msg *Message) {
	e.handleMessage(p, msg)
}

func (e *Engine) onPlatformReady(p Platform) {
	if !e.markPlatformReady(p) {
		return
	}
	slog.Info("platform ready", "project", e.name, "platform", p.Name())
	e.initPlatformCapabilities(p)
}

func (e *Engine) markPlatformReady(p Platform) bool {
	e.platformLifecycleMu.Lock()
	defer e.platformLifecycleMu.Unlock()

	if e.stopping || e.ctx.Err() != nil {
		return false
	}
	if e.platformReady[p] {
		return false
	}
	e.platformReady[p] = true
	return true
}

func (e *Engine) markPlatformUnavailable(p Platform) bool {
	e.platformLifecycleMu.Lock()
	defer e.platformLifecycleMu.Unlock()

	if e.stopping || e.ctx.Err() != nil {
		return false
	}
	if !e.platformReady[p] {
		return false
	}
	e.platformReady[p] = false
	return true
}

func (e *Engine) initPlatformCapabilities(p Platform) {
	if registrar, ok := p.(CommandRegistrar); ok {
		commands, skillsOmitted := e.menuCommandsForPlatform(p.Name())
		if skillsOmitted && strings.EqualFold(p.Name(), "telegram") {
			slog.Info("telegram: omitting skill commands from menu due to command limit", "project", e.name)
		}
		if err := registrar.RegisterCommands(commands); err != nil {
			slog.Error("platform command registration failed", "project", e.name, "platform", p.Name(), "error", err)
		} else {
			slog.Debug("platform commands registered", "project", e.name, "platform", p.Name(), "count", len(commands))
		}
	}

	if nav, ok := p.(CardNavigable); ok {
		nav.SetCardNavigationHandler(e.handleCardNav)
	}
}

// matchBannedWord returns the first banned word found in content, or "".
func (e *Engine) matchBannedWord(content string) string {
	e.bannedMu.RLock()
	defer e.bannedMu.RUnlock()
	if len(e.bannedWords) == 0 {
		return ""
	}
	lower := strings.ToLower(content)
	for _, w := range e.bannedWords {
		if strings.Contains(lower, w) {
			return w
		}
	}
	return ""
}

// resolveAlias checks if the content (or its first word) matches an alias and replaces it.
func (e *Engine) resolveAlias(content string) string {
	e.aliasMu.RLock()
	defer e.aliasMu.RUnlock()

	if len(e.aliases) == 0 {
		return content
	}

	// Exact match on full content
	if cmd, ok := e.aliases[content]; ok {
		return cmd
	}

	// Match first word, append remaining args
	parts := strings.SplitN(content, " ", 2)
	if cmd, ok := e.aliases[parts[0]]; ok {
		if len(parts) > 1 {
			return cmd + " " + parts[1]
		}
		return cmd
	}
	return content
}

func (e *Engine) handleMessageRecall(p Platform, msg *Message) {
	messageID := strings.TrimSpace(msg.MessageID)
	if messageID == "" {
		slog.Debug("message recall ignored without message id", "platform", msg.Platform)
		return
	}

	if sessionKey, ok := e.findCurrentMessageSession(messageID); ok {
		if e.stopInteractiveSessionSilently(sessionKey) {
			slog.Info("active message recalled; session stopped",
				"platform", p.Name(),
				"msg_id", messageID,
				"session", sessionKey,
			)
			return
		}
	}

	if sessionKey, ok := e.removeQueuedMessageByID(messageID); ok {
		slog.Info("queued message recalled; removed from pending queue",
			"platform", p.Name(),
			"msg_id", messageID,
			"session", sessionKey,
		)
		return
	}

	slog.Debug("message recall ignored; no active or queued message matched",
		"platform", p.Name(),
		"msg_id", messageID,
	)
}

func (e *Engine) findCurrentMessageSession(messageID string) (string, bool) {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()

	for sessionKey, state := range e.interactiveStates {
		if state == nil {
			continue
		}
		state.mu.Lock()
		currentMessageID := state.currentMessageID
		state.mu.Unlock()
		if currentMessageID == messageID {
			return sessionKey, true
		}
	}
	return "", false
}

// resolveWeComQuotedSession maps a WeCom group quoted reply back to the original
// session it referenced. WeCom's quote payload carries only the referenced message's
// text (no id/root/sender), so we cannot key on an id. Instead we match the quoted
// text against the assistant History of the group's other sessions.
//
// Matching rules (chosen to avoid cross-talk, per design):
//   - quoted text is normalized (whitespace collapsed) and must be >= 12 chars,
//     so short boilerplate ("好的"/"收到") never triggers a session switch;
//   - scan every session whose key shares the group prefix "wecom:{chatID}:";
//   - an assistant History entry whose normalized content contains the normalized
//     quoted text counts as a hit for that session;
//   - exactly one distinct session hits → return it (the user continues that discussion);
//   - zero or multiple sessions hit → return "" so the caller keeps the personal
//     session (safe fallback, no accidental cross-talk).
func (e *Engine) resolveWeComQuotedSession(msg *Message, sessions *SessionManager) string {
	chatID := extractChannelID(msg.SessionKey)
	if chatID == "" {
		return ""
	}
	prefix := "wecom:" + chatID + ":"
	quoteNorm := normalizeWhitespace(msg.QuotedText)
	if len([]rune(quoteNorm)) < 12 {
		return ""
	}

	idToKey, _ := sessions.SessionKeyMap()

	hitSession := ""
	hits := 0
	for _, s := range sessions.AllSessions() {
		key := idToKey[s.ID]
		if key == "" || !strings.HasPrefix(key, prefix) {
			continue
		}
		if key == msg.SessionKey {
			continue // don't match the sender's own (current) session
		}
		matched := false
		for _, h := range s.History {
			if h.Role != "assistant" {
				continue
			}
			if strings.Contains(normalizeWhitespace(h.Content), quoteNorm) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		hits++
		if hits > 1 {
			// Multiple sessions contain the quoted text — ambiguous, bail out.
			return ""
		}
		hitSession = key
	}
	if hits == 1 {
		return hitSession
	}
	return ""
}

// normalizeWhitespace collapses all whitespace runs to a single space and trims.
func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (e *Engine) removeQueuedMessageByID(messageID string) (string, bool) {
	e.interactiveMu.Lock()
	states := make(map[string]*interactiveState, len(e.interactiveStates))
	for sessionKey, state := range e.interactiveStates {
		states[sessionKey] = state
	}
	e.interactiveMu.Unlock()

	for sessionKey, state := range states {
		if state == nil {
			continue
		}
		state.mu.Lock()
		pending := state.pendingMessages
		if len(pending) == 0 {
			state.mu.Unlock()
			continue
		}
		filtered := pending[:0]
		removed := false
		for _, queued := range pending {
			if queued.messageID == messageID {
				removed = true
				continue
			}
			filtered = append(filtered, queued)
		}
		if removed {
			state.pendingMessages = filtered
			state.mu.Unlock()
			return sessionKey, true
		}
		state.mu.Unlock()
	}
	return "", false
}

func (e *Engine) stopCurrentMessageIfRecalled(sessionKey string) bool {
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[sessionKey]
	e.interactiveMu.Unlock()
	if !ok || state == nil {
		return false
	}

	state.mu.Lock()
	platform := state.platform
	replyCtx := state.replyCtx
	messageID := state.currentMessageID
	state.mu.Unlock()
	if platform == nil || replyCtx == nil || messageID == "" {
		return false
	}

	detector, ok := platform.(MessageRecallDetector)
	if !ok {
		return false
	}

	ctx, cancel := context.WithTimeout(e.ctx, messageRecallCheckTimeout)
	defer cancel()
	recalled, err := detector.IsMessageRecalled(ctx, replyCtx)
	if err != nil {
		slog.Debug("message recall fallback check failed",
			"platform", platform.Name(),
			"msg_id", messageID,
			"session", sessionKey,
			"error", err,
		)
		return false
	}
	if !recalled {
		return false
	}
	if e.stopInteractiveSessionSilently(sessionKey) {
		slog.Info("active message recalled by fallback probe; session stopped",
			"platform", platform.Name(),
			"msg_id", messageID,
			"session", sessionKey,
		)
		return true
	}
	return false
}

func (e *Engine) waitForSessionLock(session *Session, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if session.TryLock() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-e.ctx.Done():
			return false
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (e *Engine) startMessageRecallMonitor(sessionKey string) context.CancelFunc {
	ctx, cancel := context.WithCancel(e.ctx)
	go func() {
		ticker := time.NewTicker(messageRecallPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.stopCurrentMessageIfRecalled(sessionKey) {
					return
				}
			}
		}
	}()
	return cancel
}

func (e *Engine) handleMessage(p Platform, msg *Message) {
	if msg.Recalled {
		e.handleMessageRecall(p, msg)
		return
	}

	slog.Info("message received",
		"platform", msg.Platform, "msg_id", msg.MessageID,
		"session", msg.SessionKey, "user", msg.UserName,
		"content_len", len(msg.Content),
		"has_images", len(msg.Images) > 0, "has_audio", msg.Audio != nil, "has_files", len(msg.Files) > 0,
	)

	e.hooks.Emit(HookEvent{
		Event:      HookEventMessageReceived,
		SessionKey: msg.SessionKey,
		Platform:   msg.Platform,
		UserID:     msg.UserID,
		UserName:   msg.UserName,
		Content:    msg.Content,
	})

	// Voice message: transcribe to text first
	if msg.Audio != nil {
		// If STT is configured, use it for transcription (more accurate)
		if e.speech.Enabled && e.speech.STT != nil {
			e.handleVoiceMessage(p, msg)
			return
		}
		// Fallback: use platform-provided recognition text if available
		if msg.Content == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceNotEnabled))
			return
		}
		// Use platform recognition with a hint, then continue processing
		slog.Info("using platform-provided voice recognition",
			"platform", msg.Platform, "content_len", len(msg.Content))
		if msg.FromVoice {
			// Use platform name as parameter for the message
			// Capitalize first letter for better presentation
			if platformName := msg.Platform; len(platformName) > 0 {
				// Safe capitalization that handles multi-word names
				r := []rune(platformName)
				if len(r) > 0 {
					r[0] = []rune(strings.ToUpper(string(r[0])))[0]
				}
				platformName = string(r)
				e.send(p, msg.ReplyCtx, e.i18n.Tf(MsgVoiceUsingPlatformRecognition, platformName))
			}
		}
		// Continue processing with the platform-provided text content
	}

	content := strings.TrimSpace(msg.Content)
	if content == "" && len(msg.Images) == 0 && len(msg.Files) == 0 && msg.Location == nil {
		return
	}

	// Resolve aliases on user text BEFORE merging ExtraContent, so reply
	// quotes and platform context survive alias resolution (PR #420 fix).
	content = e.resolveAlias(content)
	if msg.ExtraContent != "" {
		if content == "" {
			msg.Content = msg.ExtraContent
		} else {
			msg.Content = msg.ExtraContent + "\n" + content
		}
	} else {
		msg.Content = content
	}

	// Rate limit check (per-user role-based, then global fallback)
	if !e.checkRateLimit(msg) {
		slog.Info("message rate limited",
			"session", msg.SessionKey, "user_id", msg.UserID, "user", msg.UserName)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRateLimited))
		return
	}

	// Banned words check (skip for slash commands and ! shell shortcut)
	if !strings.HasPrefix(content, "/") && !strings.HasPrefix(content, "!") {
		if word := e.matchBannedWord(content); word != "" {
			slog.Info("message blocked by banned word", "word", word, "user", msg.UserName)
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgBannedWordBlocked))
			return
		}
	}

	// Multi-workspace resolution
	var wsAgent Agent
	var wsSessions *SessionManager
	var resolvedWorkspace string
	if e.multiWorkspace {
		channelID := effectiveChannelID(msg)
		channelKey := effectiveWorkspaceChannelKey(msg)
		workspace, channelName, err := e.resolveWorkspace(p, channelID)
		if err != nil {
			slog.Error("workspace resolution failed", "error", err,
		"session_key", msg.SessionKey, "user", msg.UserID, "platform", msg.Platform)
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
			return
		}
		if workspace == "" {
			// No workspace — handle init flow (unless it's a /workspace command)
			if !strings.HasPrefix(content, "/workspace") && !strings.HasPrefix(content, "/ws ") {
				if e.handleWorkspaceInitFlow(p, msg, channelName) {
					return
				}
			} else {
				// Workspace command bypassed the init flow; clean up any stale flow
				// so it doesn't interfere if the channel becomes unbound again later.
				e.initFlowsMu.Lock()
				delete(e.initFlows, channelKey)
				e.initFlowsMu.Unlock()
			}
			// If init flow didn't consume, only workspace commands work
			if !strings.HasPrefix(content, "/") {
				return
			}
		} else {
			// Touch for idle tracking
			if ws := e.workspacePool.Get(workspace); ws != nil {
				ws.Touch()
			}

			var effectiveWorkspace string
			wsAgent, wsSessions, _, effectiveWorkspace, err = e.workspaceContext(workspace, msg.SessionKey)
			if err != nil {
				slog.Error("failed to create workspace agent",
		"workspace", workspace, "error", err,
		"session_key", msg.SessionKey, "user", msg.UserID)
				e.reply(p, msg.ReplyCtx, fmt.Sprintf("Failed to initialize workspace: %v", err))
				return
			}
			resolvedWorkspace = effectiveWorkspace
		}
	}

	if len(msg.Images) == 0 && strings.HasPrefix(content, "/") {
		if e.handleCommand(p, msg, content) {
			return
		}
		// Unrecognized slash command — fall through to agent as normal message
	}

	// Permission responses bypass the session lock
	if e.handlePendingPermission(p, msg, content) {
		return
	}

	// "!" prefix: treat as shell command (same as /shell)
	// Placed after permission handling so "!yes" doesn't hijack permission responses.
	if len(msg.Images) == 0 && strings.HasPrefix(content, "!") {
		shellCmd := strings.TrimSpace(content[1:])
		if shellCmd != "" {
			// Check disabled / admin just like handleCommand does for "shell"
			e.userRolesMu.RLock()
			disabledCmds := e.disabledCmds
			urm := e.userRoles
			e.userRolesMu.RUnlock()
			if urm != nil {
				if role := urm.ResolveRole(msg.UserID); role != nil {
					disabledCmds = role.DisabledCmds
				}
			}
			if disabledCmds["shell"] {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCommandDisabled), "!"))
				return
			}
			if !e.isAdmin(msg.UserID) {
				e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAdminRequired), "!"))
				return
			}
			slog.Info("audit: command_executed",
				"user_id", msg.UserID, "platform", msg.Platform,
				"project", e.name, "command", "shell")
			e.cmdShell(p, msg, "/shell "+shellCmd)
			return
		}
	}

	// Pending provider add (card-driven multi-step flow)
	if e.handlePendingProviderAdd(p, msg, content) {
		return
	}

	// Select session manager and agent based on workspace mode
	sessions := e.sessions
	agent := e.agent
	interactiveKey := msg.SessionKey
	if e.multiWorkspace && wsSessions != nil {
		sessions = wsSessions
		agent = wsAgent
		interactiveKey = resolvedWorkspace + ":" + msg.SessionKey
	}

	// WeCom quoted-reply resolution: a user quoting a bot reply in a group chat
	// should continue the ORIGINAL session rather than start a personal one.
	// Protocol gives no quoted-message id, so we match the quoted text against
	// assistant history of the group's other sessions. Unique hit → reuse it;
	// no/multiple hit → keep the personal session (safe fallback, no cross-talk).
	if msg.Platform == "wecom" && msg.QuotedText != "" {
		if resolved := e.resolveWeComQuotedSession(msg, sessions); resolved != "" {
			msg.SessionKey = resolved
			interactiveKey = resolved
			if e.multiWorkspace && wsSessions != nil {
				interactiveKey = resolvedWorkspace + ":" + resolved
			}
		}
	}

	// Explicit per-conversation routing: when the client targets a specific
	// heron session id (Web conversations now each own a unique session_key and
	// send the id of the conversation it is showing), route to THAT session
	// rather than the key's current active session. This keeps picking an old
	// conversation (e.g. s89) from going to whatever is currently active under
	// the key (e.g. s91 after an idle auto-reset). Falls back to the key-based
	// active session when the id is absent or no longer exists.
	session := sessions.FindByID(msg.SessionID)
	if session == nil {
		session = sessions.GetOrCreateActive(msg.SessionKey)
	}
	sessions.UpdateUserMeta(msg.SessionKey, msg.UserName, msg.ChatName)
	if !session.TryLock() {
		if e.stopCurrentMessageIfRecalled(interactiveKey) {
			if e.waitForSessionLock(session, recalledStopLockWait) {
				goto sessionLocked
			}
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPreviousProcessing))
			return
		}
		// Session is busy — try to queue the message for the running turn
		// so the agent processes it immediately after the current turn ends.
		if e.queueMessageForBusySession(p, msg, interactiveKey) {
			// Race guard: the drain loop in processInteractiveMessageWith may
			// have just finished (session unlocked) between our TryLock failure
			// and the queue append. Re-try TryLock — if it succeeds, no one is
			// draining the queue so we must start a processor ourselves.
			if session.TryLock() {
				go e.drainOrphanedQueue(session, sessions, interactiveKey, agent, resolvedWorkspace)
			}
			return
		}
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPreviousProcessing))
		return
	}

sessionLocked:
	if rotated := e.maybeAutoResetSessionOnIdle(p, msg, sessions, interactiveKey, session); rotated != nil {
		session = rotated
	}

	// Ensure an interactiveState entry exists before launching the async
	// processor so messages arriving during session startup can be queued
	// instead of dropped (issue #565).
	e.ensureInteractiveStateForQueueing(interactiveKey, p, msg.ReplyCtx)

	slog.Info("processing message",
		"platform", msg.Platform,
		"user", msg.UserName,
		"session", session.ID,
	)

	e.turnWg.Add(1)
	go func() {
		defer e.turnWg.Done()
		e.processInteractiveMessageWith(p, msg, session, agent, sessions, interactiveKey, resolvedWorkspace, msg.SessionKey)
	}()
}

func (e *Engine) maybeAutoResetSessionOnIdle(p Platform, msg *Message, sessions *SessionManager, interactiveKey string, session *Session) *Session {
	// Per-platform override takes precedence (0 = disabled for this platform).
	// Match on msg.Platform (the platform type the client reports, e.g. "web")
	// rather than p.Name(): for Web/bridge messages p is the BridgePlatform whose
	// Name() is always "bridge", which would never match a `type = "web"`
	// override and would wrongly fall back to the project-level value.
	resetOnIdle := e.resolveResetOnIdleForPlatform(msg.Platform)
	if resetOnIdle <= 0 || session == nil {
		return nil
	}

	hasBackend := session.GetAgentSessionID() != ""
	hasHistory := len(session.GetHistory(1)) > 0
	if !hasBackend && !hasHistory {
		return nil
	}

	lastActive := session.GetUpdatedAt()
	if lastActive.IsZero() || time.Since(lastActive) < resetOnIdle {
		return nil
	}

	slog.Info("auto-resetting idle session",
		"session_key", msg.SessionKey,
		"session_id", session.ID,
		"idle_for", time.Since(lastActive),
		"threshold", resetOnIdle,
	)

	// Check if the old session has an agent process that needs graceful
	// shutdown. If so, tell the user we're wrapping up before blocking.
	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[interactiveKey]
	hasAgent := hasState && state != nil && state.agentSession != nil && state.agentSession.Alive()
	e.interactiveMu.Unlock()

	if hasAgent {
		// Notify the user before the potentially long close. The close
		// returns as soon as the process exits (usually seconds), but
		// Stop hooks can take up to 120s.
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgSessionClosingGraceful))
	}

	e.cleanupInteractiveState(interactiveKey)
	session.UnlockWithoutUpdate()

	newSession := sessions.NewSession(msg.SessionKey, "")
	if !newSession.TryLock() {
		slog.Error("failed to lock new session after idle auto-reset", "session_key", msg.SessionKey, "new_session", newSession.ID)
		// Re-lock the old session so the caller can proceed safely.
		// The old session was unlocked above; without this, the caller
		// would operate on an unlocked session, which is racy.
		if !session.TryLock() {
			slog.Error("failed to re-lock old session after idle auto-reset failure", "session_key", msg.SessionKey, "session_id", session.ID)
			return nil
		}
		return session
	}

	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgSessionAutoResetIdle, int(resetOnIdle/time.Minute)))
	return newSession
}

// queueMessageForBusySession queues a message for later delivery when the
// session is busy. The message is NOT sent to agent stdin at queue time;
// the event loop sends it after the current turn's EventResult is received.
// Returns true if the message was successfully queued, false otherwise.
func (e *Engine) queueMessageForBusySession(p Platform, msg *Message, interactiveKey string) bool {
	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[interactiveKey]
	e.interactiveMu.Unlock()

	if !hasState || state == nil {
		return false
	}
	// Allow queueing when agentSession is nil (session is starting up,
	// issue #565). Only reject if the session was established and died.
	if state.agentSession != nil && !state.agentSession.Alive() {
		return false
	}

	// Only queue metadata — do NOT send to agent stdin yet.
	// The agent CLI may treat a mid-turn stdin message as part of the
	// current turn, causing the event loop to hang waiting for a second
	// EventResult that never arrives. Instead, the event loop sends the
	// message after the current turn's EventResult is received.
	state.mu.Lock()
	if len(state.pendingMessages) >= e.maxQueuedMessages {
		depth := len(state.pendingMessages)
		state.mu.Unlock()
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgQueueFull), depth))
		return true // handled: queue-full reply sent
	}
	state.pendingMessages = append(state.pendingMessages, queuedMessage{
		messageID:     msg.MessageID,
		platform:      p,
		replyCtx:      msg.ReplyCtx,
		content:       msg.Content,
		images:        msg.Images,
		files:         msg.Files,
		fromVoice:     msg.FromVoice,
		userID:        msg.UserID,
		userName:      msg.UserName,
		msgPlatform:   msg.Platform,
		msgSessionKey: msg.SessionKey,
		channelKey:    msg.ChannelKey,
	})
	queueDepth := len(state.pendingMessages)
	state.mu.Unlock()

	slog.Info("message queued for busy session",
		"session", msg.SessionKey,
		"user", msg.UserName,
		"queue_depth", queueDepth,
	)
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgMessageQueued))
	return true
}

// takeQueuedBatchLocked pops the next batch of queued messages.
// merge == false pops exactly one message (serial mode, the historical
// behavior); merge == true pops the whole queue so everything accumulated
// during the previous turn is submitted as a single prompt.
// Caller must hold state.mu.
func takeQueuedBatchLocked(state *interactiveState, merge bool) []queuedMessage {
	pending := state.pendingMessages
	if len(pending) == 0 {
		return nil
	}
	n := 1
	if merge && len(pending) > 1 {
		n = len(pending)
	}
	batch := pending[:n]
	state.pendingMessages = pending[n:]
	return batch
}

// mergeQueuedBatch folds a FIFO batch of queued messages into a single
// submission. Identity fields (messageID, platform, replyCtx, sender
// fields) come from the LAST message so replies quote the newest bubble and
// recall detection targets it; contents are joined with blank lines; images
// and files are concatenated in order; fromVoice is true when any message
// was spoken. A single-message batch degenerates to exactly the historical
// per-message behavior. The prompt carries one sender header when all
// messages share the same sender; otherwise each message keeps its own
// header (shared-session channels may interleave multiple senders).
func (e *Engine) mergeQueuedBatch(batch []queuedMessage) (merged queuedMessage, prompt string) {
	if len(batch) == 0 {
		return merged, ""
	}
	merged = batch[len(batch)-1]
	merged.fromVoice = false
	sameSender := true
	var images []ImageAttachment
	var files []FileAttachment
	for _, q := range batch {
		if q.fromVoice {
			merged.fromVoice = true
		}
		images = append(images, q.images...)
		files = append(files, q.files...)
		if q.userID != batch[0].userID || q.userName != batch[0].userName ||
			q.msgPlatform != batch[0].msgPlatform || q.msgSessionKey != batch[0].msgSessionKey ||
			q.channelKey != batch[0].channelKey {
			sameSender = false
		}
	}
	merged.images = images
	merged.files = files

	if sameSender {
		contents := make([]string, 0, len(batch))
		for _, q := range batch {
			contents = append(contents, q.content)
		}
		joined := strings.Join(contents, "\n\n")
		return merged, e.buildSenderPrompt(joined, merged.userID, merged.userName, merged.msgPlatform, merged.msgSessionKey, merged.channelKey)
	}
	parts := make([]string, 0, len(batch))
	for _, q := range batch {
		parts = append(parts, e.buildSenderPrompt(q.content, q.userID, q.userName, q.msgPlatform, q.msgSessionKey, q.channelKey))
	}
	return merged, strings.Join(parts, "\n\n")
}

// ensureInteractiveStateForQueueing creates a placeholder interactiveState
// entry if none exists. This allows messages arriving while the agent session
// is still starting up to be queued instead of dropped (issue #565).
// The placeholder has agentSession==nil; getOrCreateInteractiveStateWith will
// replace it with a fully initialized state once the agent process is spawned.
func (e *Engine) ensureInteractiveStateForQueueing(key string, p Platform, replyCtx any) {
	e.interactiveMu.Lock()
	defer e.interactiveMu.Unlock()
	if _, ok := e.interactiveStates[key]; !ok {
		e.interactiveStates[key] = &interactiveState{
			platform:         p,
			replyCtx:         replyCtx,
			eventsNeedResync: true,
		}
	}
}

// drainOrphanedQueue is called when a message was queued but the drain loop
// has already exited. It processes all pending messages in the state, similar
// to the drain loop in processInteractiveMessageWith but as a standalone
// goroutine.
func (e *Engine) drainOrphanedQueue(session *Session, sessions *SessionManager, interactiveKey string, agent Agent, workspaceDir string) {
	unlocked := false
	defer func() {
		if !unlocked {
			session.Unlock()
		}
	}()

	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[interactiveKey]
	e.interactiveMu.Unlock()

	if !hasState || state == nil || state.agentSession == nil || !state.agentSession.Alive() {
		if hasState && state != nil {
			e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent session ended"))
		}
		return
	}

	// Stop unsolicited reader before draining — drainPendingMessages reads
	// from Events() and we must not have concurrent readers.
	e.stopUnsolicitedReader(state)

	unlocked = e.drainPendingMessages(state, session, sessions, interactiveKey)

	// Restart unsolicited reader if the session is still alive and clean.
	state.mu.Lock()
	alive := state.agentSession != nil && state.agentSession.Alive() && !state.stopped && !state.eventsNeedResync
	state.mu.Unlock()
	if alive {
		e.startUnsolicitedReader(state, session, sessions, interactiveKey, workspaceDir)
	}
}

// ──────────────────────────────────────────────────────────────
// Voice message handling
// ──────────────────────────────────────────────────────────────

func (e *Engine) handleVoiceMessage(p Platform, msg *Message) {
	if !e.speech.Enabled || e.speech.STT == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceNotEnabled))
		return
	}

	audio := msg.Audio
	if NeedsConversion(audio.Format) && !HasFFmpeg() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceNoFFmpeg))
		return
	}

	slog.Info("transcribing voice message",
		"platform", msg.Platform, "user", msg.UserName,
		"format", audio.Format, "size", len(audio.Data),
	)
	e.send(p, msg.ReplyCtx, e.i18n.T(MsgVoiceTranscribing))

	text, err := TranscribeAudio(e.ctx, e.speech.STT, audio, e.speech.Language)
	if err != nil {
		slog.Error("speech transcription failed",
			"error", err,
			"session_key", msg.SessionKey,
			"platform", p.Name(),
			"user", msg.UserID,
			"format", audio.Format,
			"size", len(audio.Data))
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgVoiceTranscribeFailed), err))
		return
	}

	text = strings.TrimSpace(text)
	if text == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgVoiceEmpty))
		return
	}

	slog.Info("voice transcribed", "text_len", len(text))
	e.send(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgVoiceTranscribed), text))

	// Replace audio with transcribed text and re-dispatch
	msg.Audio = nil
	msg.Content = text
	msg.FromVoice = true
	e.handleMessage(p, msg)
}

// ──────────────────────────────────────────────────────────────
// Permission handling
// ──────────────────────────────────────────────────────────────

func (e *Engine) handlePendingPermission(p Platform, msg *Message, content string) bool {
	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()
	if !ok || state == nil {
		return false
	}

	state.mu.Lock()
	pending := state.pending
	state.mu.Unlock()
	if pending == nil {
		return false
	}

	// AskUserQuestion: interpret user response as an answer, not a permission decision
	if len(pending.Questions) > 0 {
		curIdx := pending.CurrentQuestion
		q := pending.Questions[curIdx]
		answer := e.resolveAskQuestionAnswer(q, content)

		if pending.Answers == nil {
			pending.Answers = make(map[int]string)
		}
		pending.Answers[curIdx] = answer

		// More questions remaining — advance to next and send new card
		if curIdx+1 < len(pending.Questions) {
			pending.CurrentQuestion = curIdx + 1
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ %s: **%s**", q.Question, answer))
			e.sendAskQuestionPrompt(p, msg.ReplyCtx, pending.Questions, curIdx+1)
			return true
		}

		// All questions answered — build response and resolve
		updatedInput := buildAskQuestionResponse(pending.ToolInput, pending.Questions, pending.Answers)

		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior:     "allow",
			UpdatedInput: updatedInput,
		}); err != nil {
			slog.Error("failed to send AskUserQuestion response",
				"error", err,
				"session_key", msg.SessionKey,
				"platform", p.Name(),
				"user", msg.UserID,
				"request_id", pending.RequestID)
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		} else {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf("✅ %s: **%s**", q.Question, answer))
		}

		state.mu.Lock()
		state.pending = nil
		state.mu.Unlock()
		pending.resolve()
		return true
	}

	lower := strings.ToLower(strings.TrimSpace(content))

	if isApproveAllResponse(lower) {
		state.mu.Lock()
		state.approveAll = true
		state.mu.Unlock()

		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior:     "allow",
			UpdatedInput: pending.ToolInput,
		}); err != nil {
			slog.Error("failed to send permission response (approve-all)",
				"error", err,
				"session_key", msg.SessionKey,
				"platform", p.Name(),
				"user", msg.UserID,
				"request_id", pending.RequestID,
				"tool", pending.ToolName)
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionApproveAll))
		}
	} else if isAllowResponse(lower) {
		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior:     "allow",
			UpdatedInput: pending.ToolInput,
		}); err != nil {
			slog.Error("failed to send permission response (allow)",
				"error", err,
				"session_key", msg.SessionKey,
				"platform", p.Name(),
				"user", msg.UserID,
				"request_id", pending.RequestID,
				"tool", pending.ToolName)
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		} else {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionAllowed))
		}
	} else if isDenyResponse(lower) {
		if err := state.agentSession.RespondPermission(pending.RequestID, PermissionResult{
			Behavior: "deny",
			Message:  "User denied this tool use.",
		}); err != nil {
			slog.Error("failed to send deny response",
				"error", err,
				"session_key", msg.SessionKey,
				"platform", p.Name(),
				"user", msg.UserID,
				"request_id", pending.RequestID,
				"tool", pending.ToolName)
		}
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionDenied))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPermissionHint))
		return true
	}

	state.mu.Lock()
	state.pending = nil
	state.mu.Unlock()
	pending.resolve()

	return true
}

// resolveAskQuestionAnswer converts user input into answer text.
// It handles button callbacks ("askq:qIdx:optIdx"), numeric selections ("1", "1,3"), and free text.
func (e *Engine) resolveAskQuestionAnswer(q UserQuestion, input string) string {
	input = strings.TrimSpace(input)

	// Handle card button callback: "askq:qIdx:optIdx"
	if strings.HasPrefix(input, "askq:") {
		parts := strings.SplitN(input, ":", 3)
		if len(parts) == 3 {
			if idx, err := strconv.Atoi(parts[2]); err == nil && idx >= 1 && idx <= len(q.Options) {
				return q.Options[idx-1].Label
			}
		}
		// Legacy format "askq:N"
		if len(parts) == 2 {
			if idx, err := strconv.Atoi(parts[1]); err == nil && idx >= 1 && idx <= len(q.Options) {
				return q.Options[idx-1].Label
			}
		}
	}

	// Try numeric index(es)
	if q.MultiSelect {
		parts := strings.FieldsFunc(input, func(r rune) bool { return r == ',' || r == '，' || r == ' ' })
		var labels []string
		allNumeric := true
		for _, p := range parts {
			p = strings.TrimSpace(p)
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 1 || idx > len(q.Options) {
				allNumeric = false
				break
			}
			labels = append(labels, q.Options[idx-1].Label)
		}
		if allNumeric && len(labels) > 0 {
			return strings.Join(labels, ", ")
		}
	} else {
		if idx, err := strconv.Atoi(input); err == nil && idx >= 1 && idx <= len(q.Options) {
			return q.Options[idx-1].Label
		}
	}

	return input
}

// buildAskQuestionResponse constructs the updatedInput for AskUserQuestion control_response.
func buildAskQuestionResponse(originalInput map[string]any, questions []UserQuestion, collected map[int]string) map[string]any {
	result := make(map[string]any)
	for k, v := range originalInput {
		result[k] = v
	}
	answers := make(map[string]any)
	for idx, ans := range collected {
		if idx >= 0 && idx < len(questions) {
			answers[questions[idx].Question] = ans
		}
	}
	result["answers"] = answers
	return result
}

func isApproveAllResponse(s string) bool {
	for _, w := range []string{
		"allow all", "allowall", "approve all", "yes all",
		"允许所有", "允许全部", "全部允许", "所有允许", "都允许", "全部同意",
	} {
		if s == w {
			return true
		}
	}
	return false
}

func isAllowResponse(s string) bool {
	for _, w := range []string{"allow", "yes", "y", "ok", "允许", "同意", "可以", "好", "好的", "是", "确认", "approve"} {
		if s == w {
			return true
		}
	}
	return false
}

func isDenyResponse(s string) bool {
	for _, w := range []string{"deny", "no", "n", "reject", "拒绝", "不允许", "不行", "不", "否", "取消", "cancel"} {
		if s == w {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────
// Interactive agent processing
// ──────────────────────────────────────────────────────────────

func (e *Engine) processInteractiveMessage(p Platform, msg *Message, session *Session) {
	e.processInteractiveMessageWith(p, msg, session, e.agent, e.sessions, msg.SessionKey, "", "")
}

// processInteractiveMessageWith is the core interactive processing loop.
// It accepts an explicit agent, interactiveKey (for the interactiveStates map),
// and workspaceDir so that multi-workspace mode can route to per-workspace agents.
// ccSessionKey, when non-empty, is used for CC_SESSION_KEY in the agent env; otherwise interactiveKey is used.
func (e *Engine) processInteractiveMessageWith(p Platform, msg *Message, session *Session, agent Agent, sessions *SessionManager, interactiveKey string, workspaceDir string, ccSessionKey string) {
	// session.Unlock() is NOT deferred here — it is called explicitly in
	// the drain loop below while holding state.mu to close the race window
	// between "queue is empty" and "session unlocked". A deferred fallback
	// ensures the lock is released on early-return paths.
	unlocked := false
	defer func() {
		if !unlocked {
			session.Unlock()
		}
	}()

	if e.ctx.Err() != nil {
		return
	}

	turnStart := time.Now()

	e.i18n.DetectAndSet(msg.Content)
	session.AddHistory("user", msg.Content)

	// Use the agent override when available (multi-workspace mode)
	var agentOverride Agent
	if agent != e.agent {
		agentOverride = agent
	}
	state := e.getOrCreateInteractiveStateWith(interactiveKey, p, msg.ReplyCtx, session, sessions, agentOverride, ccSessionKey)

	// Set workspaceDir on the state for idle reaper identification
	if workspaceDir != "" {
		state.mu.Lock()
		state.workspaceDir = workspaceDir
		state.mu.Unlock()
	}

	// Update reply context for this turn
	state.mu.Lock()
	state.platform = p
	state.platformName = msg.Platform
	state.replyCtx = msg.ReplyCtx
	state.currentMessageID = msg.MessageID
	state.lastTurnMessageID = msg.MessageID
	state.mu.Unlock()
	stopRecallMonitor := e.startMessageRecallMonitor(interactiveKey)
	defer stopRecallMonitor()

	if state.agentSession == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgFailedToStartAgentSession))
		return
	}

	if workspaceDir != "" && e.workspacePool != nil {
		ws := e.workspacePool.GetOrCreate(workspaceDir)
		ws.BeginTurn()
		defer ws.EndTurn()
	}

	// Apply per-message permission mode override (e.g. cron jobs with mode = "bypassPermissions").
	// Defer restores only when SetLiveMode succeeds for the override.
	if msg.ModeOverride != "" {
		if switcher, ok := state.agentSession.(LiveModeSwitcher); ok {
			if switcher.SetLiveMode(msg.ModeOverride) {
				defer func() {
					defaultMode := "default"
					if ma, ok := e.agent.(interface{ GetMode() string }); ok {
						if m := ma.GetMode(); m != "" {
							defaultMode = m
						}
					}
					switcher.SetLiveMode(defaultMode)
				}()
			}
		}
	}

	// Start typing indicator if platform supports it.
	// Ownership is transferred to processInteractiveEvents which manages
	// stopping/restarting it across queued message turns.
	var stopTyping func()
	if ti, ok := p.(TypingIndicator); ok {
		stopTyping = ti.StartTyping(e.ctx, msg.ReplyCtx)
	}
	defer func() {
		// Stop typing if ownership was NOT transferred to processInteractiveEvents
		// (i.e. an early return before that call).
		if stopTyping != nil {
			stopTyping()
		}
	}()

	// Stop the unsolicited reader (if running) and hand off event channel
	// ownership to this foreground turn. Only drain events when the previous
	// turn ended abnormally (eventsNeedResync=true, the default).
	e.stopUnsolicitedReader(state)
	state.mu.Lock()
	needResync := state.eventsNeedResync
	staleForMessageID := state.lastTurnMessageID
	state.mu.Unlock()
	if needResync {
		drained := drainEvents(state.agentSession.Events())
		if drained > 0 {
			slog.Warn("dropped buffered events before starting turn", "previous_message_id", staleForMessageID, "count", drained, "new_message_id", msg.MessageID)
		}
	}

	promptContent := e.buildSenderPrompt(msg.Content, msg.UserID, msg.UserName, msg.Platform, msg.SessionKey, msg.ChannelKey)

	sendStart := time.Now()
	state.mu.Lock()
	state.currentMessageID = msg.MessageID
	state.fromVoice = msg.FromVoice
	state.sideText = ""
	state.lastTurnMessageID = msg.MessageID
	state.mu.Unlock()

	// Run Send concurrently with processInteractiveEvents. Some agents block inside
	// Send until the prompt turn finishes (e.g. ACP session/prompt); they may emit
	// EventPermissionRequest while blocked — the event loop must run in parallel.
	sendDone := make(chan error, 1)
	go func() {
		sendDone <- state.agentSession.Send(promptContent, msg.Images, msg.Files)
	}()

	e.processInteractiveEvents(state, session, sessions, interactiveKey, msg.MessageID, turnStart, stopTyping, sendDone, msg.ReplyCtx)
	if elapsed := time.Since(sendStart); elapsed >= slowAgentSend {
		slog.Warn("slow agent send", "elapsed", elapsed, "session", msg.SessionKey, "content_len", len(msg.Content))
	}
	stopTyping = nil // ownership transferred; prevent defer from double-stopping

	// Guard against a narrow race: a message may have been queued between
	// processInteractiveEvents observing an empty queue and returning here
	// (session is still locked, so handleMessage's TryLock fails and routes
	// the message to queueMessageForBusySession). Drain any such orphans.
	if e.drainPendingMessages(state, session, sessions, interactiveKey) {
		unlocked = true
	}

	// Start unsolicited reader if the session is still alive and the last
	// turn ended cleanly. This goroutine will consume agent-initiated events
	// (e.g. background task completions) and relay them to the platform.
	state.mu.Lock()
	alive := state.agentSession != nil && state.agentSession.Alive() && !state.stopped && !state.eventsNeedResync
	state.mu.Unlock()
	if alive {
		e.startUnsolicitedReader(state, session, sessions, interactiveKey, workspaceDir)
	}
}

// getOrCreateWorkspaceAgent returns (or creates) a per-workspace agent and session manager.
// workspace must be a normalized path (from resolveWorkspace or normalizeWorkspacePath).
func (e *Engine) getOrCreateWorkspaceAgent(workspace string) (Agent, *SessionManager, error) {
	if e.workspacePool == nil {
		return nil, nil, fmt.Errorf("workspace pool not initialized (multi-workspace mode not enabled)")
	}
	ws := e.workspacePool.GetOrCreate(workspace)
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.agent != nil {
		return ws.agent, ws.sessions, nil
	}

	// Create a new agent instance with this workspace's work_dir
	opts := make(map[string]any)
	if snapshotter, ok := e.agent.(WorkspaceAgentOptionSnapshotter); ok {
		for k, v := range snapshotter.WorkspaceAgentOptions() {
			opts[k] = v
		}
	}
	opts["work_dir"] = workspace

	// Copy model from original agent if possible
	if _, ok := opts["model"]; !ok {
		if ma, ok := e.agent.(interface{ GetModel() string }); ok {
			if m := ma.GetModel(); m != "" {
				opts["model"] = m
			}
		}
	}
	// Copy permission mode
	if _, ok := opts["mode"]; !ok {
		if ma, ok := e.agent.(interface{ GetMode() string }); ok {
			if m := ma.GetMode(); m != "" {
				opts["mode"] = m
			}
		}
	}
	// Copy run_as_user (and run_as_env) for OS-level isolation. Without
	// this, per-workspace agents silently bypass the project-level
	// run_as_user config because their opts map is freshly constructed
	// above, not inherited from the project-level opts that main.go
	// already decorated. See heron-connect#496 and the heron-connect/core/runas.go
	// preamble for why run_as_user has to survive this copy.
	if _, ok := opts["run_as_user"]; !ok {
		if ma, ok := e.agent.(interface{ GetRunAsUser() string }); ok {
			if u := ma.GetRunAsUser(); u != "" {
				opts["run_as_user"] = u
			}
		}
	}
	if _, ok := opts["run_as_env"]; !ok {
		if ma, ok := e.agent.(interface{ GetRunAsEnv() []string }); ok {
			if env := ma.GetRunAsEnv(); len(env) > 0 {
				opts["run_as_env"] = env
			}
		}
	}

	agent, err := CreateAgent(e.agent.Name(), opts)
	if err != nil {
		return nil, nil, fmt.Errorf("create workspace agent for %s: %w", workspace, err)
	}

	// Wire providers if original agent has them
	if ps, ok := e.agent.(ProviderSwitcher); ok {
		if ps2, ok2 := agent.(ProviderSwitcher); ok2 {
			ps2.SetProviders(ps.ListProviders())
			if active := ps.GetActiveProvider(); active != nil && active.Name != "" {
				ps2.SetActiveProvider(active.Name)
			}
		}
	}

	// Create per-workspace session manager
	h := sha256.Sum256([]byte(workspace))
	sessionFile := filepath.Join(filepath.Dir(e.sessions.StorePath()),
		fmt.Sprintf("%s_ws_%s.json", e.name, hex.EncodeToString(h[:4])))
	sessions := NewSessionManager(sessionFile)

	ws.agent = agent
	ws.sessions = sessions
	return agent, sessions, nil
}

func (e *Engine) resolveChannelWorkDir(workspace, interactiveKey string) string {
	if e.projectState == nil {
		return workspace
	}
	override := e.projectState.WorkspaceDirOverride(interactiveKey)
	if override == "" {
		return workspace
	}
	if info, err := os.Stat(override); err == nil && info.IsDir() {
		return override
	}
	e.projectState.ClearWorkspaceDirOverride(interactiveKey)
	e.projectState.Save()
	return workspace
}

func (e *Engine) workspaceContext(workspace, sessionKey string) (Agent, *SessionManager, string, string, error) {
	interactiveKey := workspace + ":" + sessionKey
	effectiveDir := e.resolveChannelWorkDir(workspace, interactiveKey)
	wsAgent, wsSessions, err := e.getOrCreateWorkspaceAgent(effectiveDir)
	if err != nil {
		return nil, nil, "", "", err
	}
	return wsAgent, wsSessions, interactiveKey, effectiveDir, nil
}

// getOrCreateInteractiveStateWith accepts an optional agent override for multi-workspace mode.
// adoptPendingFromPlaceholder copies pendingMessages from an existing placeholder
// state to newState so queued messages are not lost when the map entry is replaced.
// Must be called under interactiveMu.
func adoptPendingFromPlaceholder(existing, newState *interactiveState) {
	if existing == nil || existing == newState {
		return
	}
	existing.mu.Lock()
	if len(existing.pendingMessages) > 0 {
		newState.pendingMessages = existing.pendingMessages
		existing.pendingMessages = nil
	}
	existing.mu.Unlock()
}

// When agentOverride is non-nil it is used instead of e.agent to start the session.
// ccSessionKey, when non-empty, is used for CC_SESSION_KEY env injection; otherwise sessionKey is used.
func (e *Engine) getOrCreateInteractiveStateWith(sessionKey string, p Platform, replyCtx any, session *Session, sessions *SessionManager, agentOverride Agent, ccSessionKey string) *interactiveState {
	// Track whether we hold the lock so we can release it before blocking
	// operations (closeAgentSessionWithTimeout can block up to 130s) and
	// avoid holding it during the entire function via a single defer.
	locked := true
	e.interactiveMu.Lock()
	unlock := func() {
		if locked {
			e.interactiveMu.Unlock()
			locked = false
		}
	}
	defer unlock()

	state, ok := e.interactiveStates[sessionKey]
	if ok && state.agentSession != nil && state.agentSession.Alive() {
		// Verify the running agent session matches the current active session.
		// After /new or /switch the active session changes, but the old agent
		// process may still be alive. Reusing it would send messages to the
		// wrong conversation context.
		wantID := session.GetAgentSessionID()
		currentID := state.agentSession.CurrentSessionID()
		// Reuse only when the live process matches what the Session expects:
		// - IDs match (same Claude session), or
		// - the process has not reported an ID yet (startup; empty want is OK).
		// If wantID is empty (/new, cleared session) but the process already has
		// a concrete ID, reusing would keep --resume context — recycle (#238).
		needRecycle := currentID != "" && (wantID == "" || wantID != currentID)
		if !needRecycle {
			return state
		}
		// Tear down the stale agent so we start one that matches the Session below.
		slog.Info("interactive session mismatch, recycling",
			"session_key", sessionKey,
			"want_agent_session", wantID,
			"have_agent_session", currentID,
		)
		e.stopUnsolicitedReader(state)
		state.markStopped()
		// Snapshot the agent session and delete from map before releasing
		// the lock. The actual close (which can block up to 130s for Stop
		// hooks) runs outside the critical section so other sessions are
		// not blocked.
		agentToClose := state.agentSession
		delete(e.interactiveStates, sessionKey)
		unlock()

		if agentToClose != nil {
			// Close synchronously to prevent race condition where old agent
			// continues outputting while new agent starts (issue #327).
			e.closeAgentSessionWithTimeout(sessionKey, agentToClose)
		}

		e.interactiveMu.Lock()
		locked = true
		ok = false // prevent reading stale settings below
	} else if ok && state != nil && (state.agentSession == nil || !state.agentSession.Alive()) {
		// Defensive cleanup: a previous session died but was never cleaned
		// up (e.g. the EventError path in processInteractiveEvents didn't
		// run cleanupInteractiveState, or the process died without emitting
		// an error). Without this, the dead state would be silently
		// overwritten below and Close() would never be called on the old
		// agent session — leaking the subprocess (issue: orphaned --acp
		// processes accumulating as PPID=1).
		slog.Warn("found dead agent session on new message, cleaning up defensively",
			"session_key", sessionKey,
			"agent_session_nil", state.agentSession == nil)
		e.stopUnsolicitedReader(state)
		state.markStopped()
		state.mu.Lock()
		pending := state.pending
		state.pending = nil
		state.mu.Unlock()
		if pending != nil {
			pending.resolve()
		}
		agentToClose := state.agentSession
		delete(e.interactiveStates, sessionKey)
		unlock()

		if agentToClose != nil {
			e.closeAgentSessionWithTimeout(sessionKey, agentToClose)
		}

		e.interactiveMu.Lock()
		locked = true
		ok = false
	}

	// Select the agent to use for this session
	agent := e.agent
	if agentOverride != nil {
		agent = agentOverride
	}

	ccKey := sessionKey
	if ccSessionKey != "" {
		ccKey = ccSessionKey
	}

	// Inject per-session env vars so the agent subprocess can call `heron-connect cron add` etc.
	if inj, ok := agent.(SessionEnvInjector); ok {
		envVars := []string{
			"CC_PROJECT=" + e.name,
			"CC_SESSION_KEY=" + ccKey,
		}
		if exePath, err := os.Executable(); err == nil {
			binDir := filepath.Dir(exePath)
			if curPath := os.Getenv("PATH"); curPath != "" {
				envVars = append(envVars, "PATH="+binDir+string(filepath.ListSeparator)+curPath)
			} else {
				envVars = append(envVars, "PATH="+binDir)
			}
		}
		inj.SetSessionEnv(envVars)
	}

	// Inject platform-specific formatting instructions into the agent's system prompt.
	// Clear the prompt first so instructions from a previous platform don't leak
	// into sessions for platforms that don't provide their own instructions.
	if ppi, ok := agent.(PlatformPromptInjector); ok {
		prompt := ""
		if fip, ok := p.(FormattingInstructionProvider); ok {
			prompt = fip.FormattingInstructions()
		}
		ppi.SetPlatformPrompt(prompt)
	}

	// Check if context is already canceled (e.g. during shutdown/restart)
	if e.ctx.Err() != nil {
		slog.Debug("skipping session start: context canceled", "session_key", sessionKey)
		newState := &interactiveState{platform: p, replyCtx: replyCtx, agent: agent, eventsNeedResync: true}
		adoptPendingFromPlaceholder(e.interactiveStates[sessionKey], newState)
		state = newState
		e.interactiveStates[sessionKey] = state
		return state
	}

	// Resume only when we have a concrete saved agent session ID. If the session
	// is unbound, force a fresh start instead of attaching to whichever CLI
	// conversation happens to be "latest" in this workspace.
	startSessionID := session.GetAgentSessionID()
	isResume := startSessionID != ""
	startAt := time.Now()
	agentSession, err := agent.StartSession(e.ctx, startSessionID)
	startElapsed := time.Since(startAt)
	if err != nil {
		// If resume/continue failed, try a fresh session as fallback.
		if startSessionID != "" {
			slog.Error("session resume failed, falling back to fresh session",
				"session_key", sessionKey, "failed_session_id", startSessionID,
				"error", err, "elapsed", startElapsed)
			startAt = time.Now()
			agentSession, err = agent.StartSession(e.ctx, "")
			startElapsed = time.Since(startAt)
			if err == nil {
				slog.Info("fresh session started after resume failure",
					"session_key", sessionKey, "elapsed", startElapsed)
			}
		}
		if err != nil {
			slog.Error("failed to start interactive session",
				"error", err,
				"session_key", sessionKey,
				"platform", p.Name(),
				"agent", e.agent.Name(),
				"elapsed", startElapsed)
			e.hooks.Emit(HookEvent{
				Event:      HookEventError,
				SessionKey: sessionKey,
				Platform:   p.Name(),
				Error:      fmt.Sprintf("failed to start session: %v", err),
			})
			newState := &interactiveState{platform: p, replyCtx: replyCtx, agent: agent, eventsNeedResync: true}
			adoptPendingFromPlaceholder(e.interactiveStates[sessionKey], newState)
			state = newState
			e.interactiveStates[sessionKey] = state
			return state
		}
	}
	if startElapsed >= slowAgentStart {
		slog.Warn("slow agent session start", "elapsed", startElapsed, "agent", agent.Name(), "session_id", startSessionID)
	}

	if newID := agentSession.CurrentSessionID(); newID != "" {
		// ACP-like adapters already know the concrete session/thread id at spawn
		// time and may rotate it on resume. Such sessions implement SessionIDRotator
		// so we refresh the persisted binding unconditionally, replacing any stale
		// resume id with the fresh backend-assigned id.
		// Sessions that do NOT implement SessionIDRotator (e.g. plain --resume)
		// keep their existing persisted id via Compare-and-Set: the new id is only
		// stored when the persisted slot is empty or holds the ContinueSession sentinel.
		if rotator, ok := agentSession.(SessionIDRotator); ok && rotator.RotatesSessionIDOnSpawn() {
			session.SetAgentSessionID(newID, agent.Name())
		} else if !session.CompareAndSetAgentSessionID(newID, agent.Name()) {
			// CompareAndSet returned false: a real session id was already stored
			// and the session does not rotate ids. Keep the existing id.
		}
		pendingName := session.GetName()
		if pendingName != "" && pendingName != "session" && pendingName != "default" {
			sessions.SetSessionName(newID, pendingName)
		}
		sessions.Save()
	}

	newState := &interactiveState{
		agentSession:     agentSession,
		platform:         p,
		replyCtx:         replyCtx,
		agent:            agent,
		eventsNeedResync: true,
	}
	adoptPendingFromPlaceholder(e.interactiveStates[sessionKey], newState)
	state = newState
	e.interactiveStates[sessionKey] = state

	slog.Info("session spawned", "session_key", sessionKey, "agent_session", session.GetAgentSessionID(), "is_resume", isResume, "elapsed", startElapsed)

	e.hooks.Emit(HookEvent{
		Event:      HookEventSessionStarted,
		SessionKey: sessionKey,
		Platform:   p.Name(),
		Extra: map[string]any{
			"agent_session_id": session.GetAgentSessionID(),
			"is_resume":        isResume,
		},
	})

	return state
}

// cleanupInteractiveState removes the interactive state for the given session key
// and closes its agent session. When an expected state is provided, cleanup is
// skipped if the map entry has been replaced by a different state — this prevents
// a stale goroutine (still running after /new created a fresh Session object and
// a new turn started on it) from accidentally destroying the replacement state.
//
// IMPORTANT: The state is deleted from the map AFTER the agent session is closed
// to avoid race conditions where concurrent requests see an empty map while the
// agent session is still being shut down (which can take up to 130s for Stop hooks).
func (e *Engine) cleanupInteractiveState(sessionKey string, expected ...*interactiveState) {
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[sessionKey]
	if len(expected) > 0 && expected[0] != nil && state != expected[0] {
		// Another turn has already replaced the state — skip cleanup.
		e.interactiveMu.Unlock()
		return
	}
	// Capture the agent session and nil it out atomically to prevent a
	// concurrent cleanup (without expected) from closing the same session.
	var agentSession AgentSession
	if ok && state != nil {
		state.mu.Lock()
		agentSession = state.agentSession
		state.agentSession = nil
		state.mu.Unlock()
	}
	e.interactiveMu.Unlock()

	// Notify senders of any queued messages that will never be processed.
	if ok && state != nil {
		// Stop unsolicited reader before marking stopped to avoid goroutine leak.
		e.stopUnsolicitedReader(state)

		state.markStopped()

		// Resolve any pending permission so the reader goroutine (or event
		// loop) does not block on <-pending.Resolved forever.
		state.mu.Lock()
		pending := state.pending
		state.pending = nil
		state.mu.Unlock()
		if pending != nil {
			pending.resolve()
		}

		e.notifyDroppedQueuedMessages(state, fmt.Errorf("session reset"))
	}

	// Close the agent session BEFORE deleting from the map.
	// This prevents race conditions where /stop during cleanup sees
	// an empty map and reports "No execution in progress" while
	// the agent session Close() is still blocking (up to 130s).
	if agentSession != nil {
		e.closeAgentSessionWithTimeout(sessionKey, agentSession)
	}

	// Now delete the state from the map after the session is closed.
	e.interactiveMu.Lock()
	// Re-check that the state hasn't been replaced during the close
	currentState, currentOk := e.interactiveStates[sessionKey]
	if currentOk && len(expected) > 0 && expected[0] != nil && currentState != expected[0] {
		// Another turn has replaced the state during our close — don't delete it.
		e.interactiveMu.Unlock()
		return
	}
	delete(e.interactiveStates, sessionKey)
	e.interactiveMu.Unlock()
}

// ForceNewSession creates a brand-new session for sessionKey, tearing down
// any live interactive/agent state first. Mirrors the /new command's
// behavior but is callable from non-message contexts (e.g. the web admin
// HTTP API) that have no Platform/Message to route through cmdNew. Returns
// the newly created session.
func (e *Engine) ForceNewSession(sessionKey, name string) *Session {
	e.cleanupInteractiveState(sessionKey)

	old := e.sessions.GetOrCreateActive(sessionKey)
	old.DetachAgentSession()

	newSession := e.sessions.NewSession(sessionKey, name)
	e.sessions.Save()
	return newSession
}

func (e *Engine) closeAgentSessionAsync(sessionKey string, agentSession AgentSession) {
	if agentSession == nil {
		return
	}
	go e.closeAgentSessionWithTimeout(sessionKey, agentSession)
}

func (e *Engine) closeAgentSessionWithTimeout(sessionKey string, agentSession AgentSession) {
	if agentSession == nil {
		return
	}

	// Allow enough time for the agent's own graceful shutdown sequence:
	// stdin close → Stop hooks (claude-mem summary etc.) → SIGTERM → SIGKILL.
	// Claude Code's Stop hooks can take up to 120s (claude-mem uses a
	// sonnet summarizer). The 130s budget covers the default 120s graceful
	// phase + 5s SIGTERM + 5s buffer. The wait ends early if the process
	// exits sooner — this is the ceiling, not the typical duration.
	const closeTimeout = 130 * time.Second

	slog.Debug("cleanupInteractiveState: closing agent session", "session", sessionKey)
	closeStart := time.Now()

	done := make(chan struct{})
	go func() {
		agentSession.Close()
		close(done)
	}()

	select {
	case <-done:
		if elapsed := time.Since(closeStart); elapsed >= slowAgentClose {
			slog.Warn("slow agent session close", "elapsed", elapsed, "session", sessionKey)
		}
	case <-time.After(closeTimeout):
		slog.Error("agent session close timed out, abandoning",
			"timeout", closeTimeout, "session", sessionKey)
	}
}

func (e *Engine) cmdPs(p Platform, msg *Message, args []string) {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPsEmpty))
		return
	}
	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()
	if !ok || state == nil || state.agentSession == nil || !state.agentSession.Alive() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPsNoSession))
		return
	}
	// /ps is only meaningful as a supplement to a turn already in flight.
	// When the session is idle, injecting via agentSession.Send bypasses the
	// session lock and races with concurrent normal messages on the CLI's
	// stdin, so reject instead.
	_, sessions := e.sessionContextForKey(msg.SessionKey)
	if session := sessions.GetOrCreateActive(msg.SessionKey); !session.Busy() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPsNoSession))
		return
	}
	if err := state.agentSession.Send(text, nil, nil); err != nil {
		slog.Error("ps: send failed",
			"error", err,
			"session_key", msg.SessionKey,
			"platform", p.Name(),
			"user", msg.UserID)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPsSendFailed))
		return
	}
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPsSent))
}

// matchPrefix finds a unique command matching the given prefix.
// Returns the command id or "" if no match / ambiguous.
func matchPrefix(prefix string, candidates []struct {
	names []string
	id    string
}) string {
	// Exact match first
	for _, c := range candidates {
		for _, n := range c.names {
			if prefix == n {
				return c.id
			}
		}
	}
	// Prefix match
	var matched string
	for _, c := range candidates {
		for _, n := range c.names {
			if strings.HasPrefix(n, prefix) {
				if matched != "" && matched != c.id {
					return "" // ambiguous
				}
				matched = c.id
				break
			}
		}
	}
	return matched
}

// matchSubCommand does prefix matching against a flat list of subcommand names.
func matchSubCommand(input string, candidates []string) string {
	for _, c := range candidates {
		if input == c {
			return c
		}
	}
	var matched string
	for _, c := range candidates {
		if strings.HasPrefix(c, input) {
			if matched != "" {
				return input // ambiguous → return raw input (will hit default)
			}
			matched = c
		}
	}
	if matched != "" {
		return matched
	}
	return input
}

func (e *Engine) handleCommand(p Platform, msg *Message, raw string) bool {
	parts := strings.Fields(raw)
	cmd := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	args := parts[1:]

	cmdID := matchPrefix(cmd, builtinCommands)

	// Resolve effective disabled commands: role-based if available, else project-level
	e.userRolesMu.RLock()
	disabledCmds := e.disabledCmds
	urm := e.userRoles
	e.userRolesMu.RUnlock()
	if urm != nil {
		if role := urm.ResolveRole(msg.UserID); role != nil {
			disabledCmds = role.DisabledCmds
		}
	}

	if cmdID != "" && disabledCmds[cmdID] {
		slog.Info("audit: command_blocked",
			"user_id", msg.UserID, "platform", msg.Platform,
			"project", e.name, "command", cmdID, "reason", "disabled")
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCommandDisabled), "/"+cmdID))
		return true
	}

	if cmdID != "" && privilegedCommands[cmdID] && !e.isAdmin(msg.UserID) {
		slog.Info("audit: command_blocked",
			"user_id", msg.UserID, "platform", msg.Platform,
			"project", e.name, "command", cmdID, "reason", "unauthorized")
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAdminRequired), "/"+cmdID))
		return true
	}

	if cmdID != "" {
		slog.Info("audit: command_executed",
			"user_id", msg.UserID, "platform", msg.Platform,
			"project", e.name, "command", cmdID)
	}

	switch cmdID {
	case "new":
		e.cmdNew(p, msg, args)
	case "list":
		e.cmdList(p, msg, args)
	case "switch":
		e.cmdSwitch(p, msg, args)
	case "name":
		e.cmdName(p, msg, args)
	case "current":
		e.cmdCurrent(p, msg)
	case "status":
		e.cmdStatus(p, msg)
	case "usage":
		e.cmdUsage(p, msg)
	case "history":
		e.cmdHistory(p, msg, args)
	case "allow":
		e.cmdAllow(p, msg, args)
	case "model":
		e.cmdModel(p, msg, args)
	case "reasoning":
		e.cmdReasoning(p, msg, args)
	case "mode":
		e.cmdMode(p, msg, args)
	case "lang":
		e.cmdLang(p, msg, args)
	case "quiet":
		e.cmdQuiet(p, msg, args)
	case "provider":
		e.cmdProvider(p, msg, args)
	case "memory":
		e.cmdMemory(p, msg, args)
	case "cron":
		e.cmdCron(p, msg, args)
	case "heartbeat":
		e.cmdHeartbeat(p, msg, args)
	case "compress":
		e.cmdCompress(p, msg)
	case "cancel":
		e.cmdCancel(p, msg)
	case "stop":
		e.cmdStop(p, msg)
	case "help":
		e.cmdHelp(p, msg)
	case "start":
		e.cmdStart(p, msg)
	case "version":
		e.reply(p, msg.ReplyCtx, VersionInfo)
	case "commands":
		e.cmdCommands(p, msg, args)
	case "skills":
		e.cmdSkills(p, msg)
	case "config":
		e.cmdConfig(p, msg, args)
	case "doctor":
		e.cmdDoctor(p, msg)
	case "upgrade":
		e.cmdUpgrade(p, msg, args)
	case "restart":
		e.cmdRestart(p, msg)
	case "alias":
		e.cmdAlias(p, msg, args)
	case "delete":
		e.cmdDelete(p, msg, args)
	case "bind":
		e.cmdBind(p, msg, args)
	case "search":
		e.cmdSearch(p, msg, args)
	case "shell":
		e.cmdShell(p, msg, raw)
	case "diff":
		e.cmdDiff(p, msg, raw)
	case "show":
		e.cmdShow(p, msg, args)
	case "dir":
		e.cmdDir(p, msg, args)
	case "tts":
		e.cmdTTS(p, msg, args)
	case "workspace":
		if !e.multiWorkspace {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNotEnabled))
			return true
		}
		e.handleWorkspaceCommand(p, msg, args)
		return true
	case "whoami":
		e.cmdWhoami(p, msg)
	case "web":
		e.cmdWeb(p, msg, args)
	case "ps":
		e.cmdPs(p, msg, args)
	default:
		if custom, ok := e.commands.Resolve(cmd); ok {
			if disabledCmds[strings.ToLower(custom.Name)] {
				slog.Info("audit: command_blocked",
					"user_id", msg.UserID, "platform", msg.Platform,
					"project", e.name, "command", custom.Name, "reason", "disabled")
				e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCommandDisabled), "/"+custom.Name))
				return true
			}
			slog.Info("audit: command_executed",
				"user_id", msg.UserID, "platform", msg.Platform,
				"project", e.name, "command", custom.Name, "type", "custom")
			e.executeCustomCommand(p, msg, custom, args)
			return true
		}
		if skill := e.skills.Resolve(cmd); skill != nil {
			if disabledCmds[strings.ToLower(skill.Name)] {
				slog.Info("audit: command_blocked",
					"user_id", msg.UserID, "platform", msg.Platform,
					"project", e.name, "command", skill.Name, "reason", "disabled")
				e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCommandDisabled), "/"+skill.Name))
				return true
			}
			slog.Info("audit: command_executed",
				"user_id", msg.UserID, "platform", msg.Platform,
				"project", e.name, "command", skill.Name, "type", "skill")
			e.executeSkill(p, msg, skill, args)
			return true
		}
		// Not a heron-connect command — notify user, then fall through to agent
		e.send(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgUnknownCommand), "/"+cmd))
		return false
	}
	return true
}

func (e *Engine) handleWorkspaceCommand(p Platform, msg *Message, args []string) {
	channelID := effectiveChannelID(msg)
	channelKey := effectiveWorkspaceChannelKey(msg)
	projectKey := "project:" + e.name
	resolveChannelName := func() func() string {
		resolved := false
		channelName := ""
		return func() string {
			if resolved {
				return channelName
			}
			resolved = true
			if resolver, ok := p.(ChannelNameResolver); ok {
				channelName, _ = resolver.ResolveChannelName(channelID)
			}
			return channelName
		}
	}()
	replyWorkspaceInfo := func(b *WorkspaceBinding, bindingKey string) {
		if bindingKey == sharedWorkspaceBindingsKey {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsInfoShared, b.Workspace, b.BoundAt.Format(time.RFC3339)))
			return
		}
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsInfo, b.Workspace, b.BoundAt.Format(time.RFC3339)))
	}
	routeWorkspace := func(bindingKey string, pathParts []string, usageKey, successKey MsgKey) bool {
		routePath := strings.TrimSpace(strings.Join(pathParts, " "))
		if routePath == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.T(usageKey))
			return false
		}
		if !filepath.IsAbs(routePath) {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsRouteAbsoluteRequired, routePath))
			return false
		}

		info, err := os.Stat(routePath)
		if err != nil {
			if os.IsNotExist(err) {
				e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsRouteNotFound, routePath))
			} else {
				e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
			}
			return false
		}
		if !info.IsDir() {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsRouteNotDirectory, routePath))
			return false
		}

		normalizedPath := normalizeWorkspacePath(routePath)
		e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizedPath)
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, normalizedPath))
		return true
	}
	bindWorkspace := func(bindingKey, wsName string, successKey MsgKey) bool {
		wsPath := filepath.Join(e.baseDir, wsName)

		// Check if workspace directory exists
		if _, err := os.Stat(wsPath); os.IsNotExist(err) {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsBindNotFound, wsName))
			return false
		}

		e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizeWorkspacePath(wsPath))
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, wsName))
		return true
	}
	initWorkspace := func(bindingKey, target string, successKey MsgKey) bool {
		// Support local directory paths (absolute or relative to baseDir).
		if looksLikeLocalDir(target) {
			dirPath, err := resolveLocalDirPath(target, e.baseDir)
			if err != nil {
				e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsInitDirNotFound, target))
				return false
			}
			info, statErr := os.Stat(dirPath)
			if statErr != nil || !info.IsDir() {
				e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsInitDirNotFound, target))
				return false
			}
			e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizeWorkspacePath(dirPath))
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, dirPath))
			return true
		}

		if !looksLikeGitURL(target) {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsInitInvalidTarget))
			return false
		}

		repoName := extractRepoName(target)
		cloneTo := filepath.Join(e.baseDir, repoName)

		if _, err := os.Stat(cloneTo); err == nil {
			e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizeWorkspacePath(cloneTo))
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, cloneTo))
			return true
		}

		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsCloneProgress, target))

		if err := gitClone(target, cloneTo); err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsCloneFailed, err))
			return false
		}

		e.workspaceBindings.Bind(bindingKey, channelKey, resolveChannelName(), normalizeWorkspacePath(cloneTo))
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(successKey, cloneTo))
		return true
	}
	listBindings := func(bindingKey string, emptyKey, titleKey MsgKey) {
		bindings := e.workspaceBindings.ListByProject(bindingKey)
		if len(bindings) == 0 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(emptyKey))
			return
		}
		var sb strings.Builder
		sb.WriteString(e.i18n.T(titleKey) + "\n")
		for chID, b := range bindings {
			name := b.ChannelName
			if name == "" {
				name = chID
			}
			sb.WriteString(fmt.Sprintf("• #%s → `%s`\n", name, b.Workspace))
		}
		e.reply(p, msg.ReplyCtx, sb.String())
	}

	subCmd := ""
	if len(args) > 0 {
		subCmd = matchSubCommand(args[0], []string{"init", "bind", "route", "unbind", "list", "shared"})
	}

	switch subCmd {
	case "":
		b, bindingKey, usable := e.lookupEffectiveWorkspaceBinding(channelKey)
		if !usable {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNoBinding))
		} else {
			replyWorkspaceInfo(b, bindingKey)
		}

	case "bind":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsBindUsage))
			return
		}
		bindWorkspace(projectKey, args[1], MsgWsBindSuccess)

	case "route":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsRouteUsage))
			return
		}
		routeWorkspace(projectKey, args[1:], MsgWsRouteUsage, MsgWsRouteSuccess)

	case "init":
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsInitUsage))
			return
		}
		initWorkspace(projectKey, args[1], MsgWsCloneSuccess)

	case "shared":
		sharedSubCmd := ""
		if len(args) > 1 {
			sharedSubCmd = matchSubCommand(args[1], []string{"init", "bind", "route", "unbind", "list"})
		}
		switch sharedSubCmd {
		case "":
			b := e.workspaceBindings.Lookup(sharedWorkspaceBindingsKey, channelKey)
			if b == nil {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedNoBinding))
			} else {
				replyWorkspaceInfo(b, sharedWorkspaceBindingsKey)
			}
			return
		case "bind":
			if len(args) < 3 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUsage))
				return
			}
			bindWorkspace(sharedWorkspaceBindingsKey, args[2], MsgWsSharedBindSuccess)
			return
		case "route":
			if len(args) < 3 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUsage))
				return
			}
			routeWorkspace(sharedWorkspaceBindingsKey, args[2:], MsgWsSharedUsage, MsgWsSharedRouteSuccess)
			return
		case "init":
			if len(args) < 3 {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUsage))
				return
			}
			initWorkspace(sharedWorkspaceBindingsKey, args[2], MsgWsSharedBindSuccess)
			return
		case "unbind":
			if e.workspaceBindings.Lookup(sharedWorkspaceBindingsKey, channelKey) == nil {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedNoBinding))
				return
			}
			e.workspaceBindings.Unbind(sharedWorkspaceBindingsKey, channelKey)
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUnbindSuccess))
			return
		case "list":
			listBindings(sharedWorkspaceBindingsKey, MsgWsSharedListEmpty, MsgWsSharedListTitle)
			return
		default:
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedUsage))
			return
		}

	case "unbind":
		if e.workspaceBindings.Lookup(projectKey, channelKey) == nil {
			if e.workspaceBindings.Lookup(sharedWorkspaceBindingsKey, channelKey) != nil {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsSharedOnlyHint))
			} else {
				e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsNoBinding))
			}
			return
		}
		e.workspaceBindings.Unbind(projectKey, channelKey)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsUnbindSuccess))

	case "list":
		listBindings(projectKey, MsgWsListEmpty, MsgWsListTitle)

	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgWsUsage))
	}
}

// sessionReapInterval is how often the session reaper scans for idle sessions.
const sessionReapInterval = 5 * time.Minute

// startSessionReaper launches a background goroutine that periodically scans
// interactiveStates and cleans up sessions whose agent process has been idle
// (no agent events) for longer than resetOnIdle.
func (e *Engine) startSessionReaper() {
	if e.resetOnIdle <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(e.ctx)
	e.sessionReapCancel = cancel

	go func() {
		ticker := time.NewTicker(sessionReapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.reapIdleSessions()
			}
		}
	}()
}

// reapIdleSessions scans interactiveStates and cleans up any session whose
// agent process has been idle (no events) for longer than resetOnIdle.
func (e *Engine) reapIdleSessions() {
	if e.resetOnIdle <= 0 {
		return
	}

	type reapTarget struct {
		key   string
		state *interactiveState
	}

	var targets []reapTarget
	e.interactiveMu.Lock()
	for key, state := range e.interactiveStates {
		if state == nil || state.agentSession == nil || !state.agentSession.Alive() {
			continue
		}
		lastEvent := state.lastEventTime
		if lastEvent.IsZero() {
			lastEvent = state.turnStartTime
		}
		if lastEvent.IsZero() {
			continue
		}
		if time.Since(lastEvent) > e.resetOnIdle {
			targets = append(targets, reapTarget{key: key, state: state})
		}
	}
	e.interactiveMu.Unlock()

	for _, t := range targets {
		slog.Info("reaping idle agent session",
			"session_key", t.key,
			"idle_for", time.Since(t.state.lastEventTime),
			"threshold", e.resetOnIdle)
		e.cleanupInteractiveState(t.key, t.state)
	}
}
