package core

// engine_session_cmds.go — session lifecycle commands and reply-footer helpers.
//
// Covers:
//   - cmdNew, cmdList, cmdSwitch (session CRUD)
//   - matchSession, commandWorkDir
//   - applySessionFilter, filterOwnedSessions
//   - buildReplyFooter + all replyFooter* helpers
//   - compactReplyFooterPath, appendReplyFooter
//   - appendFinalMetadataToSegment, mergeStreamDisplayContent
//
// All methods remain func (e *Engine) receivers in package core.

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (e *Engine) cmdNew(p Platform, msg *Message, args []string) {
	_, sessions, interactiveKey, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}

	slog.Info("cmdNew: cleaning up old session", "session_key", msg.SessionKey)
	e.cleanupInteractiveState(interactiveKey)
	slog.Info("cmdNew: cleanup done, creating new session", "session_key", msg.SessionKey)

	// Clear old session's agent session ID so it cannot be resumed
	old := sessions.GetOrCreateActive(msg.SessionKey)
	old.SetAgentSessionID("", "")
	old.ClearHistory()
	sessions.Save()

	name := ""
	if len(args) > 0 {
		name = strings.Join(args, " ")
	}
	sessions.NewSession(msg.SessionKey, name)
	if name != "" {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgNewSessionCreatedName), name))
	} else {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNewSessionCreated))
	}
}

// applySessionFilter conditionally filters agent sessions based on the
// filter_external_sessions config. When disabled (default), all sessions are
// returned. When enabled, only sessions tracked by cc-connect are shown.
func (e *Engine) applySessionFilter(sessions []AgentSessionInfo, sm *SessionManager) []AgentSessionInfo {
	if !e.filterExternalSessions {
		return sessions
	}
	return filterOwnedSessions(sessions, sm.KnownAgentSessionIDs())
}

