package core

// engine_turn.go — interactive turn processing (the core event loop)
//
// Contains the full turn processing subsystem:
//   - buildCardContent: assembles markdown for streaming card display
//   - stopUnsolicitedReader / startUnsolicitedReader / runUnsolicitedReader:
//     handle background "spontaneous" agent events between turns
//   - processInteractiveEvents: the main event loop that reads AgentSession.Events()
//     and routes each event to the appropriate platform sink
//   - mergeRichToolResult / notifyDroppedQueuedMessages / drainPendingMessages:
//     helpers called from the event loop
//
// All methods are func (e *Engine) receivers in the core package.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

const defaultEventIdleTimeout = 2 * time.Hour

// cardToolEntry stores a tool call record for card content rendering.
type cardToolEntry struct {
	Index int
	Name  string
	Input string
}

// buildCardContent constructs the full markdown for the streaming card.
func buildCardContent(thinking string, tools []cardToolEntry, answer string) string {
	var sb strings.Builder
	if thinking != "" {
		sb.WriteString("💭 **Thinking**\n\n")
		sb.WriteString(thinking)
		sb.WriteString("\n\n---\n\n")
	}
	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("🔧 **Tool #%d**: `%s`\n", t.Index, t.Name))
		if t.Input != "" {
			sb.WriteString(t.Input)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if answer != "" {
		if len(tools) > 0 || thinking != "" {
			sb.WriteString("---\n\n")
		}
		sb.WriteString(answer)
	}
	return sb.String()
}

// unsolicitedReaderStopTimeout bounds how long stopUnsolicitedReader waits
// for the reader goroutine to exit. The reader is structured so its iterations
// are short (blocking adapter calls like RespondPermission are offloaded), so
// this timeout should almost always be non-binding. If it does fire, callers
// force a resync of the Events channel to preserve single-reader correctness.
const unsolicitedReaderStopTimeout = 5 * time.Second

// stopUnsolicitedReader cancels any running unsolicited reader goroutine and
// waits (bounded) for it to exit. If the reader does not exit in time, the
// caller is responsible for draining/resyncing the Events channel before a
// new foreground turn reads from it — we set eventsNeedResync here so that
// any downstream consumer drains before resuming. We do NOT wait unbounded:
// some callers hold interactiveMu, and a reader stuck in a blocking adapter
// call would stall unrelated sessions.
func (e *Engine) stopUnsolicitedReader(state *interactiveState) {
	state.mu.Lock()
	cancel := state.unsolicitedCancel
	done := state.unsolicitedDone
	state.unsolicitedCancel = nil
	state.unsolicitedDone = nil
	state.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(unsolicitedReaderStopTimeout):
		slog.Warn("unsolicited reader stop timed out; forcing resync",
			"timeout", unsolicitedReaderStopTimeout)
		// Force the next foreground turn to drain Events() defensively.
		// The old reader may still be alive; its ctx-double-check will drop
		// any event read after cancellation, so concurrent consumers cannot
		// silently steal foreground events.
		state.mu.Lock()
		state.eventsNeedResync = true
		state.mu.Unlock()
	}
}

// startUnsolicitedReader launches a background goroutine that consumes agent
// events produced between user-initiated turns (e.g. background task
// completions in Claude Code). Events are relayed to the platform immediately.
// The goroutine exits when its context is cancelled (by a new foreground turn
// or session cleanup) or when the Events channel is closed.
func (e *Engine) startUnsolicitedReader(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, workspaceDir string) {
	// Ensure no previous reader is still running.
	e.stopUnsolicitedReader(state)

	// Capture the agent session under lock. cleanupInteractiveState may nil
	// state.agentSession concurrently, so reading it inside the goroutine
	// without synchronisation is a data race.
	state.mu.Lock()
	agentSession := state.agentSession
	state.mu.Unlock()
	if agentSession == nil {
		return
	}

	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})

	state.mu.Lock()
	state.unsolicitedCancel = cancel
	state.unsolicitedDone = done
	state.mu.Unlock()

	go e.runUnsolicitedReader(ctx, cancel, done, state, agentSession, session, sessions, sessionKey, workspaceDir)
}

func (e *Engine) hidesSubagentEvent(event Event, display DisplayCfg) bool {
	if !event.IsSubagent || display.ToolMessages {
		return false
	}
	switch event.Type {
	case EventText, EventThinking, EventToolUse, EventToolResult:
		return true
	default:
		return false
	}
}

// runUnsolicitedReader is the goroutine body for the unsolicited event reader.
// agentSession is captured by the caller so we don't race with
// cleanupInteractiveState nilling state.agentSession.
func (e *Engine) runUnsolicitedReader(ctx context.Context, cancel context.CancelFunc, done chan struct{}, state *interactiveState, agentSession AgentSession, session *Session, sessions *SessionManager, sessionKey string, workspaceDir string) {
	defer close(done)
	defer cancel()

	events := agentSession.Events()

	var turnActive bool // true after first event, cleared on EventResult
	defer func() {
		if turnActive {
			if workspaceDir != "" && e.workspacePool != nil {
				if ws := e.workspacePool.Get(workspaceDir); ws != nil {
					ws.EndTurn()
				}
			}
		}
	}()

	var textParts []string
	var toolsUsed []string

	for {
		select {
		case <-ctx.Done():
			// Context cancelled (new foreground turn or cleanup). Don't set
			// eventsNeedResync — the caller (stopUnsolicitedReader) knows the
			// channel state is clean because it just took ownership.
			return

		case event, ok := <-events:
			if !ok {
				// Channel closed — agent process exited. Log any buffered
				// tool/text context so it isn't lost silently.
				if len(toolsUsed) > 0 || len(textParts) > 0 {
					slog.Warn("unsolicited reader: agent channel closed mid-turn",
						"session", sessionKey,
						"tools_used", toolsUsed,
						"text_fragments", len(textParts))
				}
				state.mu.Lock()
				state.eventsNeedResync = true
				state.mu.Unlock()
				return
			}

			// Go's select is non-deterministic when multiple cases are
			// ready, so even after ctx is cancelled we may still read one
			// last event from the channel. If ownership has been handed
			// off, drop the event rather than processing it — otherwise we
			// could relay (or worse, respond to) an event that belongs to
			// the incoming foreground turn. The caller has already set
			// eventsNeedResync on timeout, so any buffered events will be
			// drained before the foreground turn reads them.
			select {
			case <-ctx.Done():
				slog.Warn("unsolicited reader: event received after cancellation, dropping",
					"session", sessionKey, "event_type", event.Type)
				state.mu.Lock()
				state.eventsNeedResync = true
				state.mu.Unlock()
				return
			default:
			}

			state.mu.Lock()
			p := state.platform
			replyCtx := state.replyCtx
			platformName := state.platformName
			state.mu.Unlock()
			display := e.resolveDisplayForPlatform(platformName)

			if e.hidesSubagentEvent(event, display) {
				continue
			}

			// Mark workspace active on first event.
			if !turnActive {
				turnActive = true
				if workspaceDir != "" && e.workspacePool != nil {
					if ws := e.workspacePool.Get(workspaceDir); ws != nil {
						ws.BeginTurn()
					}
				}
				slog.Info("unsolicited events detected, relaying to platform",
					"session", sessionKey)
			}

			switch event.Type {
			case EventText:
				if event.Content != "" {
					textParts = append(textParts, event.Content)
				}

			case EventToolUse:
				// Record tool name so we can log or surface context if the
				// channel closes before a clean EventResult. Output is
				// delivered via EventResult; we intentionally do not relay
				// per-tool progress here (no active user turn to observe it).
				if event.ToolName != "" {
					toolsUsed = append(toolsUsed, event.ToolName)
				}
				slog.Debug("unsolicited tool use",
					"session", sessionKey,
					"tool", event.ToolName)

			case EventToolResult:
				slog.Debug("unsolicited tool result",
					"session", sessionKey,
					"status", event.ToolStatus)

			case EventResult:
				fullResponse := event.Content
				if fullResponse == "" && len(textParts) > 0 {
					fullResponse = strings.Join(textParts, "")
				}

				if fullResponse != "" {
					if e.showContextIndicator && event.InputTokens >= 100 {
						fullResponse += contextIndicator(event.InputTokens)
					}
					for _, chunk := range splitMessage(fullResponse, maxPlatformMessageLen) {
						e.send(p, replyCtx, chunk)
					}
				}

				// Safety note: concurrent writes to session.History by the
				// unsolicited reader and a foreground turn cannot overlap.
				// Session.AddHistory takes session.mu internally, and
				// stopUnsolicitedReader (called before any foreground turn
				// takes event-channel ownership) blocks until this goroutine
				// exits — so a foreground AddHistory is always ordered after
				// any unsolicited AddHistory.
				session.AddHistory("assistant", fullResponse)
				sessions.Save()

				// Reset for potential subsequent unsolicited turn.
				textParts = nil
				toolsUsed = nil
				turnActive = false
				if workspaceDir != "" && e.workspacePool != nil {
					if ws := e.workspacePool.Get(workspaceDir); ws != nil {
						ws.EndTurn()
					}
				}

				// Mark clean exit so next foreground turn preserves events.
				state.mu.Lock()
				state.eventsNeedResync = false
				state.mu.Unlock()

				slog.Info("unsolicited turn complete",
					"session", sessionKey,
					"response_len", len(fullResponse))

			case EventPermissionRequest:
				// If approveAll (/yolo) is set, grant the request. Otherwise
				// deny — there is no active user turn to consult — and notify
				// the user on the platform so a silently blocked background
				// task is not invisible. RespondPermission may make a slow
				// adapter call, so we run it in a detached goroutine to keep
				// reader iterations fast (stopUnsolicitedReader relies on a
				// bounded wait for the reader to exit).
				state.mu.Lock()
				autoApprove := state.approveAll
				state.mu.Unlock()

				result := PermissionResult{Behavior: "deny", Message: "denied: no active user turn"}
				if autoApprove {
					result = PermissionResult{Behavior: "allow", UpdatedInput: event.ToolInputRaw}
				}
				reqID := event.RequestID
				respondCtx := ctx // capture current unsolicited reader context
				go func() {
					// Run in a goroutine to keep reader iterations fast, but honour
					// the reader's context so we don't call into a dead session after
					// stopUnsolicitedReader cancels the context.
					select {
					case <-respondCtx.Done():
						return
					default:
					}
					if err := agentSession.RespondPermission(reqID, result); err != nil {
						if respondCtx.Err() == nil {
							slog.Error("unsolicited: failed to respond permission", "error", err)
						}
					}
				}()
				if !autoApprove {
					toolName := event.ToolName
					if toolName == "" {
						toolName = "(unknown)"
					}
					e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgBackgroundAutoDenied), toolName))
				}

			case EventError:
				if event.Error != nil {
					slog.Error("unsolicited agent error", "error", event.Error, "session", sessionKey)
					e.send(p, replyCtx, e.sanitizeAgentError(event.Error.Error()))
				}
				state.mu.Lock()
				state.eventsNeedResync = true
				state.mu.Unlock()
				return
			}
		}
	}
}

