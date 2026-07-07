package core

// engine_model_cmds.go — model, reasoning, mode, quiet, TTS, stop and compress commands.
//
// Covers:
//   - cmdModel, resolveModelAlias, resolveModelSwitchTarget, modelSwitchNeedsLookup
//   - parseModelSwitchArgs, switchModel, switchModelOnAgent
//   - cmdReasoning, cmdMode, modeUsageText, applyLiveModeChange
//   - cmdQuiet, cmdTTS
//   - cmdStop, cmdCancel, stopInteractiveSession*
//   - cmdCompress, runCompress, processCompressEvents, drainQueuedMessagesAfterCompress
//
// All methods remain func (e *Engine) receivers in package core.

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) cmdModel(p Platform, msg *Message, args []string) {
	agent, sessions, interactiveKey, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}

	switcher, ok := agent.(ModelSwitcher)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgModelNotSupported))
		return
	}

	if len(args) == 0 {
		if !supportsCards(p) {
			fetchCtx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
			defer cancel()
			models := switcher.AvailableModels(fetchCtx)

			var sb strings.Builder
			current := switcher.GetModel()
			if current == "" {
				sb.WriteString(e.i18n.T(MsgModelDefault))
			} else {
				sb.WriteString(e.i18n.Tf(MsgModelCurrent, current))
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
			sb.WriteString(e.i18n.T(MsgModelListTitle))
			if len(models) == 0 {
				// No models available — common cause: the agent
				// exposes its model list only after the first
				// session handshake (e.g. ACP agents like
				// CodeBuddy). Tell the user to start a session first.
				sb.WriteString(e.i18n.T(MsgModelListEmptyHint))
				e.reply(p, msg.ReplyCtx, sb.String())
				return
			}
			var buttons [][]ButtonOption
			var row []ButtonOption
			for i, m := range models {
				marker := "  "
				if m.Name == current {
					marker = "> "
				}
				var line string
				if m.Alias != "" {
					line = fmt.Sprintf("%s%d. %s - %s\n", marker, i+1, m.Alias, m.Name)
				} else {
					desc := m.Desc
					if desc != "" {
						desc = " — " + desc
					}
					line = fmt.Sprintf("%s%d. %s%s\n", marker, i+1, m.Name, desc)
				}
				sb.WriteString(line)

				label := m.Name
				if m.Alias != "" {
					label = m.Alias
				}
				if m.Name == current {
					label = "▶ " + label
				}
				row = append(row, ButtonOption{Text: label, Data: fmt.Sprintf("cmd:/model switch %d", i+1)})
				if len(row) >= 3 {
					buttons = append(buttons, row)
					row = nil
				}
			}
			if len(row) > 0 {
				buttons = append(buttons, row)
			}
			sb.WriteString("\n")
			sb.WriteString(e.i18n.T(MsgModelUsage))
			e.replyWithButtons(p, msg.ReplyCtx, sb.String(), buttons)
			return
		}
		e.replyWithCard(p, msg.ReplyCtx, e.renderModelCard(msg.SessionKey))
		return
	}

	targetInput, ok := parseModelSwitchArgs(args)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgModelUsage))
		return
	}

	target := strings.TrimSpace(targetInput)
	if modelSwitchNeedsLookup(target) {
		fetchCtx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
		defer cancel()
		models := switcher.AvailableModels(fetchCtx)
		target = resolveModelSwitchTarget(target, models)
	}

	target, err = e.switchModelOnAgent(agent, target, agent == e.agent)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgModelChangeFailed, err))
		return
	}
	e.cleanupInteractiveState(interactiveKey)

	// Keep the existing agent session ID so the next StartSession uses
	// --resume <id> --model <new>, which lets the CLI agent restore context
	// natively without replaying history (no extra token cost).
	sessions.Save()

	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgModelChanged, target))
}

// resolveModelAlias resolves a user-supplied string to a model name.
// It first checks for an exact alias match, then falls back to the original value
// (which may be a direct model name).
func resolveModelAlias(models []ModelOption, input string) string {
	for _, m := range models {
		if m.Alias != "" && strings.EqualFold(m.Alias, input) {
			return m.Name
		}
	}
	return input
}