// filterOwnedSessions removes agent sessions that are not tracked by cc-connect's
// session manager. This prevents external CLI sessions in the same work_dir from
// appearing in /list, /switch, /delete, etc. If the session manager has no tracked
// agent sessions at all (e.g. first run), all sessions are returned unfiltered.
func filterOwnedSessions(sessions []AgentSessionInfo, known map[string]struct{}) []AgentSessionInfo {
	if len(known) == 0 {
		return sessions
	}
	filtered := make([]AgentSessionInfo, 0, len(sessions))
	for _, s := range sessions {
		if _, ok := known[s.ID]; ok {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

const listPageSize = 20

// dirCardPageSize is the max directory history rows per card page (Feishu / other card UIs).
const dirCardPageSize = 20

// sessionsFromSessionManager builds a fallback session list from
// cc-connect's own SessionManager, scoped to userKey. Used when the
// agent backend does not report sessions (e.g. ACP servers without
// session/list support like CodeBuddy). Only sessions with a non-empty
// AgentSessionID and matching the given agent name are included.
//
// userKey is msg.SessionKey (NOT interactiveKey): the SessionManager is
// already per-workspace in multi-workspace mode, and within it userKey
// is the plain session key. This prevents one user from seeing another
// user's sessions via the fallback path.
func sessionsFromSessionManager(sm *SessionManager, agentName, userKey string) []AgentSessionInfo {
	all := sm.ListSessions(userKey)
	out := make([]AgentSessionInfo, 0, len(all))
	for _, s := range all {
		agentSID := s.GetAgentSessionID()
		if agentSID == "" || agentSID == ContinueSession {
			continue
		}
		if name := s.GetAgentName(); name != "" && name != agentName {
			continue
		}
		history := s.GetHistory(0)
		info := AgentSessionInfo{
			ID:           agentSID,
			Summary:      lastUserMessageSnippet(history, 30),
			MessageCount: len(history),
			ModifiedAt:   s.GetUpdatedAt(),
		}
		out = append(out, info)
	}
	return out
}

// lastUserMessageSnippet returns a truncated snippet of the last
// user-role message in history, or "" if there is none.
func lastUserMessageSnippet(history []HistoryEntry, maxRunes int) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "user" {
			continue
		}
		text := strings.ReplaceAll(history[i].Content, "\n", " ")
		text = strings.Join(strings.Fields(text), " ")
		if text == "" {
			return ""
		}
		r := []rune(text)
		if len(r) > maxRunes {
			return string(r[:maxRunes]) + "…"
		}
		return text
	}
	return ""
}

func (e *Engine) cmdList(p Platform, msg *Message, args []string) {
	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}

	if !supportsCards(p) {
		agentSessions, err := agent.ListSessions(e.ctx)
		if err != nil {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgListError), err))
			return
		}
		agentSessions = e.applySessionFilter(agentSessions, sessions)
		// Fallback: when the agent backend doesn't report sessions
		// (e.g. ACP servers like CodeBuddy that don't implement
		// session/list), surface sessions tracked by cc-connect's own
		// SessionManager so /list and /switch still work.
		if len(agentSessions) == 0 {
			agentSessions = sessionsFromSessionManager(sessions, agent.Name(), msg.SessionKey)
		}
		if len(agentSessions) == 0 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgListEmpty))
			return
		}

		total := len(agentSessions)
		totalPages := (total + listPageSize - 1) / listPageSize

		page := 1
		if len(args) > 0 {
			if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
				page = n
			}
		}
		if page > totalPages {
			page = totalPages
		}

		start := (page - 1) * listPageSize
		end := start + listPageSize
		if end > total {
			end = total
		}

		agentName := agent.Name()
		activeSession := sessions.GetOrCreateActive(msg.SessionKey)
		activeAgentID := activeSession.GetAgentSessionID()

		var sb strings.Builder
		if totalPages > 1 {
			sb.WriteString(fmt.Sprintf(e.i18n.T(MsgListTitlePaged), agentName, total, page, totalPages))
		} else {
			sb.WriteString(fmt.Sprintf(e.i18n.T(MsgListTitle), agentName, total))
		}
		for i := start; i < end; i++ {
			s := agentSessions[i]
			marker := "◻"
			if s.ID == activeAgentID {
				marker = "▶"
			}
			displayName := sessions.GetSessionName(s.ID)
			if displayName != "" {
				displayName = "📌 " + displayName
			} else {
				displayName = strings.ReplaceAll(s.Summary, "\n", " ")
				displayName = strings.Join(strings.Fields(displayName), " ")
				if displayName == "" {
					displayName = "(empty)"
				}
				if len([]rune(displayName)) > 40 {
					displayName = string([]rune(displayName)[:40]) + "…"
				}
			}
			sb.WriteString(fmt.Sprintf("%s **%d.** %s · **%d** msgs · %s\n",
				marker, i+1, displayName, s.MessageCount, s.ModifiedAt.Format("01-02 15:04")))
		}
		if totalPages > 1 {
			sb.WriteString(fmt.Sprintf(e.i18n.T(MsgListPageHint), page, totalPages))
		}
		sb.WriteString(e.i18n.T(MsgListSwitchHint))
		e.reply(p, msg.ReplyCtx, sb.String())
		return
	}

	page := 1
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			page = n
		}
	}
	card, err := e.renderListCard(msg.SessionKey, page)
	if err != nil {
		e.reply(p, msg.ReplyCtx, err.Error())
		return
	}
	e.replyWithCard(p, msg.ReplyCtx, card)
}

func (e *Engine) cmdSwitch(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, "Usage: /switch <number | id_prefix | name>")
		return
	}
	query := strings.TrimSpace(strings.Join(args, " "))

	slog.Info("cmdSwitch: listing agent sessions", "session_key", msg.SessionKey)
	agent, sessions, interactiveKey, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	agentSessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}
	agentSessions = e.applySessionFilter(agentSessions, sessions)
	// Fallback: when the agent backend doesn't report sessions
	// (e.g. ACP servers like CodeBuddy that don't implement
	// session/list), surface sessions tracked by cc-connect's own
	// SessionManager so /switch still works after restart.
	if len(agentSessions) == 0 {
		agentSessions = sessionsFromSessionManager(sessions, agent.Name(), msg.SessionKey)
	}

	matched := e.matchSession(agentSessions, sessions, query)
	if matched == nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSwitchNoMatch), query))
		return
	}

	slog.Info("cmdSwitch: cleaning up old session", "session_key", msg.SessionKey)
	e.cleanupInteractiveState(interactiveKey)
	slog.Info("cmdSwitch: cleanup done", "session_key", msg.SessionKey)

	session := sessions.SwitchToAgentSession(msg.SessionKey, matched.ID, agent.Name(), matched.Summary)
	session.ClearHistory()

	shortID := matched.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	displayName := sessions.GetSessionName(matched.ID)
	if displayName == "" {
		displayName = matched.Summary
	}
	e.reply(p, msg.ReplyCtx,
		e.i18n.Tf(MsgSwitchSuccess, displayName, shortID, matched.MessageCount))
}