type agentErrorHandler struct {
	contains string
	msgKey   MsgKey
}

// contains values are lowercase because matching is case-insensitive.
var agentErrorHandlers = []agentErrorHandler{
	{"session not found", MsgSessionNotFound},
	{"unknown session", MsgSessionNotFound},
	{"invalid session", MsgSessionNotFound},
	{"quota exceeded", MsgModelQuotaExceeded},
	{"usage quota", MsgModelQuotaExceeded},
	{"rate limit", MsgModelQuotaExceeded},
	{"too many requests", MsgModelQuotaExceeded},
	{"method not found", MsgAgentUnsupportedMethod},
	{"not implemented", MsgAgentUnsupportedMethod},
	{"unsupported method", MsgAgentUnsupportedMethod},
	{"agent process exited", MsgAgentProcessExited},
	{"process exited", MsgAgentProcessExited},
	{"broken pipe", MsgAgentProcessExited},
	{"connection reset", MsgAgentProcessExited},
	{"signal: killed", MsgAgentProcessExited},
}

// sanitizeAgentError returns a localized, user-facing error without exposing
// raw stderr, stack traces, RPC payloads, filesystem paths, or unknown
// provider data to IM users. Raw details remain available in logs and hooks.
func (e *Engine) sanitizeAgentError(errMsg string) string {
	return sanitizeAgentErrorMessage(errMsg, e.i18n)
}

// sanitizeAgentErrorMessage is the stateless core used by tests.
func sanitizeAgentErrorMessage(errMsg string, lang *I18n) string {
	t := func(k MsgKey) string { return lang.T(k) }
	normalized := strings.ToLower(strings.TrimSpace(errMsg))

	for _, h := range agentErrorHandlers {
		if strings.Contains(normalized, h.contains) {
			return t(h.msgKey)
		}
	}

	if strings.Contains(normalized, "-32601") || strings.Contains(normalized, "-32600") {
		return t(MsgAgentUnsupportedMethod)
	}

	if strings.Count(errMsg, "\n") >= 2 || strings.Contains(normalized, "exit status") ||
		strings.Contains(normalized, "unexpected eof") {
		return t(MsgAgentProcessExited)
	}

	// Default-deny: unknown agent errors may contain tokens, internal paths,
	// JSON-RPC payloads, or provider diagnostics. Never relay them verbatim.
	return t(MsgAgentInternalError)
}