func resolveModelSwitchTarget(input string, models []ModelOption) string {
	input = strings.TrimSpace(input)
	if idx, err := strconv.Atoi(input); err == nil && idx >= 1 && idx <= len(models) {
		return models[idx-1].Name
	}
	if resolved := resolveModelAlias(models, input); resolved != input {
		return resolved
	}
	for _, m := range models {
		if strings.EqualFold(m.Name, input) {
			return m.Name
		}
	}
	return input
}

func modelSwitchNeedsLookup(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	if _, err := strconv.Atoi(input); err == nil {
		return true
	}
	return !strings.Contains(input, "/")
}

func parseModelSwitchArgs(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if len(args) == 1 {
		if strings.EqualFold(strings.TrimSpace(args[0]), "switch") {
			return "", false
		}
		return args[0], true
	}
	if strings.EqualFold(strings.TrimSpace(args[0]), "switch") && len(args) >= 2 {
		return strings.TrimSpace(args[1]), true
	}
	return "", false
}

// switchModel applies a runtime model selection to the global engine agent and
// persists the change so reloads keep the selected default.
func (e *Engine) switchModel(target string) (string, error) {
	return e.switchModelOnAgent(e.agent, target, true)
}

// switchModelOnAgent applies a runtime model selection to the provided agent.
// When persistConfig is true, config-backed model/provider changes are saved so
// reloads keep the new default. Workspace-scoped runtime switches pass false.
func (e *Engine) switchModelOnAgent(agent Agent, target string, persistConfig bool) (string, error) {
	switcher, ok := agent.(ModelSwitcher)
	if !ok {
		return target, nil
	}

	providerSwitcher, ok := agent.(ProviderSwitcher)
	if !ok {
		if e.modelSaveFunc != nil {
			if err := e.modelSaveFunc(target); err != nil {
				return "", fmt.Errorf("save model: %w", err)
			}
		}
		switcher.SetModel(target)
		return target, nil
	}
	active := providerSwitcher.GetActiveProvider()
	if active == nil {
		if e.modelSaveFunc != nil {
			if err := e.modelSaveFunc(target); err != nil {
				return "", fmt.Errorf("save model: %w", err)
			}
		}
		switcher.SetModel(target)
		return target, nil
	}

	providers := providerSwitcher.ListProviders()
	updated, found := SetProviderModel(providers, active.Name, target)
	if !found {
		switcher.SetModel(target)
		return target, nil
	}
	if !persistConfig {
		switcher.SetModel(target)
		return target, nil
	}
	if persistConfig && e.providerModelSaveFunc != nil {
		if err := e.providerModelSaveFunc(active.Name, target); err != nil {
			return "", fmt.Errorf("save provider model %q: %w", active.Name, err)
		}
	}
	providerSwitcher.SetProviders(updated)
	switcher.SetModel(target)
	providerSwitcher.SetActiveProvider(active.Name)
	return target, nil
}

