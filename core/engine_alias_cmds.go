package core

// engine_alias_cmds.go — alias, delete-session, and miscellaneous helpers.
//
// Covers:
//   - cmdAlias, cmdAliasList, cmdAliasAdd, cmdAliasDel
//   - cmdDelete, isExplicitDeleteBatchArg, parseDeleteBatchIndices
//   - cmdDeleteBatch, deleteSingleSession, deleteSingleSessionReply
//   - deleteSessionDisplayName
//   - toolCodeLang, formatToolResultEventFallback
//   - truncateIf, splitMessage, sendTTSReply
//
// All methods remain func (e *Engine) receivers in package core.

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)
func (e *Engine) cmdAlias(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		if !supportsCards(p) {
			e.cmdAliasList(p, msg)
			return
		}
		e.replyWithCard(p, msg.ReplyCtx, e.renderAliasCard())
		return
	}

	sub := matchSubCommand(strings.ToLower(args[0]), []string{"list", "add", "del", "delete", "remove"})
	switch sub {
	case "list":
		e.cmdAliasList(p, msg)
	case "add":
		e.cmdAliasAdd(p, msg, args[1:])
	case "del", "delete", "remove":
		e.cmdAliasDel(p, msg, args[1:])
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgAliasUsage))
	}
}

func (e *Engine) cmdAliasList(p Platform, msg *Message) {
	e.aliasMu.RLock()
	defer e.aliasMu.RUnlock()

	if len(e.aliases) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgAliasEmpty))
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgAliasListHeader), len(e.aliases)))
	sb.WriteString("\n")

	names := make([]string, 0, len(e.aliases))
	for n := range e.aliases {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		sb.WriteString(fmt.Sprintf("  %s → %s\n", n, e.aliases[n]))
	}
	e.reply(p, msg.ReplyCtx, strings.TrimRight(sb.String(), "\n"))
}

func (e *Engine) cmdAliasAdd(p Platform, msg *Message, args []string) {
	if len(args) < 2 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgAliasUsage))
		return
	}
	name := args[0]
	command := strings.Join(args[1:], " ")
	if !strings.HasPrefix(command, "/") {
		command = "/" + command
	}

	e.aliasMu.Lock()
	e.aliases[name] = command
	e.aliasMu.Unlock()

	if e.aliasSaveAddFunc != nil {
		if err := e.aliasSaveAddFunc(name, command); err != nil {
			slog.Error("alias: save failed", "error", err)
		}
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAliasAdded), name, command))
}

func (e *Engine) cmdAliasDel(p Platform, msg *Message, args []string) {
	if len(args) < 1 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgAliasUsage))
		return
	}
	name := args[0]

	e.aliasMu.Lock()
	_, exists := e.aliases[name]
	if exists {
		delete(e.aliases, name)
	}
	e.aliasMu.Unlock()

	if !exists {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAliasNotFound), name))
		return
	}

	if e.aliasSaveDelFunc != nil {
		if err := e.aliasSaveDelFunc(name); err != nil {
			slog.Error("alias: save failed", "error", err)
		}
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAliasDeleted), name))
}

func (e *Engine) cmdDelete(p Platform, msg *Message, args []string) {
	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	deleter, ok := agent.(SessionDeleter)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteNotSupported))
		return
	}

	if len(args) == 0 {
		if supportsCards(p) {
			_ = e.getOrCreateDeleteModeState(msg.SessionKey, p, msg.ReplyCtx)
			e.replyWithCard(p, msg.ReplyCtx, e.renderDeleteModeCard(msg.SessionKey))
			return
		}
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteUsage))
		return
	}
	if len(args) > 1 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteUsage))
		return
	}

	agentSessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}
	agentSessions = e.applySessionFilter(agentSessions, sessions)

	prefix := strings.TrimSpace(args[0])
	if isExplicitDeleteBatchArg(prefix) {
		indices, err := parseDeleteBatchIndices(prefix, len(agentSessions))
		if err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteUsage))
			return
		}
		e.cmdDeleteBatch(p, msg, deleter, agentSessions, indices)
		return
	}
	var matched *AgentSessionInfo

	if idx, err := strconv.Atoi(prefix); err == nil && idx >= 1 && idx <= len(agentSessions) {
		matched = &agentSessions[idx-1]
	} else {
		for i := range agentSessions {
			if strings.HasPrefix(agentSessions[i].ID, prefix) {
				matched = &agentSessions[i]
				break
			}
		}
	}

	if matched == nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSwitchNoMatch), prefix))
		return
	}

	e.deleteSingleSession(p, msg, deleter, matched)
}