func (e *Engine) processInteractiveEvents(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, msgID string, turnStart time.Time, stopTypingFn func(), sendDone <-chan error, replyCtx any) {
	if msgID != "" {
		state.mu.Lock()
		state.currentMessageID = msgID
		state.lastTurnMessageID = msgID
		state.mu.Unlock()
	}

	var textParts []string
	var segmentStart int // index into textParts: text before this has been sent/displayed
	silentHold := false  // true while accumulated segment text could still resolve to a bare NO_REPLY marker
	toolCount := 0
	toolCounts := make(map[string]int) // per-tool call counts for dashboard metrics
	statsErr := ""                     // terminal error message, recorded at turn end
	waitStart := time.Now()
	firstEventLogged := false
	var toolSteps []ToolStep
	var lastRichCardUpdate time.Time
	var lastRichCardLen int
	var cardMessageID any
	var partialText string
	triggerAutoCompress := false
	pendingSend := sendDone

	// stopTyping tracks the current turn's typing indicator so it can be
	// stopped when a queued message starts a new turn.
	stopTyping := stopTypingFn
	// doneReaction stores a function to add a "done" emoji after stopTyping.
	// Set during EventResult handling for multi-round quiet turns.
	var doneReaction func()
	defer func() {
		if stopTyping != nil {
			stopTyping()
		}
		if doneReaction != nil {
			doneReaction()
		}
	}()

	state.mu.Lock()
	workspaceDir := state.workspaceDir
	replyAgent := state.agent
	platformName := state.platformName
	if replyAgent == nil {
		replyAgent = e.agent
	}
	workspaceRenderer := func(content string) string {
		return e.renderOutgoingContentForWorkspace(state.platform, content, workspaceDir)
	}
	sendWorkspace := func(p Platform, replyCtx any, content string) {
		e.sendForWorkspace(p, replyCtx, content, workspaceDir)
	}
	sendWorkspaceWithError := func(p Platform, replyCtx any, content string) error {
		return e.sendWithErrorForWorkspace(p, replyCtx, content, workspaceDir)
	}
	// Streaming card: aggregate entire turn into a single updatable card.
	var streamCard StreamingCard
	var cardToolCalls []cardToolEntry  // track tool calls for card content
	var cardThinkingText string        // latest thinking text
	var cardAnswerText strings.Builder // accumulated answer text

	if scp, ok := state.platform.(StreamingCardPlatform); ok {
		if sc, err := scp.CreateStreamingCard(e.ctx, state.replyCtx); err != nil {
			slog.Warn("streaming card creation failed, falling back to normal messages", "error", err)
		} else {
			streamCard = sc
			slog.Info("streaming card created for turn", "session", sessionKey)
		}
	}
	sp := newStreamPreview(e.streamPreview, state.platform, state.replyCtx, e.ctx, workspaceRenderer)
	cp := newCompactProgressWriter(e.ctx, state.platform, state.replyCtx, e.agent.Name(), e.i18n.CurrentLang(), workspaceRenderer)
	state.mu.Unlock()
	display := e.resolveDisplayForPlatform(platformName)
	streamPreviewToolHold := sp.previewMode() == "tool_hold" && display.Mode == displayModeStream

	// Send instant confirmation reply if enabled and no streaming card is active.
	// Streaming cards provide their own "processing" indicator, so instant reply
	// is only needed when the platform doesn't support cards or card creation failed.
	if e.instantReply.Enabled && streamCard == nil {
		replyContent := e.instantReply.Content
		if replyContent == "" {
			replyContent = e.i18n.T(MsgStarting)
		}
		e.send(state.platform, state.replyCtx, replyContent)
	}

	// Idle timeout: 0 = disabled
	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if e.eventIdleTimeout > 0 {
		idleTimer = time.NewTimer(e.eventIdleTimeout)
		defer idleTimer.Stop()
		idleCh = idleTimer.C
	}

	// Reassurance timer: sends a "still working" message via stream preview
	// when no output has been received for too long (default 1 minute).
	// Since WeCom uses full-replacement stream updates, the reassurance text
	// is automatically replaced when real agent output arrives.
	const defaultReassureInterval = 1 * time.Minute
	reassureTimer := time.NewTimer(defaultReassureInterval)
	defer reassureTimer.Stop()
	var reassureSent bool

	events := state.agentSession.Events()
	stopCh := state.stopSignal()

	// cancelCh is a per-turn signal that /cancel closes to make this loop stop
	// relaying further events. It is created fresh each turn and cleared on
	// exit, so its scope is exactly "the currently running turn" — the session
	// itself stays alive for the next message.
	cancelCh := make(chan struct{})
	state.mu.Lock()
	state.cancelCh = cancelCh
	state.lastEventTime = time.Time{} // reset for new turn
	state.turnStartTime = time.Now()
	state.mu.Unlock()
	defer func() {
		state.mu.Lock()
		if state.cancelCh == cancelCh {
			state.cancelCh = nil
		}
		state.mu.Unlock()
	}()

	// handleCancel performs the same terminal cleanup as the idle-timeout
	// branch: finalize the progress card, discard the streaming preview, and
	// mark eventsNeedResync so any events still buffered in the channel are
	// drained before the next turn AND are not picked up by the unsolicited
	// reader (which would otherwise relay them to the user post-cancel).
	handleCancel := func() {
		cp.Finalize(ProgressCardStateFailed)
		sp.discard()
		state.mu.Lock()
		state.eventsNeedResync = true
		state.mu.Unlock()
	}

	for {
		var event Event
		var ok bool

		// Prioritize cancel: Go's select is random when multiple cases are
		// ready, so without this a /cancel could still lose the race to a
		// buffered chunk and leak one more message before exiting. Checking
		// cancelCh first makes the stop deterministic.
		select {
		case <-cancelCh:
			handleCancel()
			return
		default:
		}

		select {
		case <-stopCh:
			sp.discard()
			return
		case <-cancelCh:
			handleCancel()
			return
		case event, ok = <-events:
			if !ok {
				goto channelClosed
			}
		case err := <-pendingSend:
			pendingSend = nil
			if err != nil {
				state.mu.Lock()
				pName := state.platform.Name()
				state.mu.Unlock()
				slog.Error("failed to send prompt",
					"error", err,
					"session_key", sessionKey,
					"platform", pName,
					"content_len", len(textParts))
				sp.discard()
				if stopTyping != nil {
					stopTyping()
					stopTyping = nil
				}
				e.notifyDroppedQueuedMessages(state, err)
				if state.agentSession == nil || !state.agentSession.Alive() {
					e.cleanupInteractiveState(sessionKey, state)
				}
				state.mu.Lock()
				p := state.platform
				state.mu.Unlock()
				e.send(p, replyCtx, e.sanitizeAgentError(err.Error()))
				return
			}
			continue
		case <-reassureTimer.C:
			msg := "⏳ 仍在处理中，请耐心等待..."
			if !reassureSent {
				msg = "⏳ 正在处理您的请求..."
				reassureSent = true
			}
			if sp.hasPreview() {
				sp.reassure(msg)
			} else {
				sp.ensurePreview(msg)
			}
			reassureTimer.Reset(defaultReassureInterval)
		case <-idleCh:
			slog.Error("agent session idle timeout: no events for too long, killing session",
				"session_key", sessionKey, "timeout", e.eventIdleTimeout, "elapsed", time.Since(turnStart))
			cp.Finalize(ProgressCardStateFailed)
			sp.discard()
			state.mu.Lock()
			state.eventsNeedResync = true
			p := state.platform
			state.mu.Unlock()
			e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), "agent session timed out (no response)"))
			e.cleanupInteractiveState(sessionKey, state)
			return
		case <-e.ctx.Done():
			state.mu.Lock()
			state.eventsNeedResync = true
			state.mu.Unlock()
			return
		}

		if state.isStopped() {
			sp.discard()
			state.mu.Lock()
			state.eventsNeedResync = true
			state.mu.Unlock()
			return
		}

		// Reset idle timer after receiving an event
		if idleTimer != nil {
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(e.eventIdleTimeout)
		}

		// Update lastEventTime for the session reaper
		state.mu.Lock()
		state.lastEventTime = time.Now()
		state.mu.Unlock()

		// Reset reassurance timer on every agent event
		if !reassureTimer.Stop() {
			select {
			case <-reassureTimer.C:
			default:
			}
		}
		reassureTimer.Reset(defaultReassureInterval)

		if !firstEventLogged {
			firstEventLogged = true
			if elapsed := time.Since(waitStart); elapsed >= slowAgentFirstEvent {
				slog.Warn("slow agent first event", "elapsed", elapsed, "session", sessionKey, "event_type", event.Type)
			}
		}

		if e.hidesSubagentEvent(event, display) {
			continue
		}

		state.mu.Lock()
		p := state.platform
		state.mu.Unlock()

		// main codebase has no per-session quiet flag; pr309 referenced
		// sessionQuiet which we drop. display.ThinkingMessages /
		// ToolMessages handle user-level quiet in the fallback branches.
		richCardSupporter, hasRichCard := p.(RichCardSupporter)
		// Card 2.0 rich-card path is opt-in via [display] mode = "rich".
		// Default "legacy" keeps upstream behavior for all platforms.
		if display.CardMode != "rich" {
			hasRichCard = false
		}

		switch event.Type {
		case EventThinking:
			if isEllipsisOnly(event.Content) {
				break
			}
			if hasRichCard {
				// When thinking messages are suppressed, skip card creation.
				if !display.ThinkingMessages {
					break
				}
				if thinking := strings.TrimSpace(truncateIf(event.Content, display.ThinkingMaxLen)); thinking != "" {
					toolSteps = append(toolSteps, ToolStep{
						Kind:    ToolStepKindThinking,
						Name:    "Thinking",
						Summary: thinking,
						Done:    true,
					})
				}
				if cardMessageID == nil {
					card := richCardSupporter.BuildRichCard(CardStatusThinking, "", toolSteps, partialText, true, time.Since(turnStart))
					if starter, ok := p.(PreviewStarter); ok {
						handle, err := starter.SendPreviewStart(e.ctx, replyCtx, card)
						if err != nil {
							slog.Debug("rich card: failed to create initial thinking card", "platform", p.Name(), "error", err)
						} else {
							cardMessageID = handle
						}
					}
				} else if updater, ok := p.(MessageUpdater); ok {
					card := richCardSupporter.BuildRichCard(CardStatusThinking, "", toolSteps, partialText, true, time.Since(turnStart))
					if err := updater.UpdateMessage(e.ctx, cardMessageID, card); err != nil {
						slog.Debug("rich card: failed to update thinking card", "platform", p.Name(), "error", err)
					}
				}
				break
			}
			// When thinking messages are hidden, behavior depends on display mode:
			//   quiet/stream: append separator to keep all text in one preview
			//   compact:      freeze+detach to split text into separate cards
			if !display.ThinkingMessages && len(textParts) > segmentStart {
				if display.Mode == displayModeQuiet || display.Mode == displayModeStream {
					if sp.canPreview() && sp.appendSeparator("\n\n") {
						textParts = append(textParts, "\n\n")
					}
				} else {
					if sp.canPreview() {
						sp.freeze()
						sp.detachPreview()
					} else {
						segment := strings.Join(textParts[segmentStart:], "")
						if segment != "" {
							for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
								sendWorkspace(p, replyCtx, chunk)
							}
						}
					}
					segmentStart = len(textParts)
				}
				silentHold = false
			}
			if display.ThinkingMessages && event.Content != "" {
				// --- StreamingCard path ---
				if streamCard != nil && !streamCard.Failed() {
					cardThinkingText = truncateIf(event.Content, display.ThinkingMaxLen)
					_ = streamCard.Update(e.ctx, buildCardContent(cardThinkingText, cardToolCalls, cardAnswerText.String()))
					continue // skip original independent message sending
				}
				// --- Original path (fallback) ---
				// Flush accumulated text segment before thinking display
				previewActive := sp.canPreview()
				if len(textParts) > segmentStart {
					if !previewActive {
						segment := strings.Join(textParts[segmentStart:], "")
						if segment != "" {
							for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
								sendWorkspace(p, replyCtx, chunk)
							}
						}
					}
					segmentStart = len(textParts)
					silentHold = false
				}
				sp.freeze()
				if previewActive {
					sp.detachPreview() // keep frozen preview visible as permanent message
				}
				preview := truncateIf(event.Content, display.ThinkingMaxLen)
				thinkingMsg := fmt.Sprintf(e.i18n.T(MsgThinking), preview)
				if !cp.AppendEvent(ProgressEntryThinking, preview, "", thinkingMsg) {
					sendWorkspace(p, replyCtx, thinkingMsg)
				}
			}

		case EventToolUse:
			toolCount++
			toolCounts[event.ToolName]++
			toolInput := event.ToolInput
			// Sub-agent tool calls get a "↳ " prefix on the display name so
			// users can tell child-agent activity from the main agent's. The
			// marker goes on the name inside the message template (after the
			// "🔧 **" prefix) so platform parsers that match on that prefix
			// (WeCom stream assembler, Feishu) keep working.
			displayToolName := event.ToolName
			if event.IsSubagent {
				displayToolName = "↳ " + event.ToolName
			}
			var formattedInput string
			if toolInput == "" {
				formattedInput = ""
			} else if strings.Contains(toolInput, "```") {
				formattedInput = toolInput
			} else if strings.Contains(toolInput, "\n") || utf8.RuneCountInString(toolInput) > 200 {
				lang := toolCodeLang(event.ToolName, toolInput)
				formattedInput = fmt.Sprintf("```%s\n%s\n```", lang, toolInput)
			} else {
				switch event.ToolName {
				case "shell", "run_shell_command", "Bash":
					formattedInput = fmt.Sprintf("```bash\n%s\n```", toolInput)
				default:
					formattedInput = fmt.Sprintf("`%s`", toolInput)
				}
			}
			if hasRichCard {
				// When tool messages are suppressed, skip card updates on tool events.
				if !display.ToolMessages {
					break
				}
				toolSteps = append(toolSteps, ToolStep{
					Kind:    ToolStepKindTool,
					Name:    event.ToolName,
					Summary: truncateIf(event.ToolInput, display.ToolMaxLen),
				})
				if cardMessageID == nil {
					card := richCardSupporter.BuildRichCard(CardStatusWorking, "", toolSteps, partialText, true, time.Since(turnStart))
					if starter, ok := p.(PreviewStarter); ok {
						handle, err := starter.SendPreviewStart(e.ctx, replyCtx, card)
						if err != nil {
							slog.Debug("rich card: failed to create initial tool card", "platform", p.Name(), "error", err)
						} else {
							cardMessageID = handle
						}
					}
				} else if updater, ok := p.(MessageUpdater); ok {
					card := richCardSupporter.BuildRichCard(CardStatusWorking, "", toolSteps, partialText, true, time.Since(turnStart))
					if err := updater.UpdateMessage(e.ctx, cardMessageID, card); err != nil {
						slog.Debug("rich card: failed to update tool card", "platform", p.Name(), "error", err)
					}
				}
				break
			}
			if streamPreviewToolHold {
				if display.ToolMessages {
					// Route tool progress to ProgressAssembler side-channel instead of
					// polluting visibleText. Tool progress is a UI side-channel, never
					// enters the model-produced answer text.
					if assembler, ok := p.(ProgressAssembler); ok {
						if handle := sp.progressHandle(e.i18n.T(MsgStarting)); handle != nil {
							_ = assembler.OnToolStart(sp.ctx, handle, event.ToolName, formattedInput, event.ToolInput)
						}
					}
				}
				continue
			}
		if display.Mode == displayModeStream && display.ToolMessages {
			toolMsg := fmt.Sprintf(e.i18n.T(MsgTool), toolCount, displayToolName, formattedInput)
				prefix := ""
				if len(textParts) > 0 {
					textParts = append(textParts, "\n\n")
					prefix = "\n\n"
				}
				textParts = append(textParts, toolMsg)
				if sp.canPreview() {
					sp.appendTextNow(prefix + toolMsg)
				}
				continue
			}
			// When tool messages are hidden, behavior depends on display mode:
			//   quiet/stream: append separator to keep all text in one preview
			//   compact:      freeze+detach to split text into separate cards
			if !display.ToolMessages && len(textParts) > segmentStart {
				if display.Mode == displayModeQuiet || display.Mode == displayModeStream {
					if sp.canPreview() && sp.appendSeparator("\n\n") {
						textParts = append(textParts, "\n\n")
					}
				} else {
					if sp.canPreview() {
						sp.freeze()
						sp.detachPreview()
					} else {
						segment := strings.Join(textParts[segmentStart:], "")
						if segment != "" {
							for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
								sendWorkspace(p, replyCtx, chunk)
							}
						}
					}
					segmentStart = len(textParts)
				}
				silentHold = false
			}
			if display.ToolMessages {
				// --- StreamingCard path ---
				if streamCard != nil && !streamCard.Failed() {
					cardToolCalls = append(cardToolCalls, cardToolEntry{
						Index: toolCount,
						Name:  event.ToolName,
						Input: formattedInput,
					})
					_ = streamCard.Update(e.ctx, buildCardContent(cardThinkingText, cardToolCalls, cardAnswerText.String()))
					continue // skip original independent message sending
				}
				// --- Original path (fallback) ---
				// Flush accumulated text segment before tool display
				previewActive := sp.canPreview()
				if len(textParts) > segmentStart {
					if !previewActive {
						segment := strings.Join(textParts[segmentStart:], "")
						if segment != "" {
							for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
								sendWorkspace(p, replyCtx, chunk)
							}
						}
					}
					segmentStart = len(textParts)
					silentHold = false
				}
				sp.freeze()
				if previewActive {
					sp.detachPreview() // keep frozen preview visible as permanent message
				}
			toolMsg := fmt.Sprintf(e.i18n.T(MsgTool), toolCount, displayToolName, formattedInput)
			if !cp.AppendStructured(ProgressCardEntry{
				Kind:           ProgressEntryToolUse,
				Text:           toolInput,
				Tool:           event.ToolName,
				ID:             event.ToolID,
				CorrelationKey: event.ToolID,
			}, toolMsg) {
					for _, chunk := range SplitMessageCodeFenceAware(toolMsg, maxPlatformMessageLen) {
						sendWorkspace(p, replyCtx, chunk)
					}
				}
			}

		case EventToolResult:
			if display.ToolMessages {
				result := strings.TrimSpace(event.ToolResult)
				if result == "" {
					result = strings.TrimSpace(event.Content)
				}
				if result != "" {
					result = truncateIf(result, display.ToolMaxLen)
				}
				if result != "" || event.ToolStatus != "" || event.ToolExitCode != nil || event.ToolSuccess != nil {
					if hasRichCard {
						toolSteps = mergeRichToolResult(toolSteps, event, result, display.ToolMaxLen)
						if cardMessageID == nil {
							card := richCardSupporter.BuildRichCard(CardStatusWorking, "", toolSteps, partialText, true, time.Since(turnStart))
							if starter, ok := p.(PreviewStarter); ok {
								handle, err := starter.SendPreviewStart(e.ctx, replyCtx, card)
								if err != nil {
									slog.Debug("rich card: failed to create tool-result card", "platform", p.Name(), "error", err)
								} else {
									cardMessageID = handle
								}
							}
						} else if updater, ok := p.(MessageUpdater); ok {
							card := richCardSupporter.BuildRichCard(CardStatusWorking, "", toolSteps, partialText, true, time.Since(turnStart))
							if err := updater.UpdateMessage(e.ctx, cardMessageID, card); err != nil {
								slog.Debug("rich card: failed to update tool-result card", "platform", p.Name(), "error", err)
							}
						}
						break
					}
					resultToolName := event.ToolName
				if event.IsSubagent {
					resultToolName = "↳ " + event.ToolName
				}
				resultMsg := e.formatToolResultEventFallback(resultToolName, result, event.ToolStatus, event.ToolExitCode, event.ToolSuccess)
					if streamPreviewToolHold {
						if display.ToolMessages {
							// Route tool result to ProgressAssembler side-channel.
							if assembler, ok := p.(ProgressAssembler); ok {
								if handle := sp.progressHandle(e.i18n.T(MsgStarting)); handle != nil {
									_ = assembler.OnToolComplete(sp.ctx, handle, event.ToolName, result)
								}
							}
						}
						continue
					}
					if display.Mode == displayModeStream {
						prefix := ""
						if len(textParts) > 0 {
							textParts = append(textParts, "\n\n")
							prefix = "\n\n"
						}
						textParts = append(textParts, resultMsg)
						if sp.canPreview() {
							sp.appendTextNow(prefix + resultMsg)
						}
						break
					}
				entry := ProgressCardEntry{
					Kind:           ProgressEntryToolResult,
					Tool:           event.ToolName,
					ID:             event.ToolID,
					CorrelationKey: event.ToolID,
					Text:           result,
					Status:         event.ToolStatus,
					ExitCode:       event.ToolExitCode,
					Success:        event.ToolSuccess,
				}
					if !cp.AppendStructured(entry, resultMsg) {
						if !SuppressStandaloneToolResultEvent(p) {
							e.sendRaw(p, replyCtx, resultMsg)
						}
					}
				}
			}

		case EventText:
			if event.Content != "" && !isEllipsisOnly(event.Content) {
				if streamCard != nil && !streamCard.Failed() {
					// Streaming card path (e.g. DingTalk AI Card): aggregate
					// answer text into a single updatable card message.
					textParts = append(textParts, event.Content) // always accumulate for history
					cardAnswerText.WriteString(event.Content)
					_ = streamCard.Update(e.ctx, buildCardContent(cardThinkingText, cardToolCalls, cardAnswerText.String()))
				} else {
					if len(textParts) == 0 {
						if hasRichCard {
							if cardMessageID == nil {
								card := richCardSupporter.BuildRichCard(CardStatusWorking, "", toolSteps, partialText, true, time.Since(turnStart))
								if starter, ok := p.(PreviewStarter); ok {
									handle, err := starter.SendPreviewStart(e.ctx, replyCtx, card)
									if err != nil {
										slog.Debug("rich card: failed to create initial text card", "platform", p.Name(), "error", err)
									} else {
										cardMessageID = handle
									}
								}
							}
						} else {
							sp.setStatus(CardStatusWorking)
						}
					}
					textParts = append(textParts, event.Content)
					partialText += event.Content
					if hasRichCard {
						if cardMessageID != nil && (time.Since(lastRichCardUpdate) > 1500*time.Millisecond || len(partialText)-lastRichCardLen > 30) {
							card := richCardSupporter.BuildRichCard(CardStatusWorking, "", toolSteps, partialText, true, time.Since(turnStart))
							if updater, ok := p.(MessageUpdater); ok {
								if err := updater.UpdateMessage(e.ctx, cardMessageID, card); err == nil {
									lastRichCardUpdate = time.Now()
									lastRichCardLen = len(partialText)
								} else {
									slog.Debug("rich card: failed to update text card", "platform", p.Name(), "error", err)
								}
							}
						}
					} else {
						segmentText := strings.Join(textParts[segmentStart:], "")
						if silentHold {
							if !couldBeSilentPrefix(segmentText) {
								silentHold = false
								if sp.canPreview() {
									sp.appendText(segmentText) // flush all held chunks at once
								}
							}
						} else if couldBeSilentPrefix(segmentText) {
							// Hold streaming until we know whether this segment is NO_REPLY.
							// Safe because once segmentText is no longer a prefix of "NO_REPLY",
							// it can never become one again — we only ever transition held→released once.
							silentHold = true
						} else if sp.canPreview() {
							sp.appendText(event.Content)
						}
					}
				}
			}
			if event.SessionID != "" {
				if session.CompareAndSetAgentSessionID(event.SessionID, e.agent.Name()) {
					pendingName := session.GetName()
					if pendingName != "" && pendingName != "session" && pendingName != "default" {
						sessions.SetSessionName(event.SessionID, pendingName)
					}
					sessions.Save()
				}
			}

		case EventPermissionRequest:
			isAskQuestion := event.ToolName == "AskUserQuestion" && len(event.Questions) > 0

			state.mu.Lock()
			autoApprove := state.approveAll
			state.mu.Unlock()

			if autoApprove && !isAskQuestion {
				slog.Debug("auto-approving (approve-all)", "request_id", event.RequestID, "tool", event.ToolName)
				_ = state.agentSession.RespondPermission(event.RequestID, PermissionResult{
					Behavior:     "allow",
					UpdatedInput: event.ToolInputRaw,
				})
				continue
			}

			// Flush accumulated text segment before permission prompt
			previewActive := sp.canPreview()
			if len(textParts) > segmentStart {
				if !previewActive {
					segment := strings.Join(textParts[segmentStart:], "")
					if segment != "" {
						for _, chunk := range splitMessage(segment, maxPlatformMessageLen) {
							sendWorkspace(p, replyCtx, chunk)
						}
					}
				}
				segmentStart = len(textParts)
				silentHold = false
			}
			sp.freeze()
			if previewActive {
				sp.detachPreview() // keep frozen preview visible as permanent message
			}

			slog.Info("permission request",
				"request_id", event.RequestID,
				"tool", event.ToolName,
			)

			pending := &pendingPermission{
				RequestID:    event.RequestID,
				ToolName:     event.ToolName,
				ToolInput:    event.ToolInputRaw,
				InputPreview: event.ToolInput,
				Questions:    event.Questions,
				Resolved:     make(chan struct{}),
			}
			state.mu.Lock()
			state.pending = pending
			state.mu.Unlock()

			if isAskQuestion {
				e.sendAskQuestionPrompt(p, replyCtx, event.Questions, 0)
			} else {
				permLimit := display.ToolMaxLen
				if permLimit > 0 {
					permLimit = permLimit * 8 / 5
				}
				toolInput := truncateIf(event.ToolInput, permLimit)
				prompt := fmt.Sprintf(e.i18n.T(MsgPermissionPrompt), event.ToolName, toolInput)
				e.sendPermissionPrompt(p, replyCtx, prompt, event.ToolName, toolInput)
			}

			// Stop idle timer while waiting for user permission response;
			// the user may take a long time to decide, and we don't want
			// the idle timeout to kill the session during that wait.
			if idleTimer != nil {
				idleTimer.Stop()
			}

			<-pending.Resolved
			slog.Info("permission resolved", "request_id", event.RequestID)

			// Restart idle timer after permission is resolved
			if idleTimer != nil {
				idleTimer.Reset(e.eventIdleTimeout)
			}

		case EventResult:
			cp.Finalize(ProgressCardStateCompleted)
			// Use state.agentSession.CurrentSessionID() instead of event.SessionID.
			// event.SessionID may be empty in some cases, causing the agent_session_id
			// to not be persisted to disk, breaking session resume on next startup.
		if state != nil && state.agentSession != nil {
			if currentID := state.agentSession.CurrentSessionID(); currentID != "" {
				if session.CompareAndSetAgentSessionID(currentID, e.agent.Name()) {
					pendingName := session.GetName()
					if !isPlaceholderSessionName(pendingName) && !session.GetNameAuto() {
						sessions.SetSessionName(currentID, pendingName)
					}
				}
				sessions.Save()
			}
		}

		// Auto-title the session after its first completed turn: prefer the
		// agent's own session summary, fall back to the first user message.
		// Async because ListSessions may do real I/O (e.g. claudecode scans
		// its projects dir) and must not delay reply delivery below.
		go e.maybeAutoTitleSession(e.agent, sessions, session)

			// Mark clean exit so unsolicited reader preserves buffered events.
			state.mu.Lock()
			state.eventsNeedResync = false
			state.mu.Unlock()

			fullResponse := event.Content
			if fullResponse == "" && len(textParts) > 0 {
				fullResponse = strings.Join(textParts, "")
			}
			if fullResponse == "" {
				fullResponse = e.i18n.T(MsgEmptyResponse)
			}

			// Context usage indicator: prefer SDK tokens, fall back to self-reported.
			sdkPlausible := event.InputTokens >= 100
			selfPct := parseSelfReportedCtx(fullResponse)
			cleanResponse := ctxSelfReportRe.ReplaceAllString(fullResponse, "")
			cleanResponse = strings.TrimRight(cleanResponse, "\n ")
			baseResponse := cleanResponse

			contextEstimate := estimateTokensWithPendingAssistant(session.GetHistory(0), baseResponse)

			// Evaluate auto-compress trigger (token estimate on user+assistant text,
			// including this turn's assistant reply before it is appended to history).
			if e.autoCompressEnabled && e.autoCompressMaxTokens > 0 {
				estimate := contextEstimate
				now := time.Now()
				state.mu.Lock()
				last := state.lastAutoCompressAt
				state.mu.Unlock()
				if estimate >= e.autoCompressMaxTokens && (last.IsZero() || now.Sub(last) >= e.autoCompressMinGap) {
					triggerAutoCompress = true
					state.mu.Lock()
					state.lastAutoCompressTokens = estimate
					state.mu.Unlock()
				}
			}

			// Detect NO_REPLY marker on the base response (before indicators/footer are appended).
			// Three cases:
			//   1. bare marker (isSilentReply)               → fully silent
			//   2. trailing marker with non-empty reasoning  → strip marker, deliver reasoning
			//   3. trailing marker with empty strip result   → fully silent
			// History records the ORIGINAL baseResponse so the agent retains context of its own
			// decision; only the outbound platform text gets rewritten/suppressed.
			session.AddHistory("assistant", baseResponse)
			sessions.Save()

			isSilent := isSilentReply(baseResponse)
			if !isSilent {
				if stripped, ok := stripTrailingSilent(baseResponse); ok {
					if strings.TrimSpace(stripped) == "" {
						isSilent = true
					} else {
						baseResponse = stripped
						cleanResponse = stripped
					}
				}
			}

			if !isSilent {
				e.hooks.Emit(HookEvent{
					Event:      HookEventMessageSent,
					SessionKey: sessionKey,
					Platform:   p.Name(),
					Content:    baseResponse,
				})
			}

			contextText := ""
			if e.showContextIndicator && !isSilent {
				if sdkPlausible {
					contextText = contextIndicatorText(event.InputTokens)
				} else if selfPct > 0 {
					contextText = fmt.Sprintf("[ctx: ~%d%%]", selfPct)
				}
			}
			if !isSilent {
				footerContext := replyFooterContextText(replyFooterSessionContextUsage(state.agentSession), e.i18n)
				if contextText != "" && e.replyFooterEnabled {
					footerContext = contextText
				}
				if footer := e.buildReplyFooter(replyAgent, state.agentSession, workspaceDir, footerContext); footer != "" {
					cleanResponse = appendReplyFooter(cleanResponse, footer)
				} else if contextText != "" {
					cleanResponse += "\n" + contextText
				}
			}
			fullResponse = cleanResponse
			deliverResponse := fullResponse
			if display.Mode == displayModeStream && !isSilent {
				deliverResponse = mergeStreamDisplayContent(strings.Join(textParts, ""), event.Content, fullResponse)
			}

			turnDuration := time.Since(turnStart)
			slog.Info("turn complete",
				"session", session.ID,
				"agent_session", session.GetAgentSessionID(),
				"msg_id", msgID,
				"tools", toolCount,
				"response_len", len(fullResponse),
				"turn_duration", turnDuration,
				"input_tokens", event.InputTokens,
				"output_tokens", event.OutputTokens,
				"silent", isSilent,
			)

			// Dashboard metrics: one record per completed turn.
			if e.statsRecorder != nil {
				userID, userName := e.turnStatsFromState(state, sessions, sessionKey)
				platformName := ""
				state.mu.Lock()
				platformName = state.platformName
				state.mu.Unlock()
				e.recordTurnStats(statsTurnInput{
					session:         session,
					sessionKey:      sessionKey,
					platformName:    platformName,
					agentName:       replyAgent.Name(),
					userID:          userID,
					userName:        userName,
					msgID:           msgID,
					turnStart:       turnStart,
					duration:        turnDuration,
					inputTokens:     event.InputTokens,
					outputTokens:    event.OutputTokens,
					tokensPlausible: sdkPlausible,
					contextEstimate: contextEstimate,
					toolCalls:       toolCount,
					tools:           toolCounts,
					responseChars:   len(fullResponse),
					silent:          isSilent,
					err:             statsErr,
				})
			}

			normalizedBaseResponse := strings.TrimSpace(baseResponse)
			state.mu.Lock()
			suppressDuplicate := normalizedBaseResponse != "" && normalizedBaseResponse == state.sideText
			state.sideText = ""
			state.mu.Unlock()

			replyStart := time.Now()

			// --- StreamingCard path ---
			if streamCard != nil && !streamCard.Failed() {
				sp.finish("") // cleanup preview (should be no-op if card was active)
				// Build final card content with full response
				finalContent := buildCardContent(cardThinkingText, cardToolCalls, fullResponse)
				if err := streamCard.Finalize(e.ctx, finalContent); err != nil {
					slog.Error("streaming card finalize failed, sending fallback",
						"error", err,
						"session_key", sessionKey,
						"platform", p.Name(),
						"msg_id", msgID)
					// Fallback: send the response as a normal message
					for _, chunk := range splitMessage(deliverResponse, maxPlatformMessageLen) {
						if err := sendWorkspaceWithError(p, replyCtx, chunk); err != nil {
							return
						}
					}
				}
			} else if isSilent {
				// Silent reply: drop any in-flight preview and skip all send paths.
				// sp.discard() clears previewMsgID so sp.needsDoneReaction() also returns false,
				// preventing a stray done_emoji push.
				sp.discard()
				// Rich mode: cardMessageID is tracked independently of sp.previewMsgID,
				// so sp.discard() doesn't reach it. Without this cleanup the rich card
				// would stay frozen in "Working" / "Thinking" header state forever
				// (no Done flip, no Patch). Delete the message so NO_REPLY truly leaves
				// no trace.
				if hasRichCard && cardMessageID != nil {
					if cleaner, ok := p.(PreviewCleaner); ok {
						if err := cleaner.DeletePreviewMessage(e.ctx, cardMessageID); err != nil {
							slog.Debug("rich card: failed to delete card on silent reply", "platform", p.Name(), "error", err)
						}
					}
					cardMessageID = nil
				}
				slog.Info("silent reply suppressed", "session", session.ID)
			} else if hasRichCard {
				parts := []string{fullResponse}
				if splitter, ok := p.(MarkdownTableSplitter); ok {
					parts = splitter.SplitMarkdownByTables(fullResponse, 5)
				}
				finalCard := richCardSupporter.BuildRichCard(CardStatusDone, "", toolSteps, parts[0], false, time.Since(turnStart))
				if cardMessageID != nil {
					if updater, ok := p.(MessageUpdater); ok {
						if err := updater.UpdateMessage(e.ctx, cardMessageID, finalCard); err != nil {
							slog.Debug("rich card: final update failed, falling back to send", "platform", p.Name(), "error", err)
							if err := p.Send(e.ctx, replyCtx, finalCard); err != nil {
								slog.Error("failed to send rich card reply",
									"error", err,
									"session_key", sessionKey,
									"platform", p.Name(),
									"msg_id", msgID)
								return
							}
						}
					}
				} else {
					if err := p.Send(e.ctx, replyCtx, finalCard); err != nil {
						slog.Error("failed to send rich card reply",
							"error", err,
							"session_key", sessionKey,
							"platform", p.Name(),
							"msg_id", msgID)
						return
					}
				}
				for _, overflow := range parts[1:] {
					overflowCard := richCardSupporter.BuildRichCard(CardStatusDone, "", nil, overflow, false, time.Since(turnStart))
					if err := p.Send(e.ctx, replyCtx, overflowCard); err != nil {
						slog.Error("failed to send overflow rich card", "error", err)
						return
					}
				}
			} else if toolCount > 0 && segmentStart > 0 && display.Mode != displayModeStream {
				// When tool calls happened and prior text was already surfaced in segments,
				// only send the unsent remainder. When tool progress is hidden, tool events don't surface
				// side-channel messages and segmentStart stays 0, so keep normal finalize flow.
				sp.discard()
				unsent := strings.Join(textParts[segmentStart:], "")
				if strings.TrimSpace(unsent) != "" {
					unsent = appendFinalMetadataToSegment(unsent, fullResponse)
				}
				if unsent != "" {
					for _, chunk := range splitMessage(unsent, maxPlatformMessageLen) {
						if err := sendWorkspaceWithError(p, replyCtx, chunk); err != nil {
							return
						}
					}
				}
			} else if suppressDuplicate {
				sp.discard()
				slog.Debug("EventResult: suppressed duplicate side-channel text", "response_len", len(deliverResponse))
			} else if sp.finish(deliverResponse) {
				slog.Debug("EventResult: finalized stream preview in-place", "response_len", len(deliverResponse))
			} else {
				slog.Debug("EventResult: sending via p.Send (preview inactive or failed)", "response_len", len(deliverResponse), "chunks", len(splitMessage(deliverResponse, maxPlatformMessageLen)))
				for _, chunk := range splitMessage(deliverResponse, maxPlatformMessageLen) {
					if err := sendWorkspaceWithError(p, replyCtx, chunk); err != nil {
						return
					}
				}
			}

			if elapsed := time.Since(replyStart); elapsed >= slowPlatformSend {
				slog.Warn("slow final reply send", "platform", p.Name(), "elapsed", elapsed, "response_len", len(fullResponse))
			}

			// TTS: async voice reply if enabled (skipped for silent replies)
			if !isSilent && e.tts != nil && e.tts.Enabled && e.tts.TTS != nil {
				state.mu.Lock()
				fromVoice := state.fromVoice
				state.mu.Unlock()
				mode := e.tts.GetTTSMode()
				slog.Debug("tts: checking conditions", "mode", mode, "fromVoice", fromVoice, "will_send", mode == "always" || (mode == "voice_only" && fromVoice))
				if mode == "always" || (mode == "voice_only" && fromVoice) {
					go e.sendTTSReply(p, replyCtx, fullResponse)
				}
			} else {
				slog.Debug("tts: not enabled", "tts_nil", e.tts == nil, "enabled", e.tts != nil && e.tts.Enabled, "tts_obj_nil", e.tts == nil || e.tts.TTS == nil)
			}

			// Auto-compress after finishing a turn, before sending any queued messages.
			if triggerAutoCompress {
				compressor, ok := e.agent.(ContextCompressor)
				if ok && compressor.CompressCommand() != "" {
					if pendingSend != nil {
						if err := <-pendingSend; err != nil {
							slog.Debug("async send error before compress", "error", err)
						}
					}
					state.mu.Lock()
					state.lastAutoCompressAt = time.Now()
					tokenEst := state.lastAutoCompressTokens
					state.mu.Unlock()
					slog.Info("auto-compress: triggering", "session", sessionKey)

					// Notify user before compressing so they know the context is about to change.
					compressNotice := e.i18n.T(MsgCompressing)
					if tokenEst > 0 {
						compressNotice = fmt.Sprintf("%s (~%dk tokens)", compressNotice, tokenEst/1000)
					}
					e.send(state.platform, state.replyCtx, compressNotice)

					// Run compress inline while the session is still locked.
					e.runCompress(state, session, sessions, sessionKey, state.platform, state.replyCtx, true)
					return
				}
			}

			// Check for queued messages — if present, continue the event loop
			// for the next turn instead of returning. In merge mode the whole
			// queue becomes one turn (single merged prompt and reply); in
			// serial mode one message per turn (historical behavior).
			state.mu.Lock()
			if batch := takeQueuedBatchLocked(state, e.queuedMessagesMerge); len(batch) > 0 {
				merged, queuedPrompt := e.mergeQueuedBatch(batch)
				remainingQueue := len(state.pendingMessages)
				state.platform = merged.platform
				state.platformName = merged.msgPlatform
				state.replyCtx = merged.replyCtx
				state.currentMessageID = merged.messageID
				state.lastTurnMessageID = merged.messageID
				state.fromVoice = merged.fromVoice
				state.mu.Unlock()

				// Stop the previous turn's typing indicator
				if stopTyping != nil {
					stopTyping()
					stopTyping = nil
				}
				// Start a new typing indicator for the queued message's context
				if ti, ok := merged.platform.(TypingIndicator); ok {
					stopTyping = ti.StartTyping(e.ctx, merged.replyCtx)
				}
				// Agent continues working — don't add done reaction for this turn.
				doneReaction = nil

				// Drain stale events before starting the next turn. Between
				// EventResult and Send(), the only buffered events would be
				// stale leftovers (e.g. a deferred EventError from cmd.Wait()).
				//
				// Snapshot agentSession into a local so a concurrent
				// cleanupInteractiveState (idle reaper / /new / recall) that
				// nils state.agentSession between the queued-batch take and
				// here cannot cause a nil dereference below.
				agentSession := state.agentSession
				if agentSession == nil {
					slog.Debug("queued turn skipped: agent session cleaned up", "session", sessionKey)
					return
				}
				drainEvents(agentSession.Events())

				if pendingSend != nil {
					if err := <-pendingSend; err != nil {
						slog.Debug("async send error before queued turn", "error", err)
					}
				}

				nextSend := make(chan error, 1)
				go func() {
					nextSend <- agentSession.Send(queuedPrompt, merged.images, merged.files)
				}()
				pendingSend = nextSend

				// Detect language now (deferred from queue time to avoid
				// flipping locale while the previous turn is still running).
				e.i18n.DetectAndSet(merged.content)

				// Reset per-turn state for the next turn
				msgID = merged.messageID
				textParts = nil
				segmentStart = 0
				toolCount = 0
				turnStart = time.Now()
				firstEventLogged = false
				waitStart = time.Now()
				// Reassign the local replyCtx parameter to the queued messages'
				// trigger context (the last message's). state.replyCtx was
				// updated above, but the function-scope replyCtx is what gets
				// passed to p.Send / p.Reply further down — and platforms
				// derive the parent message_id from it for the reply quote.
				// Without this reassignment, the merged turn's reply would
				// quote an older bubble.
				replyCtx = merged.replyCtx
				queuedRenderer := func(content string) string {
					return e.renderOutgoingContentForWorkspace(merged.platform, content, workspaceDir)
				}
				sp = newStreamPreview(e.streamPreview, merged.platform, merged.replyCtx, e.ctx, queuedRenderer)
				cp = newCompactProgressWriter(e.ctx, merged.platform, merged.replyCtx, e.agent.Name(), e.i18n.CurrentLang(), queuedRenderer)

				// Reset streaming card state for the next turn
				streamCard = nil
				cardToolCalls = nil
				cardThinkingText = ""
				cardAnswerText.Reset()

				// Try to create a new streaming card for the queued turn
				if scp, ok := merged.platform.(StreamingCardPlatform); ok {
					if sc, err := scp.CreateStreamingCard(e.ctx, merged.replyCtx); err != nil {
						slog.Warn("streaming card creation failed for queued turn", "error", err)
					} else {
						streamCard = sc
					}
				}

				// Send instant reply for queued turn if no streaming card is active.
				if e.instantReply.Enabled && streamCard == nil {
					replyContent := e.instantReply.Content
					if replyContent == "" {
						replyContent = e.i18n.T(MsgStarting)
					}
					e.send(merged.platform, merged.replyCtx, replyContent)
				}

				// Each queued message keeps its own history entry so the Web
				// admin shows the conversation exactly as the user sent it,
				// even though the agent received them as one merged prompt.
				for _, q := range batch {
					session.AddHistory("user", q.content)
				}

				if idleTimer != nil {
					if !idleTimer.Stop() {
						select {
						case <-idleTimer.C:
						default:
						}
					}
					idleTimer.Reset(e.eventIdleTimeout)
				}

				slog.Info("processing queued messages",
					"session", sessionKey,
					"batch_size", len(batch),
					"remaining_queue", remainingQueue,
				)
				continue
			}
			state.mu.Unlock()

			if pendingSend != nil {
				if err := <-pendingSend; err != nil {
					slog.Debug("async send error after EventResult", "error", err)
				}
			}

			// Add a "done" reaction when the preview was updated in-place
			// (user only got a push for the initial send). Skip for silent
			// (NO_REPLY) turns and for rich card mode (the card itself shows
			// the done status already).
			if !isSilent && !hasRichCard && sp.needsDoneReaction() {
				if doneTI, ok := p.(TypingIndicatorDone); ok {
					doneReaction = func() { doneTI.AddDoneReaction(replyCtx) }
				}
			}

			return

		case EventError:
			cp.Finalize(ProgressCardStateFailed)
			sp.discard()
			state.mu.Lock()
			state.eventsNeedResync = true
			state.mu.Unlock()
			if event.Error != nil {
				statsErr = event.Error.Error()
			}
			// Dashboard metrics: record the failed turn (duration/tools only —
			// EventError carries no usage).
			if e.statsRecorder != nil {
				userID, userName := e.turnStatsFromState(state, sessions, sessionKey)
				platformName := ""
				state.mu.Lock()
				platformName = state.platformName
				state.mu.Unlock()
				e.recordTurnStats(statsTurnInput{
					session:       session,
					sessionKey:    sessionKey,
					platformName:  platformName,
					agentName:     replyAgent.Name(),
					userID:        userID,
					userName:      userName,
					msgID:         msgID,
					turnStart:     turnStart,
					duration:      time.Since(turnStart),
					toolCalls:     toolCount,
					tools:         toolCounts,
					err:           statsErr,
				})
			}
			if hasRichCard && cardMessageID != nil {
				errCard := richCardSupporter.BuildRichCard(CardStatusError, "", toolSteps, partialText, false, time.Since(turnStart))
				if updater, ok := p.(MessageUpdater); ok {
					if err := updater.UpdateMessage(e.ctx, cardMessageID, errCard); err != nil {
						slog.Debug("rich card: failed to update error card", "platform", p.Name(), "error", err)
					}
				}
			}
			if event.Error != nil {
				errMsg := event.Error.Error()
				slog.Error("agent error",
					"error", event.Error,
					"session_key", sessionKey,
					"platform", p.Name(),
					"msg_id", msgID)
				e.hooks.Emit(HookEvent{
					Event:      HookEventError,
					SessionKey: sessionKey,
					Platform:   p.Name(),
					Error:      event.Error.Error(),
				})
				e.send(p, replyCtx, e.sanitizeAgentError(errMsg))
			}
			// An unrecoverable agent session (e.g. a process-per-turn CLI that
			// exited silently because --resume targeted a session with no
			// backing store, such as a mistakenly-persisted subagent child id)
			// can never succeed by retrying the same binding. Detach it — the
			// old id is preserved in past_agent_session_ids for traceability —
			// so the next message starts a fresh agent session instead of
			// being stuck in a permanent zero-output failure loop.
			if v, _ := event.Metadata[EventMetadataSessionUnrecoverable].(bool); v {
				slog.Warn("agent session unrecoverable, detaching for fresh restart",
					"session_key", sessionKey,
					"agent_session", session.GetAgentSessionID())
				session.DetachAgentSession()
				sessions.Save()
			}
			// Only drop queued messages if the agent session is dead.
			// Some agents (e.g. Codex) emit EventError for per-turn failures
			// while keeping the session alive for subsequent turns.
			if state.agentSession == nil || !state.agentSession.Alive() {
				// Session is dead — run full cleanup so the old subprocess
				// (and its stdin pipe) is torn down. Without this, the dead
				// interactiveState lingers in the map until the next message
				// replaces it, and Close() is never called — leaking the
				// agent process (issue: orphaned --acp processes PPID=1).
				// cleanupInteractiveState will call notifyDroppedQueuedMessages
				// internally, so we don't call it here.
				e.cleanupInteractiveState(sessionKey, state)
			}
			return
		}
	}