func (e *Engine) cmdReasoning(p Platform, msg *Message, args []string) {
	switcher, ok := e.agent.(ReasoningEffortSwitcher)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgReasoningNotSupported))
		return
	}

	if len(args) == 0 {
		if !supportsCards(p) {
			efforts := switcher.AvailableReasoningEfforts()

			var sb strings.Builder
			current := switcher.GetReasoningEffort()
			if current == "" {
				sb.WriteString(e.i18n.T(MsgReasoningDefault))
			} else {
				sb.WriteString(e.i18n.Tf(MsgReasoningCurrent, current))
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
			sb.WriteString(e.i18n.T(MsgReasoningListTitle))
			var buttons [][]ButtonOption
			var row []ButtonOption
			for i, effort := range efforts {
				marker := "  "
				if effort == current {
					marker = "> "
				}
				sb.WriteString(fmt.Sprintf("%s%d. %s\n", marker, i+1, effort))

				label := effort
				if effort == current {
					label = "▶ " + label
				}
				row = append(row, ButtonOption{Text: label, Data: fmt.Sprintf("cmd:/reasoning %d", i+1)})
				if len(row) >= 3 {
					buttons = append(buttons, row)
					row = nil
				}
			}
			if len(row) > 0 {
				buttons = append(buttons, row)
			}
			sb.WriteString("\n")
			sb.WriteString(e.i18n.T(MsgReasoningUsage))
			e.replyWithButtons(p, msg.ReplyCtx, sb.String(), buttons)
			return
		}
		e.replyWithCard(p, msg.ReplyCtx, e.renderReasoningCard())
		return
	}

	efforts := switcher.AvailableReasoningEfforts()
	target := strings.ToLower(strings.TrimSpace(args[0]))
	if idx, err := strconv.Atoi(target); err == nil && idx >= 1 && idx <= len(efforts) {
		target = efforts[idx-1]
	}

	valid := false
	for _, effort := range efforts {
		if effort == target {
			valid = true
			break
		}
	}
	if !valid {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgReasoningUsage))
		return
	}

	switcher.SetReasoningEffort(target)
	e.cleanupInteractiveState(e.interactiveKeyForSessionKey(msg.SessionKey))

	s := e.sessions.GetOrCreateActive(msg.SessionKey)
	s.SetAgentSessionID("", "")
	s.ClearHistory()
	e.sessions.Save()

	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgReasoningChanged, target))
}

func (e *Engine) cmdMode(p Platform, msg *Message, args []string) {
	switcher, ok := e.agent.(ModeSwitcher)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgModeNotSupported))
		return
	}

	if len(args) == 0 {
		if !supportsCards(p) {
			current := switcher.GetMode()
			modes := switcher.PermissionModes()
			var sb strings.Builder
			zhLike := e.i18n.IsZhLike()
			for _, m := range modes {
				suffix := ""
				if m.Key == current {
					if zhLike {
						suffix = "（当前）"
					} else {
						suffix = " (current)"
					}
				}
				if zhLike {
					sb.WriteString(fmt.Sprintf("**%s**%s — %s\n", m.NameZh, suffix, m.DescZh))
				} else {
					sb.WriteString(fmt.Sprintf("**%s**%s — %s\n", m.Name, suffix, m.Desc))
				}
			}
			sb.WriteString(e.modeUsageText(modes))

			var buttons [][]ButtonOption
			var row []ButtonOption
			for _, m := range modes {
				label := m.Name
				if zhLike {
					label = m.NameZh
				}
				row = append(row, ButtonOption{Text: label, Data: "cmd:/mode " + m.Key})
				if len(row) >= 2 {
					buttons = append(buttons, row)
					row = nil
				}
			}
			if len(row) > 0 {
				buttons = append(buttons, row)
			}
			e.replyWithButtons(p, msg.ReplyCtx, sb.String(), buttons)
			return
		}
		e.replyWithCard(p, msg.ReplyCtx, e.renderModeCard())
		return
	}

	target := strings.ToLower(args[0])
	switcher.SetMode(target)
	newMode := switcher.GetMode()
	appliedLive := e.applyLiveModeChange(msg.SessionKey, newMode)

	if !appliedLive {
		e.cleanupInteractiveState(e.interactiveKeyForSessionKey(msg.SessionKey))
	}

	modes := switcher.PermissionModes()
	displayName := newMode
	zhLike := e.i18n.IsZhLike()
	for _, m := range modes {
		if m.Key == newMode {
			if zhLike {
				displayName = m.NameZh
			} else {
				displayName = m.Name
			}
			break
		}
	}
	reply := fmt.Sprintf(e.i18n.T(MsgModeChanged), displayName)
	if appliedLive {
		reply += "\n\n(Current session updated immediately.)"
	}
	e.reply(p, msg.ReplyCtx, reply)
}

func (e *Engine) modeUsageText(modes []PermissionModeInfo) string {
	keys := make([]string, 0, len(modes))
	for _, mode := range modes {
		keys = append(keys, "`"+mode.Key+"`")
	}
	return e.i18n.Tf(MsgModeUsage, strings.Join(keys, " / "))
}

