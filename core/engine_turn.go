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

			state.mu.Lock()
			p := state.platform
			replyCtx := state.replyCtx
			state.mu.Unlock()

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
					e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), event.Error))
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

var agentErrorHandlers = []agentErrorHandler{
	{"Session not found", MsgSessionNotFound},
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
	streamPreviewToolHold := sp.previewMode() == "tool_hold" && e.display.Mode == displayModeStream

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

	events := state.agentSession.Events()
	stopCh := state.stopSignal()

	// cancelCh is a per-turn signal that /cancel closes to make this loop stop
	// relaying further events. It is created fresh each turn and cleared on
	// exit, so its scope is exactly "the currently running turn" — the session
	// itself stays alive for the next message.
	cancelCh := make(chan struct{})
	state.mu.Lock()
	state.cancelCh = cancelCh
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
				slog.Error("failed to send prompt", "error", err, "session_key", sessionKey)
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
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
				return
			}
			continue
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

		if !firstEventLogged {
			firstEventLogged = true
			if elapsed := time.Since(waitStart); elapsed >= slowAgentFirstEvent {
				slog.Warn("slow agent first event", "elapsed", elapsed, "session", sessionKey, "event_type", event.Type)
			}
		}

		state.mu.Lock()
		p := state.platform
		state.mu.Unlock()

		// main codebase has no per-session quiet flag; pr309 referenced
		// sessionQuiet which we drop. e.display.ThinkingMessages /
		// ToolMessages handle user-level quiet in the fallback branches.
		richCardSupporter, hasRichCard := p.(RichCardSupporter)
		// Card 2.0 rich-card path is opt-in via [display] mode = "rich".
		// Default "legacy" keeps upstream behavior for all platforms.
		if e.display.CardMode != "rich" {
			hasRichCard = false
		}

		switch event.Type {
		case EventThinking:
			if isEllipsisOnly(event.Content) {
				break
			}
			if hasRichCard {
				// When thinking messages are suppressed, skip card creation.
				if !e.display.ThinkingMessages {
					break
				}
				if thinking := strings.TrimSpace(truncateIf(event.Content, e.display.ThinkingMaxLen)); thinking != "" {
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
			if !e.display.ThinkingMessages && len(textParts) > segmentStart {
				if e.display.Mode == displayModeQuiet || e.display.Mode == displayModeStream {
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
			if e.display.ThinkingMessages && event.Content != "" {
				// --- StreamingCard path ---
				if streamCard != nil && !streamCard.Failed() {
					cardThinkingText = truncateIf(event.Content, e.display.ThinkingMaxLen)
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
				preview := truncateIf(event.Content, e.display.ThinkingMaxLen)
				thinkingMsg := fmt.Sprintf(e.i18n.T(MsgThinking), preview)
				if !cp.AppendEvent(ProgressEntryThinking, preview, "", thinkingMsg) {
					sendWorkspace(p, replyCtx, thinkingMsg)
				}
			}

		case EventToolUse:
			toolCount++
			toolInput := event.ToolInput
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
				if !e.display.ToolMessages {
					break
				}
				toolSteps = append(toolSteps, ToolStep{
					Kind:    ToolStepKindTool,
					Name:    event.ToolName,
					Summary: truncateIf(event.ToolInput, e.display.ToolMaxLen),
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
				if e.display.ToolMessages {
					// Route tool progress to ProgressAssembler side-channel instead of
					// polluting visibleText. Tool progress is a UI side-channel, never
					// enters the model-produced answer text.
					if assembler, ok := p.(ProgressAssembler); ok {
						_ = assembler.OnToolStart(sp.ctx, sp.previewMsgID, event.ToolName, formattedInput, event.ToolInput)
					}
				}
				continue
			}
			if e.display.Mode == displayModeStream && e.display.ToolMessages {
				toolMsg := fmt.Sprintf(e.i18n.T(MsgTool), toolCount, event.ToolName, formattedInput)
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
			if !e.display.ToolMessages && len(textParts) > segmentStart {
				if e.display.Mode == displayModeQuiet || e.display.Mode == displayModeStream {
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
			if e.display.ToolMessages {
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
				toolMsg := fmt.Sprintf(e.i18n.T(MsgTool), toolCount, event.ToolName, formattedInput)
				if !cp.AppendEvent(ProgressEntryToolUse, toolInput, event.ToolName, toolMsg) {
					for _, chunk := range SplitMessageCodeFenceAware(toolMsg, maxPlatformMessageLen) {
						sendWorkspace(p, replyCtx, chunk)
					}
				}
			}

		case EventToolResult:
			if e.display.ToolMessages {
				result := strings.TrimSpace(event.ToolResult)
				if result == "" {
					result = strings.TrimSpace(event.Content)
				}
				if result != "" {
					result = truncateIf(result, e.display.ToolMaxLen)
				}
				if result != "" || event.ToolStatus != "" || event.ToolExitCode != nil || event.ToolSuccess != nil {
					if hasRichCard {
						toolSteps = mergeRichToolResult(toolSteps, event, result, e.display.ToolMaxLen)
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
					resultMsg := e.formatToolResultEventFallback(event.ToolName, result, event.ToolStatus, event.ToolExitCode, event.ToolSuccess)
					if streamPreviewToolHold {
						if e.display.ToolMessages {
							// Route tool result to ProgressAssembler side-channel.
							if assembler, ok := p.(ProgressAssembler); ok {
								_ = assembler.OnToolComplete(sp.ctx, sp.previewMsgID, event.ToolName, result)
							}
						}
						continue
					}
					if e.display.Mode == displayModeStream {
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
						Kind:     ProgressEntryToolResult,
						Tool:     event.ToolName,
						Text:     result,
						Status:   event.ToolStatus,
						ExitCode: event.ToolExitCode,
						Success:  event.ToolSuccess,
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
				permLimit := e.display.ToolMaxLen
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
						if pendingName != "" && pendingName != "session" && pendingName != "default" {
							sessions.SetSessionName(currentID, pendingName)
						}
					}
					sessions.Save()
				}
			}

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
			if e.display.Mode == displayModeStream && !isSilent {
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
					slog.Error("streaming card finalize failed, sending fallback", "error", err)
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
								slog.Error("failed to send rich card reply", "error", err)
								return
							}
						}
					}
				} else {
					if err := p.Send(e.ctx, replyCtx, finalCard); err != nil {
						slog.Error("failed to send rich card reply", "error", err)
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
			} else if toolCount > 0 && segmentStart > 0 && e.display.Mode != displayModeStream {
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
			// for the next turn instead of returning.
			state.mu.Lock()
			if len(state.pendingMessages) > 0 {
				queued := state.pendingMessages[0]
				state.pendingMessages = state.pendingMessages[1:]
				remainingQueue := len(state.pendingMessages)
				state.platform = queued.platform
				state.replyCtx = queued.replyCtx
				state.currentMessageID = queued.messageID
				state.lastTurnMessageID = queued.messageID
				state.fromVoice = queued.fromVoice
				state.mu.Unlock()

				// Stop the previous turn's typing indicator
				if stopTyping != nil {
					stopTyping()
					stopTyping = nil
				}
				// Start a new typing indicator for the queued message's context
				if ti, ok := queued.platform.(TypingIndicator); ok {
					stopTyping = ti.StartTyping(e.ctx, queued.replyCtx)
				}
				// Agent continues working — don't add done reaction for this turn.
				doneReaction = nil

				// Drain stale events before starting the next turn. Between
				// EventResult and Send(), the only buffered events would be
				// stale leftovers (e.g. a deferred EventError from cmd.Wait()).
				drainEvents(state.agentSession.Events())

				if pendingSend != nil {
					if err := <-pendingSend; err != nil {
						slog.Debug("async send error before queued turn", "error", err)
					}
				}

				queuedPrompt := e.buildSenderPrompt(queued.content, queued.userID, queued.userName, queued.msgPlatform, queued.msgSessionKey, queued.channelKey)

				nextSend := make(chan error, 1)
				go func() {
					nextSend <- state.agentSession.Send(queuedPrompt, queued.images, queued.files)
				}()
				pendingSend = nextSend

				// Detect language now (deferred from queue time to avoid
				// flipping locale while the previous turn is still running).
				e.i18n.DetectAndSet(queued.content)

				// Reset per-turn state for the next turn
				msgID = queued.messageID
				textParts = nil
				segmentStart = 0
				toolCount = 0
				turnStart = time.Now()
				firstEventLogged = false
				waitStart = time.Now()
				// Reassign the local replyCtx parameter to the queued message's
				// trigger context. state.replyCtx was updated above, but the
				// function-scope replyCtx is what gets passed to p.Send / p.Reply
				// further down — and platforms derive the parent message_id from
				// it for the reply quote. Without this reassignment, msg2's
				// reply would quote msg1's bubble.
				replyCtx = queued.replyCtx
				queuedRenderer := func(content string) string {
					return e.renderOutgoingContentForWorkspace(queued.platform, content, workspaceDir)
				}
				sp = newStreamPreview(e.streamPreview, queued.platform, queued.replyCtx, e.ctx, queuedRenderer)
				cp = newCompactProgressWriter(e.ctx, queued.platform, queued.replyCtx, e.agent.Name(), e.i18n.CurrentLang(), queuedRenderer)

				// Reset streaming card state for the next turn
				streamCard = nil
				cardToolCalls = nil
				cardThinkingText = ""
				cardAnswerText.Reset()

				// Try to create a new streaming card for the queued turn
				if scp, ok := queued.platform.(StreamingCardPlatform); ok {
					if sc, err := scp.CreateStreamingCard(e.ctx, queued.replyCtx); err != nil {
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
					e.send(queued.platform, queued.replyCtx, replyContent)
				}

				session.AddHistory("user", queued.content)

				if idleTimer != nil {
					if !idleTimer.Stop() {
						select {
						case <-idleTimer.C:
						default:
						}
					}
					idleTimer.Reset(e.eventIdleTimeout)
				}

				slog.Info("processing queued message",
					"session", sessionKey,
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
				slog.Error("agent error", "error", event.Error)
				e.hooks.Emit(HookEvent{
					Event:      HookEventError,
					SessionKey: sessionKey,
					Platform:   p.Name(),
					Error:      event.Error.Error(),
				})
				userMsg := fmt.Sprintf(e.i18n.T(MsgError), errMsg)
				for _, h := range agentErrorHandlers {
					if strings.Contains(errMsg, h.contains) {
						userMsg = e.i18n.T(h.msgKey)
						break
					}
				}
				e.send(p, replyCtx, userMsg)
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
	slog.Warn("agent process exited", "session_key", sessionKey)
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
		e.send(q.platform, q.replyCtx, fmt.Sprintf(e.i18n.T(MsgError), reason))
	}
}

// drainPendingMessages processes all queued messages in the state's pendingMessages
// queue. It atomically unlocks the session when the queue is empty (while holding
// state.mu) to close the race window between "queue empty" and "session unlocked".
// Returns true if the session was unlocked by this call.
func (e *Engine) drainPendingMessages(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string) bool {
	for {
		state.mu.Lock()
		if len(state.pendingMessages) == 0 {
			session.Unlock()
			state.mu.Unlock()
			return true
		}
		queued := state.pendingMessages[0]
		state.pendingMessages = state.pendingMessages[1:]
		state.platform = queued.platform
		state.replyCtx = queued.replyCtx
		state.currentMessageID = queued.messageID
		state.lastTurnMessageID = queued.messageID
		state.fromVoice = queued.fromVoice
		state.mu.Unlock()

		e.i18n.DetectAndSet(queued.content)
		prompt := e.buildSenderPrompt(queued.content, queued.userID, queued.userName, queued.msgPlatform, queued.msgSessionKey, queued.channelKey)

		if state.agentSession == nil || !state.agentSession.Alive() {
			e.send(queued.platform, queued.replyCtx, fmt.Sprintf(e.i18n.T(MsgError), "agent session ended"))
			e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent session ended"))
			return false
		}

		drained := drainEvents(state.agentSession.Events())
		if drained > 0 {
			slog.Warn("dropped buffered events before queued turn", "previous_message_id", state.lastTurnMessageID, "count", drained, "new_message_id", queued.messageID)
		}

		session.AddHistory("user", queued.content)

		sendDone := make(chan error, 1)
		go func() {
			sendDone <- state.agentSession.Send(prompt, queued.images, queued.files)
		}()

		var stopTyping func()
		if ti, ok := queued.platform.(TypingIndicator); ok {
			stopTyping = ti.StartTyping(e.ctx, queued.replyCtx)
		}

		slog.Info("processing queued message", "session", sessionKey)
		e.processInteractiveEvents(state, session, sessions, sessionKey, queued.messageID, time.Now(), stopTyping, sendDone, queued.replyCtx)
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