channelClosed:
	// Channel closed - process exited unexpectedly
	state.mu.Lock()
	pName := state.platform.Name()
	state.mu.Unlock()
	slog.Warn("agent process exited",
		"session_key", sessionKey,
		"platform", pName,
		"msg_id", msgID)
	state.mu.Lock()
	state.eventsNeedResync = true
	state.mu.Unlock()
	e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent process exited"))
	e.cleanupInteractiveState(sessionKey, state)

	if len(textParts) > 0 {
		state.mu.Lock()
		p := state.platform
		state.mu.Unlock()

		fullResponse := strings.Join(textParts, "")
		session.AddHistory("assistant", fullResponse)

		// Respect NO_REPLY even on abnormal exit so silent turns stay silent.
		if isSilentReply(fullResponse) {
			sp.discard()
			slog.Info("silent reply suppressed (channel closed)", "session", session.ID)
			return
		}
		if stripped, ok := stripTrailingSilent(fullResponse); ok {
			if strings.TrimSpace(stripped) == "" {
				sp.discard()
				return
			}
			fullResponse = stripped
		}

		e.hooks.Emit(HookEvent{
			Event:      HookEventMessageSent,
			SessionKey: sessionKey,
			Platform:   p.Name(),
			Content:    fullResponse,
		})

		if toolCount > 0 && segmentStart > 0 {
			sp.discard()
			if segmentStart < len(textParts) {
				unsent := strings.Join(textParts[segmentStart:], "")
				if unsent != "" {
					for _, chunk := range splitMessage(unsent, maxPlatformMessageLen) {
						if err := sendWorkspaceWithError(p, replyCtx, chunk); err != nil {
							return
						}
					}
				}
			}
		} else {
			for _, chunk := range splitMessage(fullResponse, maxPlatformMessageLen) {
				if err := sendWorkspaceWithError(p, replyCtx, chunk); err != nil {
					return
				}
			}
		}
	}
}