func (e *Engine) applyLiveModeChange(sessionKey, mode string) bool {
	iKey := e.interactiveKeyForSessionKey(sessionKey)
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()
	if !ok || state == nil || state.agentSession == nil || !state.agentSession.Alive() {
		return false
	}
	switcher, ok := state.agentSession.(LiveModeSwitcher)
	if !ok {
		return false
	}
	return switcher.SetLiveMode(mode)
}

func (e *Engine) cmdQuiet(p Platform, msg *Message, args []string) {
	// /quiet [full|compact|quiet|stream]
	// Without argument: cycle full → quiet → compact → stream → full.
	// With argument: set mode directly.
	var newMode string
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case displayModeFull, displayModeCompact, displayModeQuiet, displayModeStream:
			newMode = strings.ToLower(args[0])
		default:
			e.reply(p, msg.ReplyCtx, "Usage: /quiet [full|compact|quiet|stream]")
			return
		}
	} else {
		switch e.display.Mode {
		case displayModeFull, "":
			newMode = displayModeQuiet
		case displayModeQuiet:
			newMode = displayModeCompact
		case displayModeCompact:
			newMode = displayModeStream
		default:
			newMode = displayModeFull
		}
	}

	e.display.Mode = newMode
	switch newMode {
	case displayModeCompact, displayModeQuiet:
		e.display.ThinkingMessages = false
		e.display.ToolMessages = false
	case displayModeStream:
		e.display.ThinkingMessages = false
		e.display.ToolMessages = true
	default:
		e.display.ThinkingMessages = true
		e.display.ToolMessages = true
	}

	if e.displaySaveFunc != nil {
		tm := e.display.ThinkingMessages
		tool := e.display.ToolMessages
		if err := e.displaySaveFunc(&newMode, &tm, nil, nil, &tool); err != nil {
			slog.Error("failed to persist display config after /quiet", "error", err)
		}
	}

	switch newMode {
	case displayModeQuiet:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietOn))
	case displayModeCompact:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDisplayModeCompact))
	case displayModeStream:
		e.reply(p, msg.ReplyCtx, "Stream mode ON: thinking hidden, tool progress merged into one live message.")
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgQuietOff))
	}
}

func (e *Engine) cmdTTS(p Platform, msg *Message, args []string) {
	if e.tts == nil || !e.tts.Enabled || e.tts.TTS == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgTTSNotEnabled))
		return
	}
	if len(args) == 0 {
		providerStr := e.tts.Provider
		if providerStr == "" {
			providerStr = "unknown"
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgTTSStatus), e.tts.GetTTSMode(), providerStr))
		return
	}
	switch args[0] {
	case "always", "voice_only":
		mode := args[0]
		e.tts.SetTTSMode(mode)
		if e.ttsSaveFunc != nil {
			if err := e.ttsSaveFunc(mode); err != nil {
				slog.Warn("tts: failed to persist mode", "error", err)
			}
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgTTSSwitched), mode))
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgTTSUsage))
	}
}

func (e *Engine) cmdStop(p Platform, msg *Message) {
	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	if !e.stopInteractiveSession(iKey, p, msg.ReplyCtx) {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNoExecution))
		return
	}
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgExecutionStopped))
}

func (e *Engine) cmdCancel(p Platform, msg *Message) {
	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()
	if !ok || state == nil {
		slog.Info("cancel: no interactive session", "session_key", msg.SessionKey)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNoTurnInProgress))
		return
	}

	// cancelCh is non-nil only while a turn's event loop is running. Claim it
	// (set to nil under the lock) so a racing second /cancel sees no turn in
	// progress instead of double-closing the channel.
	state.mu.Lock()
	agentSession := state.agentSession
	cancelCh := state.cancelCh
	state.cancelCh = nil
	state.mu.Unlock()

	if agentSession == nil || cancelCh == nil {
		slog.Info("cancel: no turn in progress", "session_key", msg.SessionKey)
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNoTurnInProgress))
		return
	}

	sid := agentSession.CurrentSessionID()
	slog.Info("cancel: sending CancelTurn", "session_key", msg.SessionKey, "agent_session_id", sid)
	// Best-effort: ask the agent backend to stop generating server-side.
	agentSession.CancelTurn()
	// Authoritative: stop the local event loop from relaying anything further.
	close(cancelCh)
	e.reply(p, msg.ReplyCtx, e.i18n.T(MsgTurnCancelled))
}