func isExplicitDeleteBatchArg(arg string) bool {
	if strings.Contains(arg, ",") {
		return true
	}
	if !strings.Contains(arg, "-") {
		return false
	}
	for _, r := range arg {
		if (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func parseDeleteBatchIndices(spec string, max int) ([]int, error) {
	parts := strings.Split(spec, ",")
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty batch spec")
	}
	seen := make(map[int]struct{}, len(parts))
	indices := make([]int, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty batch item")
		}

		if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			if len(bounds) != 2 || bounds[0] == "" || bounds[1] == "" {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			start, err := strconv.Atoi(bounds[0])
			if err != nil {
				return nil, err
			}
			end, err := strconv.Atoi(bounds[1])
			if err != nil {
				return nil, err
			}
			if start < 1 || end < 1 || start > end || end > max {
				return nil, fmt.Errorf("range %q out of bounds", part)
			}
			for idx := start; idx <= end; idx++ {
				if _, ok := seen[idx]; ok {
					continue
				}
				seen[idx] = struct{}{}
				indices = append(indices, idx)
			}
			continue
		}

		idx, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		if idx < 1 || idx > max {
			return nil, fmt.Errorf("index %d out of bounds", idx)
		}
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		indices = append(indices, idx)
	}

	return indices, nil
}

func (e *Engine) cmdDeleteBatch(p Platform, msg *Message, deleter SessionDeleter, sessions []AgentSessionInfo, indices []int) {
	lines := make([]string, 0, len(indices))
	for _, idx := range indices {
		matched := &sessions[idx-1]
		if line := e.deleteSingleSessionReply(msg, deleter, matched); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDeleteUsage))
		return
	}
	e.reply(p, msg.ReplyCtx, strings.Join(lines, "\n"))
}

func (e *Engine) deleteSingleSession(p Platform, msg *Message, deleter SessionDeleter, matched *AgentSessionInfo) {
	e.reply(p, msg.ReplyCtx, e.deleteSingleSessionReply(msg, deleter, matched))
}

func (e *Engine) deleteSingleSessionReply(msg *Message, deleter SessionDeleter, matched *AgentSessionInfo) string {
	if matched == nil {
		return ""
	}

	// Prevent deleting the currently active session
	_, sessions := e.sessionContextForKey(msg.SessionKey)
	activeSession := sessions.GetOrCreateActive(msg.SessionKey)
	if activeSession.GetAgentSessionID() == matched.ID {
		return e.i18n.T(MsgDeleteActiveDenied)
	}

	displayName := e.deleteSessionDisplayName(sessions, matched)

	if err := deleter.DeleteSession(e.ctx, matched.ID); err != nil {
		return e.i18n.Tf(MsgFailedToDeleteSession, displayName, err)
	}

	// Keep local session snapshot aligned with agent-side deletion.
	sessions.DeleteByAgentSessionID(matched.ID)
	sessions.SetSessionName(matched.ID, "")
	return fmt.Sprintf(e.i18n.T(MsgDeleteSuccess), displayName)
}

func (e *Engine) deleteSessionDisplayName(sessions *SessionManager, matched *AgentSessionInfo) string {
	displayName := sessions.GetSessionName(matched.ID)
	if displayName == "" {
		displayName = matched.Summary
	}
	if displayName == "" {
		shortID := matched.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		displayName = shortID
	}
	return displayName
}

// toolCodeLang picks the code block language hint for tool display.
func toolCodeLang(toolName, input string) string {
	switch toolName {
	case "shell", "run_shell_command", "Bash":
		return "bash"
	case "write_file", "WriteFile", "replace", "ReplaceInFile":
		if strings.Contains(input, "\n- ") || strings.Contains(input, "\n+ ") {
			return "diff"
		}
	}
	// Fallback: detect diff-like content
	if strings.Contains(input, "\n- ") && strings.Contains(input, "\n+ ") {
		return "diff"
	}
	return ""
}