// autoTitleListTimeout bounds the ListSessions call used to fetch the agent's
// own session summary when auto-titling a session.
const autoTitleListTimeout = 5 * time.Second

// maybeAutoTitleSession gives a placeholder-named session a descriptive title
// after its first completed turn. It prefers the agent backend's own summary
// for the current agent session (ACP session Title, claudecode/codex summary)
// and falls back to a snippet of the first user message. User-chosen names
// (non-placeholder) are never overwritten. Runs in its own goroutine from the
// turn-result handler because ListSessions may perform slow I/O.
func (e *Engine) maybeAutoTitleSession(agent Agent, sessions *SessionManager, session *Session) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in maybeAutoTitleSession", "panic", r)
		}
	}()
	if agent == nil || sessions == nil || session == nil {
		return
	}
	if !e.autoSessionTitle {
		return
	}
	if !isPlaceholderSessionName(session.GetName()) {
		return
	}

	title := ""
	if agentID := session.GetAgentSessionID(); agentID != "" {
		ctx, cancel := context.WithTimeout(e.ctx, autoTitleListTimeout)
		if infos, err := agent.ListSessions(ctx); err == nil {
			for _, info := range infos {
				if info.ID == agentID {
					if s := strings.TrimSpace(info.Summary); s != "" {
						title = s
					}
					break
				}
			}
		}
		cancel()
	}
	if title == "" {
		title = firstUserMessageSnippet(session.GetHistory(0), 30)
	}
	if title == "" {
		return
	}
	// Re-check: the user may have named the session while we were listing.
	if !isPlaceholderSessionName(session.GetName()) {
		return
	}
	session.SetName(title)
	session.SetNameAuto(true)
	sessions.Save()
	slog.Info("session auto-titled", "session_id", session.ID, "agent_session_id", session.GetAgentSessionID(), "title", title)
}