func (e *Engine) stopInteractiveSession(sessionKey string, quietPlatform Platform, quietReplyCtx any) bool {
	return e.stopInteractiveSessionWithOptions(sessionKey, true)
}

func (e *Engine) stopInteractiveSessionSilently(sessionKey string) bool {
	return e.stopInteractiveSessionWithOptions(sessionKey, false)
}

func (e *Engine) stopInteractiveSessionWithOptions(sessionKey string, notifyQueued bool) bool {
	e.interactiveMu.Lock()
	state, ok := e.interactiveStates[sessionKey]
	if !ok || state == nil {
		e.interactiveMu.Unlock()
		return false
	}

	// Stop unsolicited reader before touching state to avoid races.
	e.stopUnsolicitedReader(state)

	state.mu.Lock()
	pending := state.pending
	state.pending = nil
	agentSession := state.agentSession
	state.mu.Unlock()

	state.markStopped()
	delete(e.interactiveStates, sessionKey)
	e.interactiveMu.Unlock()

	if pending != nil {
		pending.resolve()
	}
	if notifyQueued {
		e.notifyDroppedQueuedMessages(state, fmt.Errorf("session reset"))
	} else {
		state.mu.Lock()
		state.pendingMessages = nil
		state.mu.Unlock()
	}
	e.closeAgentSessionAsync(sessionKey, agentSession)

	e.hooks.Emit(HookEvent{
		Event:      HookEventSessionEnded,
		SessionKey: sessionKey,
	})

	return true
}

func (e *Engine) cmdCompress(p Platform, msg *Message) {
	compressor, ok := e.agent.(ContextCompressor)
	if !ok || compressor.CompressCommand() == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCompressNotSupported))
		return
	}

	iKey := e.interactiveKeyForSessionKey(msg.SessionKey)
	e.interactiveMu.Lock()
	state, hasState := e.interactiveStates[iKey]
	e.interactiveMu.Unlock()

	if !hasState || state == nil || state.agentSession == nil || !state.agentSession.Alive() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCompressNoSession))
		return
	}

	_, sessions := e.sessionContextForKey(msg.SessionKey)
	session := sessions.GetOrCreateActive(msg.SessionKey)
	if !session.TryLock() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPreviousProcessing))
		return
	}

	e.send(p, msg.ReplyCtx, e.i18n.T(MsgCompressing))

	go e.runCompress(state, session, sessions, iKey, p, msg.ReplyCtx, false)
}

// runCompress sends the agent's compress command and handles results.
// If autoTriggered is true, suppress user-visible "compressing" and completion messages.
func (e *Engine) runCompress(state *interactiveState, session *Session, sessions *SessionManager, iKey string, p Platform, replyCtx any, auto bool) {
	// session.Unlock() is called inside drainQueuedMessagesAfterCompress
	// while holding state.mu to close the race window. Deferred fallback
	// ensures the lock is released on early-return paths.
	compressUnlocked := false
	defer func() {
		if !compressUnlocked {
			session.Unlock()
		}
	}()

	// Stop unsolicited reader before taking event channel ownership.
	e.stopUnsolicitedReader(state)

	state.mu.Lock()
	state.platform = p
	state.replyCtx = replyCtx
	state.mu.Unlock()

	drainEvents(state.agentSession.Events())

	compressor, ok := e.agent.(ContextCompressor)
	if !ok || compressor.CompressCommand() == "" {
		if !auto {
			e.reply(p, replyCtx, e.i18n.T(MsgCompressNotSupported))
		}
		return
	}

	cmd := compressor.CompressCommand()
	if err := state.agentSession.Send(cmd, nil, nil); err != nil {
		if !auto {
			e.reply(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), err))
		}
		if !state.agentSession.Alive() {
			e.cleanupInteractiveState(iKey)
		}
		return
	}

	e.processCompressEvents(state, session, sessions, iKey, p, replyCtx, &compressUnlocked, auto)
}