// matchSession resolves a user query to an agent session. Priority:
//  1. Numeric index (1-based, matching /list output)
//  2. Exact custom name match (case-insensitive)
//  3. Session ID prefix match
//  4. Custom name prefix match (case-insensitive)
//  5. Summary substring match (case-insensitive)
func (e *Engine) matchSession(sessions []AgentSessionInfo, manager *SessionManager, query string) *AgentSessionInfo {
	if len(sessions) == 0 {
		return nil
	}

	// 1. Numeric index
	if idx, err := strconv.Atoi(query); err == nil && idx >= 1 && idx <= len(sessions) {
		return &sessions[idx-1]
	}

	queryLower := strings.ToLower(query)

	// 2. Exact custom name match
	for i := range sessions {
		name := manager.GetSessionName(sessions[i].ID)
		if name != "" && strings.ToLower(name) == queryLower {
			return &sessions[i]
		}
	}

	// 3. Session ID prefix match
	for i := range sessions {
		if strings.HasPrefix(sessions[i].ID, query) {
			return &sessions[i]
		}
	}

	// 4. Custom name prefix match
	for i := range sessions {
		name := manager.GetSessionName(sessions[i].ID)
		if name != "" && strings.HasPrefix(strings.ToLower(name), queryLower) {
			return &sessions[i]
		}
	}

	// 5. Summary substring match
	for i := range sessions {
		if sessions[i].Summary != "" && strings.Contains(strings.ToLower(sessions[i].Summary), queryLower) {
			return &sessions[i]
		}
	}

	return nil
}