func mergeRichToolResult(steps []ToolStep, event Event, result string, maxLen int) []ToolStep {
	toolName := strings.TrimSpace(event.ToolName)
	if toolName == "" {
		toolName = "Tool"
	}

	idx := -1
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Kind == ToolStepKindThinking {
			continue
		}
		if strings.TrimSpace(steps[i].Name) == "" || strings.TrimSpace(steps[i].Name) == toolName {
			idx = i
			break
		}
	}
	if idx == -1 {
		summary := strings.TrimSpace(event.ToolInput)
		if summary != "" {
			summary = truncateIf(summary, maxLen)
		}
		steps = append(steps, ToolStep{
			Kind:    ToolStepKindTool,
			Name:    toolName,
			Summary: summary,
		})
		idx = len(steps) - 1
	}

	if strings.TrimSpace(steps[idx].Name) == "" {
		steps[idx].Name = toolName
	}
	if steps[idx].Kind == "" {
		steps[idx].Kind = ToolStepKindTool
	}
	if strings.TrimSpace(steps[idx].Summary) == "" && strings.TrimSpace(event.ToolInput) != "" {
		steps[idx].Summary = truncateIf(strings.TrimSpace(event.ToolInput), maxLen)
	}
	steps[idx].Result = result
	steps[idx].Status = strings.TrimSpace(event.ToolStatus)
	steps[idx].ExitCode = event.ToolExitCode
	steps[idx].Success = event.ToolSuccess
	steps[idx].Done = true
	return steps
}