// processCompressEvents drains agent events after a compress command.
// Unlike processInteractiveEvents it does NOT record history and treats
// an empty result as success rather than "(empty response)".
func (e *Engine) processCompressEvents(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, p Platform, replyCtx any, unlocked *bool, auto bool) {

	var textParts []string
	events := state.agentSession.Events()
	stopCh := state.stopSignal()

	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if e.eventIdleTimeout > 0 {
		idleTimer = time.NewTimer(e.eventIdleTimeout)
		defer idleTimer.Stop()
		idleCh = idleTimer.C
	}

	for {
		var event Event
		var ok bool

		select {
		case <-stopCh:
			return
		case event, ok = <-events:
			if !ok {
				e.cleanupInteractiveState(sessionKey, state)
				if !auto {
					if len(textParts) > 0 {
						e.send(p, replyCtx, strings.Join(textParts, ""))
					} else {
						e.reply(p, replyCtx, e.i18n.T(MsgCompressDone))
					}
				}
				e.notifyDroppedQueuedMessages(state, fmt.Errorf("agent process exited during compress"))
				return
			}
		case <-idleCh:
			if !auto {
				e.send(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), "compress timed out"))
			}
			e.cleanupInteractiveState(sessionKey, state)
			e.notifyDroppedQueuedMessages(state, fmt.Errorf("compress timed out"))
			return
		case <-e.ctx.Done():
			return
		}

		if state.isStopped() {
			return
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

		switch event.Type {
		case EventText:
			if !auto && event.Content != "" {
				textParts = append(textParts, event.Content)
			}
		case EventToolResult:
			if !auto {
				out := strings.TrimSpace(event.Content)
				if out == "" {
					out = strings.TrimSpace(event.ToolResult)
				}
				if out == "" {
					break
				}
				tn := strings.TrimSpace(event.ToolName)
				if tn == "" {
					tn = "tool"
				}
				textParts = append(textParts, fmt.Sprintf(e.i18n.T(MsgToolResult), tn, out)+"\n")
			}
		case EventResult:
			result := event.Content
			if result == "" && len(textParts) > 0 {
				result = strings.Join(textParts, "")
			}
			if !auto {
				if result != "" {
					e.send(p, replyCtx, result)
				} else {
					e.reply(p, replyCtx, e.i18n.T(MsgCompressDone))
				}
			}

			// After compress succeeds, process any queued messages instead of dropping them.
			e.drainQueuedMessagesAfterCompress(state, session, sessions, sessionKey, unlocked)
			return
		case EventError:
			if !auto && event.Error != nil {
				e.reply(p, replyCtx, fmt.Sprintf(e.i18n.T(MsgError), event.Error))
			}
			// Only drop queued messages if the agent is dead; some agents
			// emit per-turn EventError while staying alive.
			if !state.agentSession.Alive() {
				e.notifyDroppedQueuedMessages(state, event.Error)
			} else {
				// Agent survived — try to process queued messages.
				e.drainQueuedMessagesAfterCompress(state, session, sessions, sessionKey, unlocked)
			}
			return
		case EventPermissionRequest:
			_ = state.agentSession.RespondPermission(event.RequestID, PermissionResult{
				Behavior:     "allow",
				UpdatedInput: event.ToolInputRaw,
			})
		}
	}
}

// drainQueuedMessagesAfterCompress processes any messages that were queued
// during a /compress operation. It sends each one to the agent and runs the
// full interactive event loop for it.
func (e *Engine) drainQueuedMessagesAfterCompress(state *interactiveState, session *Session, sessions *SessionManager, sessionKey string, unlocked *bool) {
	if e.drainPendingMessages(state, session, sessions, sessionKey) {
		*unlocked = true
	}
}