func (e *Engine) commandWorkDir(agent Agent, msg *Message) string {
	if switcher, ok := agent.(WorkDirSwitcher); ok {
		if wd := strings.TrimSpace(switcher.GetWorkDir()); wd != "" {
			return normalizeWorkspacePath(wd)
		}
	}
	if e.multiWorkspace {
		channelKey := effectiveWorkspaceChannelKey(msg)
		if b, _, usable := e.lookupEffectiveWorkspaceBinding(channelKey); usable {
			return normalizeWorkspacePath(b.Workspace)
		}
	}
	if wd, ok := agent.(interface{ GetWorkDir() string }); ok {
		if dir := strings.TrimSpace(wd.GetWorkDir()); dir != "" {
			return normalizeWorkspacePath(dir)
		}
	}
	if wd, ok := e.agent.(interface{ GetWorkDir() string }); ok {
		if dir := strings.TrimSpace(wd.GetWorkDir()); dir != "" {
			return normalizeWorkspacePath(dir)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return normalizeWorkspacePath(cwd)
	}
	return ""
}

func (e *Engine) buildReplyFooter(agent Agent, session AgentSession, workspaceDir string, contextLeft string) string {
	if !e.replyFooterEnabled || agent == nil {
		return ""
	}

	var parts []string
	hasStatus := false
	contextLeft = strings.TrimSpace(contextLeft)
	contextFirst := strings.HasPrefix(contextLeft, "[ctx:")
	if contextFirst {
		parts = append(parts, contextLeft)
		hasStatus = true
	}
	if model := replyFooterModel(session, agent); model != "" {
		parts = append(parts, model)
		hasStatus = true
	}
	if effort := replyFooterReasoningEffort(session, agent); effort != "" {
		parts = append(parts, effort)
		hasStatus = true
	}
	if contextFirst {
		// Already added before model so "[ctx]" stays on the same footer line.
	} else if contextLeft != "" {
		parts = append(parts, contextLeft)
		hasStatus = true
	} else if usage := e.replyFooterUsageText(session, agent); usage != "" {
		parts = append(parts, usage)
		hasStatus = true
	}
	if dir := replyFooterWorkDir(session, agent, workspaceDir); dir != "" {
		parts = append(parts, dir)
	}
	if !hasStatus {
		return ""
	}
	return strings.Join(parts, " · ")
}

func replyFooterModel(session AgentSession, agent Agent) string {
	if session != nil {
		if getter, ok := session.(interface{ GetModel() string }); ok {
			if model := strings.TrimSpace(getter.GetModel()); model != "" {
				return model
			}
		}
	}
	if getter, ok := agent.(interface{ GetModel() string }); ok {
		return strings.TrimSpace(getter.GetModel())
	}
	return ""
}

func replyFooterReasoningEffort(session AgentSession, agent Agent) string {
	if session != nil {
		if getter, ok := session.(interface{ GetReasoningEffort() string }); ok {
			if effort := strings.TrimSpace(getter.GetReasoningEffort()); effort != "" {
				return effort
			}
		}
	}
	if getter, ok := agent.(interface{ GetReasoningEffort() string }); ok {
		return strings.TrimSpace(getter.GetReasoningEffort())
	}
	return ""
}

func (e *Engine) replyFooterUsageText(session AgentSession, agent Agent) string {
	ctx, cancel := context.WithTimeout(e.ctx, replyFooterUsageTimeout)
	defer cancel()

	if session != nil {
		if reporter, ok := session.(UsageReporter); ok {
			if report, err := reporter.GetUsage(ctx); err == nil {
				return formatReplyFooterUsage(report, e.i18n)
			}
		}
	}

	reporter, ok := agent.(UsageReporter)
	if !ok {
		return ""
	}

	e.replyFooterMu.Lock()
	cached := e.replyFooterUsage
	e.replyFooterMu.Unlock()
	if !cached.fetchedAt.IsZero() && time.Since(cached.fetchedAt) < replyFooterUsageCacheTTL {
		return cached.text
	}

	text := ""
	if report, err := reporter.GetUsage(ctx); err == nil {
		text = formatReplyFooterUsage(report, e.i18n)
	} else if !cached.fetchedAt.IsZero() {
		text = cached.text
	}

	e.replyFooterMu.Lock()
	e.replyFooterUsage = replyFooterUsageCache{text: text, fetchedAt: time.Now()}
	e.replyFooterMu.Unlock()
	return text
}

func formatReplyFooterUsage(report *UsageReport, i18n *I18n) string {
	if report == nil || i18n == nil {
		return ""
	}
	window, _ := selectUsageWindows(report)
	if window == nil {
		return ""
	}
	remaining := 100 - window.UsedPercent
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	return i18n.Tf(MsgReplyFooterRemaining, remaining)
}

func replyFooterSessionContextUsage(session AgentSession) *ContextUsage {
	if session == nil {
		return nil
	}
	reporter, ok := session.(ContextUsageReporter)
	if !ok {
		return nil
	}
	return reporter.GetContextUsage()
}

func replyFooterContextText(usage *ContextUsage, i18n *I18n) string {
	if usage == nil || i18n == nil {
		return ""
	}
	if usage.ContextWindow <= 0 {
		return ""
	}

	usedTokens := usage.UsedTokens
	if usedTokens <= 0 {
		switch {
		case usage.TotalTokens > 0:
			usedTokens = usage.TotalTokens
		case usage.InputTokens > 0 || usage.OutputTokens > 0:
			usedTokens = usage.InputTokens + usage.OutputTokens
		default:
			return ""
		}
	}

	baseline := usage.BaselineTokens
	if baseline < 0 {
		baseline = 0
	}
	if usage.ContextWindow <= baseline {
		return i18n.Tf(MsgReplyFooterRemaining, 0)
	}

	effectiveWindow := usage.ContextWindow - baseline
	effectiveUsed := usedTokens - baseline
	if effectiveUsed < 0 {
		effectiveUsed = 0
	}
	remaining := effectiveWindow - effectiveUsed
	if remaining < 0 {
		remaining = 0
	}

	left := int(math.Round(float64(remaining) / float64(effectiveWindow) * 100))
	if left < 0 {
		left = 0
	}
	if left > 100 {
		left = 100
	}
	return i18n.Tf(MsgReplyFooterRemaining, left)
}

func replyFooterWorkDir(session AgentSession, agent Agent, workspaceDir string) string {
	dir := strings.TrimSpace(workspaceDir)
	if dir == "" {
		if session != nil {
			if wd, ok := session.(interface{ GetWorkDir() string }); ok {
				dir = strings.TrimSpace(wd.GetWorkDir())
			}
		}
	}
	if dir == "" {
		if switcher, ok := agent.(WorkDirSwitcher); ok {
			dir = strings.TrimSpace(switcher.GetWorkDir())
		}
	}
	if dir == "" {
		if wd, ok := agent.(interface{ GetWorkDir() string }); ok {
			dir = strings.TrimSpace(wd.GetWorkDir())
		}
	}
	if dir == "" {
		return ""
	}
	return compactReplyFooterPath(dir)
}

func compactReplyFooterPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// Keep a cleaned (but not symlink-resolved) copy for the fallback check
	// below. normalizeWorkspacePath calls filepath.EvalSymlinks which fails on
	// paths that do not yet exist on disk; in that case the home prefix check
	// against a resolved home directory would also fail on macOS where $TMPDIR
	// is under /var/folders (a symlink to /private/var/folders).
	rawPath := filepath.Clean(path)
	path = normalizeWorkspacePath(path)
	if home, err := os.UserHomeDir(); err == nil {
		rawHome := filepath.Clean(home)
		home = normalizeWorkspacePath(home)
		if path == home || rawPath == rawHome {
			return "~"
		}
		prefix := home + string(os.PathSeparator)
		if strings.HasPrefix(path, prefix) {
			return "~" + filepath.ToSlash(strings.TrimPrefix(path, home))
		}
		// Fallback: check against unresolved home (handles non-existent dirs
		// and platforms/tests where the path was not yet symlink-resolved).
		rawPrefix := rawHome + string(os.PathSeparator)
		if strings.HasPrefix(rawPath, rawPrefix) {
			return "~" + filepath.ToSlash(strings.TrimPrefix(rawPath, rawHome))
		}
	}

	slash := filepath.ToSlash(path)
	if filepath.IsAbs(path) {
		trimmed := strings.Trim(slash, "/")
		if trimmed == "" {
			return "/"
		}
		parts := strings.Split(trimmed, "/")
		if len(parts) == 1 {
			return parts[0]
		}
		start := len(parts) - 2
		if start < 0 {
			start = 0
		}
		return "…/" + strings.Join(parts[start:], "/")
	}
	return slash
}