// notifyDroppedQueuedMessages drains pendingMessages from the state and
// sends an error notification to each queued message's sender. Called when
// the event loop exits abnormally (EventError, channel closed) and queued
// messages can no longer be delivered to the agent.
func (e *Engine) notifyDroppedQueuedMessages(state *interactiveState, reason error) {
	state.mu.Lock()
	remaining := state.pendingMessages
	state.pendingMessages = nil
	state.mu.Unlock()
	for _, q := range remaining {
		e.send(q.platform, q.replyCtx, e.sanitizeAgentError(reason.Error()))
	}
}

// drainPendingMessages processes all queued messages in the state's pendingMessages
// queue. It atomically unlocks the session when the queue is empty (while holding
// state.mu) to close the race window between "queue empty" and "session unlocked".
// In merge mode each drain iteration submits the whole queue as one merged turn;
// in serial mode one message per turn (historical behavior).
// Returns true if the session was unlocked by this call.
func (e *Engine) drainPendingMessages(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string) bool {
	for {
		state.mu.Lock()
		if len(state.pendingMessages) == 0 {
			session.Unlock()
			state.mu.Unlock()
			return true
		}
		batch := takeQueuedBatchLocked(state, e.queuedMessagesMerge)
		merged, prompt := e.mergeQueuedBatch(batch)
		state.platform = merged.platform
		state.platformName = merged.msgPlatform
		state.replyCtx = merged.replyCtx
		state.currentMessageID = merged.messageID
		state.lastTurnMessageID = merged.messageID
		state.fromVoice = merged.fromVoice
		if len(batch) > 0 {
			state.turnUserID = batch[len(batch)-1].userID
			state.turnUserName = batch[len(batch)-1].userName
		}
		state.mu.Unlock()

		e.i18n.DetectAndSet(merged.content)

		if state.agentSession == nil || !state.agentSession.Alive() {
			for _, q := range batch {
				e.send(q.platform, q.replyCtx, fmt.Sprintf(e.i18n.T(MsgError), "agent session ended"))
			}
			e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent session ended"))
			return false
		}

		drained := drainEvents(state.agentSession.Events())
		if drained > 0 {
			slog.Warn("dropped buffered events before queued turn", "previous_message_id", state.lastTurnMessageID, "count", drained, "new_message_id", merged.messageID)
		}

		// Each queued message keeps its own history entry (Web admin shows
		// the conversation as sent); the agent receives one merged prompt.
		for _, q := range batch {
			session.AddHistory("user", q.content)
		}

		sendDone := make(chan error, 1)
		go func() {
			sendDone <- state.agentSession.Send(prompt, merged.images, merged.files)
		}()

		var stopTyping func()
		if ti, ok := merged.platform.(TypingIndicator); ok {
			stopTyping = ti.StartTyping(e.ctx, merged.replyCtx)
		}

		slog.Info("processing queued messages", "session", sessionKey, "batch_size", len(batch))
		e.processInteractiveEvents(state, session, sessions, sessionKey, merged.messageID, time.Now(), stopTyping, sendDone, merged.replyCtx)
	}
}