func (e *Engine) formatToolResultEventFallback(toolName, result, status string, exitCode *int, success *bool) string {
	statusLabel := e.i18n.T(MsgToolResultFmtStatus)
	exitLabel := e.i18n.T(MsgToolResultFmtExit)
	noOutput := e.i18n.T(MsgToolResultFmtNoOutput)
	dot := "⚪"
	if success != nil {
		if *success {
			dot = "🟢"
		} else {
			dot = "🔴"
		}
	}
	var lines []string
	first := "🧾"
	if strings.TrimSpace(toolName) != "" {
		first += " " + strings.TrimSpace(toolName)
	}
	lines = append(lines, first)
	if strings.TrimSpace(status) != "" || success != nil {
		s := strings.TrimSpace(status)
		if s == "" {
			if success != nil && *success {
				s = e.i18n.T(MsgToolResultFmtOk)
			} else if success != nil && !*success {
				s = e.i18n.T(MsgToolResultFmtFailed)
			}
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", dot, statusLabel, s))
	}
	if exitCode != nil {
		lines = append(lines, fmt.Sprintf("🔢 %s: %d", exitLabel, *exitCode))
	}
	if strings.TrimSpace(result) != "" {
		lines = append(lines, "```text\n"+strings.TrimSpace(result)+"\n```")
	} else {
		lines = append(lines, "_"+noOutput+"_")
	}
	return strings.Join(lines, "\n")
}

// truncateIf truncates s to maxLen runes. 0 means no truncation.
func truncateIf(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	return string([]rune(s)[:maxLen]) + "..."
}

func splitMessage(text string, maxLen int) []string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}
	var chunks []string

	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}

		end := maxLen

		// Try to split at newline boundary within the rune window.
		// Convert the candidate chunk back to a string for newline search.
		candidate := string(runes[:end])
		if idx := strings.LastIndex(candidate, "\n"); idx > 0 {
			// idx is a byte offset within candidate; convert to rune offset.
			runeIdx := utf8.RuneCountInString(candidate[:idx])
			if runeIdx >= end/2 {
				end = runeIdx + 1
			}
		}

		chunks = append(chunks, string(runes[:end]))
		runes = runes[end:]
	}
	return chunks
}

// sendTTSReply synthesizes fullResponse text and sends audio to the platform.
// Called asynchronously after EventResult; text reply is always sent first.
func (e *Engine) sendTTSReply(p Platform, replyCtx any, text string) {
	slog.Debug("tts: sendTTSReply called", "platform", p.Name(), "text_len", len(text))
	if e.tts == nil {
		slog.Warn("tts: e.tts is nil, skipping")
		return
	}
	if e.tts.TTS == nil {
		slog.Warn("tts: e.tts.TTS is nil, skipping")
		return
	}
	if e.tts.MaxTextLen > 0 && utf8.RuneCountInString(text) > e.tts.MaxTextLen {
		slog.Warn("tts: text exceeds max_text_len, skipping synthesis", "len", utf8.RuneCountInString(text), "max", e.tts.MaxTextLen)
		return
	}
	slog.Info("tts: starting synthesis", "voice", e.tts.Voice, "text_len", len(text))
	opts := TTSSynthesisOpts{Voice: e.tts.Voice}
	audioData, format, err := e.tts.TTS.Synthesize(e.ctx, StripMarkdown(text), opts)
	if err != nil {
		slog.Error("tts: synthesis failed", "error", err)
		return
	}
	slog.Info("tts: synthesis successful", "format", format, "audio_size", len(audioData))
	as, ok := p.(AudioSender)
	if !ok {
		slog.Warn("tts: platform does not support audio sending", "platform", p.Name())
		return
	}
	if err := as.SendAudio(e.ctx, replyCtx, audioData, format); err != nil {
		slog.Error("tts: platform audio send failed", "platform", p.Name(), "error", err)
		return
	}
	slog.Info("tts: audio sent successfully", "platform", p.Name())
}