func appendReplyFooter(content, footer string) string {
	if footer == "" {
		return content
	}
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return "*" + footer + "*"
	}
	return content + "\n\n*" + footer + "*"
}

func appendFinalMetadataToSegment(segment, fullResponse string) string {
	segment = strings.TrimRight(segment, "\n ")
	if segment == "" {
		return fullResponse
	}
	fullResponse = strings.TrimSpace(fullResponse)
	if fullResponse == "" || strings.TrimSpace(segment) == fullResponse {
		return segment
	}

	metadata := ""
	if idx := strings.LastIndex(fullResponse, "\n\n*"); idx >= 0 && strings.HasSuffix(fullResponse, "*") {
		metadata = fullResponse[idx:]
	} else if match := ctxSelfReportRe.FindString(fullResponse); match != "" {
		metadata = "\n" + strings.TrimSpace(match)
	}
	if metadata == "" || strings.Contains(segment, strings.TrimSpace(metadata)) {
		return segment
	}
	return segment + metadata
}

func mergeStreamDisplayContent(streamContent, lastAssistantSegment, finalResponse string) string {
	streamContent = strings.TrimRight(streamContent, "\n ")
	finalResponse = strings.TrimSpace(finalResponse)
	if streamContent == "" {
		return finalResponse
	}
	if finalResponse == "" {
		return streamContent
	}

	// Dedup 1: when finalResponse starts with the streamed content,
	// the stream was just a prefix (e.g. metadata appended after the
	// answer, like "model · usage · path"). Return finalResponse as-is.
	if strings.HasPrefix(finalResponse, streamContent) {
		return finalResponse
	}
	// Dedup 2: when streamContent starts with finalResponse, the
	// stream has already been delivered in full; no need to append.
	if strings.HasPrefix(streamContent, finalResponse) {
		return streamContent
	}
	// Dedup 3: remove the last assistant segment from streamContent
	// if it appears as a suffix, so we don't duplicate the final
	// chunk that the assistant already echoed.
	lastAssistantSegment = strings.TrimSpace(lastAssistantSegment)
	if lastAssistantSegment != "" {
		trimmedStream := strings.TrimSpace(streamContent)
		if strings.HasSuffix(trimmedStream, lastAssistantSegment) {
			suffixIdx := strings.LastIndex(streamContent, lastAssistantSegment)
			if suffixIdx >= 0 {
				prefix := strings.TrimRight(streamContent[:suffixIdx], "\n ")
				if prefix == "" {
					return finalResponse
				}
				return prefix + "\n\n" + finalResponse
			}
		}
	}

	// Dedup 4: exact match after trim
	if strings.TrimSpace(streamContent) == finalResponse {
		return streamContent
	}
	return streamContent + "\n\n" + finalResponse
}