// ──────────────────────────────────────────────────────────────
// Command handling
// ──────────────────────────────────────────────────────────────

// builtinCommands maps canonical command names to their aliases/full names.
// The first entry is the canonical name used for prefix matching.
var builtinCommands = []struct {
	names []string
	id    string
}{
	{[]string{"new"}, "new"},
	{[]string{"list", "sessions"}, "list"},
	{[]string{"switch"}, "switch"},
	{[]string{"name", "rename"}, "name"},
	{[]string{"current"}, "current"},
	{[]string{"status"}, "status"},
	{[]string{"usage", "quota"}, "usage"},
	{[]string{"history"}, "history"},
	{[]string{"allow"}, "allow"},
	{[]string{"model"}, "model"},
	{[]string{"reasoning", "effort"}, "reasoning"},
	{[]string{"mode"}, "mode"},
	{[]string{"lang"}, "lang"},
	{[]string{"quiet"}, "quiet"},
	{[]string{"provider"}, "provider"},
	{[]string{"memory"}, "memory"},
	{[]string{"cron"}, "cron"},
	{[]string{"dashboard"}, "dashboard"},
	{[]string{"heartbeat", "hb"}, "heartbeat"},
	{[]string{"compress", "compact"}, "compress"},
	{[]string{"cancel"}, "cancel"},
	{[]string{"stop"}, "stop"},
	{[]string{"help"}, "help"},
	{[]string{"version"}, "version"},
	{[]string{"commands", "command", "cmd"}, "commands"},
	{[]string{"skills", "skill"}, "skills"},
	{[]string{"config"}, "config"},
	{[]string{"doctor"}, "doctor"},
	{[]string{"upgrade", "update"}, "upgrade"},
	{[]string{"restart"}, "restart"},
	{[]string{"alias"}, "alias"},
	{[]string{"delete", "del", "rm"}, "delete"},
	{[]string{"bind"}, "bind"},
	{[]string{"search", "find"}, "search"},
	{[]string{"shell", "sh", "exec", "run"}, "shell"},
	{[]string{"show"}, "show"},
	{[]string{"dir", "cd", "chdir", "workdir"}, "dir"},
	{[]string{"tts"}, "tts"},
	{[]string{"workspace", "ws"}, "workspace"},
	{[]string{"whoami", "myid"}, "whoami"},
	{[]string{"web"}, "web"},
	{[]string{"diff"}, "diff"},
	{[]string{"ps", "btw"}, "ps"},
}
