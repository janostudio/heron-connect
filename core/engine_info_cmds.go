package core

// engine_info_cmds.go — information/status query commands and help system.
//
// Covers:
//   - cmdSearch, cmdName, cmdCurrent, cmdStatus, cmdUsage
//   - formatUsageReport, formatUsageBlocks, formatUsageBlock
//   - accountDisplay, selectUsageWindows
//   - cmdHistory, cmdLang, langDisplayName
//   - cmdHelp, cmdStart, helpCardGroups, renderHelpCard
//   - splitHelpTabRows, renderHelpGroupCard
//   - GetAllCommands, menuCommandsForPlatform, Telegram menu helpers
//
// All methods remain func (e *Engine) receivers in package core.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)
func (e *Engine) cmdSearch(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgSearchUsage))
		return
	}

	keyword := strings.ToLower(strings.Join(args, " "))

	// Get all agent sessions
	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	agentSessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSearchError), err))
		return
	}
	agentSessions = e.applySessionFilter(agentSessions, sessions)

	type searchResult struct {
		id           string
		name         string
		summary      string
		matchType    string // "name" or "message"
		messageCount int
	}

	var results []searchResult

	for _, s := range agentSessions {
		// Check session name (custom name or summary)
		customName := sessions.GetSessionName(s.ID)
		displayName := customName
		if displayName == "" {
			displayName = s.Summary
		}

		// Match by name/summary
		if strings.Contains(strings.ToLower(displayName), keyword) {
			results = append(results, searchResult{
				id:           s.ID,
				name:         displayName,
				summary:      s.Summary,
				matchType:    "name",
				messageCount: s.MessageCount,
			})
			continue
		}

		// Match by session ID prefix
		if strings.HasPrefix(strings.ToLower(s.ID), keyword) {
			results = append(results, searchResult{
				id:           s.ID,
				name:         displayName,
				summary:      s.Summary,
				matchType:    "id",
				messageCount: s.MessageCount,
			})
			continue
		}
	}

	if len(results) == 0 {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSearchNoResult), keyword))
		return
	}

	// Build result message
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgSearchResult), len(results), keyword))

	for i, r := range results {
		shortID := r.id
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		sb.WriteString(fmt.Sprintf("\n%d. [%s] %s", i+1, shortID, r.name))
	}

	sb.WriteString("\n\n" + e.i18n.T(MsgSearchHint))

	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdName(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNameUsage))
		return
	}

	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}

	// Check if first arg is a number → naming a specific session by list index
	var targetID string
	var name string

	if idx, err := strconv.Atoi(args[0]); err == nil && idx >= 1 {
		// /name <number> <name...>
		if len(args) < 2 {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNameUsage))
			return
		}
		agentSessions, err := agent.ListSessions(e.ctx)
		if err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
			return
		}
		agentSessions = e.applySessionFilter(agentSessions, sessions)
		if idx > len(agentSessions) {
			e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgSwitchNoSession), idx))
			return
		}
		targetID = agentSessions[idx-1].ID
		name = strings.Join(args[1:], " ")
	} else {
		// /name <name...> → current session
		session := sessions.GetOrCreateActive(msg.SessionKey)
		targetID = session.GetAgentSessionID()
		if targetID == "" {
			e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNameNoSession))
			return
		}
		name = strings.Join(args, " ")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgNameUsage))
		return
	}

	sessions.SetSessionName(targetID, name)

	shortID := targetID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgNameSet), name, shortID))
}

func (e *Engine) cmdCurrent(p Platform, msg *Message) {
	if !supportsCards(p) {
		_, sessions, _, err := e.commandContext(p, msg)
		if err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
			return
		}
		s := sessions.GetOrCreateActive(msg.SessionKey)
		agentID := s.GetAgentSessionID()
		if agentID == "" {
			agentID = e.i18n.T(MsgSessionNotStarted)
		}
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCurrentSession), s.Name, agentID, len(s.History)))
		return
	}

	e.replyWithCard(p, msg.ReplyCtx, e.renderCurrentCard(msg.SessionKey))
}

func (e *Engine) cmdStatus(p Platform, msg *Message) {
	if !supportsCards(p) {
		agent, sessions, _, err := e.commandContext(p, msg)
		if err != nil {
			e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
			return
		}
		platNames := make([]string, len(e.platforms))
		for i, pl := range e.platforms {
			platNames[i] = pl.Name()
		}
		platformStr := strings.Join(platNames, ", ")
		if len(platNames) == 0 {
			platformStr = "-"
		}

		workDirStr := e.commandWorkDir(agent, msg)

		uptimeStr := formatDurationI18n(time.Since(e.startedAt), e.i18n.CurrentLang())

		cur := e.i18n.CurrentLang()
		langStr := fmt.Sprintf("%s (%s)", string(cur), langDisplayName(cur))

		var modeStr string
		if ms, ok := agent.(ModeSwitcher); ok {
			mode := ms.GetMode()
			if mode != "" {
				modeStr = e.i18n.Tf(MsgStatusMode, mode)
			}
		}
		thinkingStr := e.i18n.T(MsgDisabledShort)
		if e.display.ThinkingMessages {
			thinkingStr = e.i18n.T(MsgEnabledShort)
		}
		toolStr := e.i18n.T(MsgDisabledShort)
		if e.display.ToolMessages {
			toolStr = e.i18n.T(MsgEnabledShort)
		}
		modeStr += e.i18n.Tf(MsgStatusThinkingMessages, thinkingStr)
		modeStr += e.i18n.Tf(MsgStatusToolMessages, toolStr)

		s := sessions.GetOrCreateActive(msg.SessionKey)
		sessionDisplayName := sessions.GetSessionName(s.GetAgentSessionID())
		if sessionDisplayName == "" {
			sessionDisplayName = s.Name
		}
		sessionStr := e.i18n.Tf(MsgStatusSession, sessionDisplayName, len(s.History))

		var cronStr string
		if e.cronScheduler != nil {
			if jobs := e.cronScheduler.Store().ListBySessionKey(msg.SessionKey); len(jobs) > 0 {
				enabledCount := 0
				for _, j := range jobs {
					if j.Enabled {
						enabledCount++
					}
				}
				cronStr = e.i18n.Tf(MsgStatusCron, len(jobs), enabledCount)
			}
		}

		sessionKeyStr := e.i18n.Tf(MsgStatusSessionKey, msg.SessionKey)

		agentSIDStr := ""
		if agentSID := s.GetAgentSessionID(); agentSID != "" {
			agentSIDStr = e.i18n.Tf(MsgStatusAgentSID, agentSID)
		}

		userIDStr := ""
		if msg.UserID != "" {
			userIDStr = e.i18n.Tf(MsgStatusUserID, msg.UserID)
		}

		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgStatusTitle,
			e.name,
			agent.Name(),
			workDirStr,
			platformStr,
			uptimeStr,
			langStr,
			modeStr,
			sessionStr,
			cronStr,
			sessionKeyStr,
			agentSIDStr,
			userIDStr,
		))
		return
	}

	e.replyWithCard(p, msg.ReplyCtx, e.renderStatusCard(msg.SessionKey, msg.UserID))
}

func (e *Engine) cmdUsage(p Platform, msg *Message) {
	reporter, ok := e.agent.(UsageReporter)
	if !ok {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgUsageNotSupported))
		return
	}

	fetchCtx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()

	report, err := reporter.GetUsage(fetchCtx)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgUsageFetchFailed, err))
		return
	}

	if supportsCards(p) {
		e.replyWithCard(p, msg.ReplyCtx, e.renderUsageCard(report))
		return
	}

	e.reply(p, msg.ReplyCtx, formatUsageReport(report, e.i18n.CurrentLang()))
}

func formatUsageReport(report *UsageReport, lang Language) string {
	if report == nil {
		return usageUnavailableText(lang)
	}

	var sb strings.Builder
	sb.WriteString(usageAccountLabel(lang))
	sb.WriteString(accountDisplay(report))
	sb.WriteString(formatUsageBlocks(report, lang))

	return strings.TrimSpace(sb.String())
}

func formatUsageBlocks(report *UsageReport, lang Language) string {
	primary, secondary := selectUsageWindows(report)
	var sections []string
	if primary != nil {
		sections = append(sections, formatUsageBlock(lang, primary))
	}
	if secondary != nil {
		sections = append(sections, formatUsageBlock(lang, secondary))
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

func accountDisplay(report *UsageReport) string {
	var base string
	if report.Email != "" {
		base = report.Email
	} else if report.AccountID != "" {
		base = report.AccountID
	} else if report.UserID != "" {
		base = report.UserID
	} else {
		base = "-"
	}
	if report.Plan != "" {
		return fmt.Sprintf("%s (%s)", base, report.Plan)
	}
	return base
}

func selectUsageWindows(report *UsageReport) (*UsageWindow, *UsageWindow) {
	for _, bucket := range report.Buckets {
		if len(bucket.Windows) == 0 {
			continue
		}
		var primary, secondary *UsageWindow
		for i := range bucket.Windows {
			window := &bucket.Windows[i]
			switch window.WindowSeconds {
			case 18000:
				primary = window
			case 604800:
				if secondary == nil {
					secondary = window
				}
			}
		}
		if primary == nil && len(bucket.Windows) > 0 {
			primary = &bucket.Windows[0]
		}
		if secondary == nil && len(bucket.Windows) > 1 {
			secondary = &bucket.Windows[1]
		}
		if primary != nil || secondary != nil {
			return primary, secondary
		}
	}
	return nil, nil
}

func formatUsageBlock(lang Language, window *UsageWindow) string {
	remaining := 100 - window.UsedPercent
	if remaining < 0 {
		remaining = 0
	}
	var sb strings.Builder
	sb.WriteString(usageWindowLabel(lang, window.WindowSeconds))
	sb.WriteString("\n")
	sb.WriteString(usageRemainingLabel(lang))
	sb.WriteString(usageColon(lang))
	sb.WriteString(fmt.Sprintf("%d%%", remaining))
	sb.WriteString("\n")
	sb.WriteString(usageResetLabel(lang))
	sb.WriteString(usageColon(lang))
	sb.WriteString(formatUsageResetTime(lang, window.ResetAfterSeconds))
	return sb.String()
}

func (e *Engine) cmdHistory(p Platform, msg *Message, args []string) {
	if len(args) == 0 && supportsCards(p) {
		e.replyWithCard(p, msg.ReplyCtx, e.renderHistoryCard(msg.SessionKey))
		return
	}
	if len(args) == 0 {
		args = []string{"10"}
	}

	agent, sessions, _, err := e.commandContext(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	s := sessions.GetOrCreateActive(msg.SessionKey)
	n := 10
	if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
		n = v
	}

	entries := s.GetHistory(n)
	agentSID := s.GetAgentSessionID()
	if len(entries) == 0 && agentSID != "" {
		if hp, ok := agent.(HistoryProvider); ok {
			if agentEntries, err := hp.GetSessionHistory(e.ctx, agentSID, n); err == nil {
				entries = agentEntries
			}
		}
	}

	if len(entries) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHistoryEmpty))
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📜 History (last %d):\n\n", len(entries)))
	for _, h := range entries {
		icon := "👤"
		if h.Role == "assistant" {
			icon = "🤖"
		}
		content := h.Content
		if len([]rune(content)) > 200 {
			content = string([]rune(content)[:200]) + "..."
		}
		sb.WriteString(fmt.Sprintf("%s [%s]\n%s\n\n", icon, h.Timestamp.Format("15:04:05"), content))
	}
	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdLang(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		cur := e.i18n.CurrentLang()
		name := langDisplayName(cur)
		text := e.i18n.Tf(MsgLangCurrent, name)
		buttons := [][]ButtonOption{
			{
				{Text: "English", Data: "cmd:/lang en"},
				{Text: "中文", Data: "cmd:/lang zh"},
				{Text: "繁體中文", Data: "cmd:/lang zh-TW"},
			},
			{
				{Text: "日本語", Data: "cmd:/lang ja"},
				{Text: "Español", Data: "cmd:/lang es"},
				{Text: "Auto", Data: "cmd:/lang auto"},
			},
		}
		if supportsCards(p) {
			e.replyWithCard(p, msg.ReplyCtx, e.renderLangCard())
			return
		}
		if _, ok := p.(InlineButtonSender); ok {
			e.replyWithButtons(p, msg.ReplyCtx, text, buttons)
			return
		}
		var sb strings.Builder
		sb.WriteString(text)
		sb.WriteString("\n\n")
		sb.WriteString("- English: `/lang en`\n")
		sb.WriteString("- 中文: `/lang zh`\n")
		sb.WriteString("- 繁體中文: `/lang zh-TW`\n")
		sb.WriteString("- 日本語: `/lang ja`\n")
		sb.WriteString("- Español: `/lang es`\n")
		sb.WriteString("- Auto: `/lang auto`")
		e.reply(p, msg.ReplyCtx, sb.String())
		return
	}

	target := strings.ToLower(strings.TrimSpace(args[0]))
	var lang Language
	switch target {
	case "en", "english":
		lang = LangEnglish
	case "zh", "cn", "chinese", "中文":
		lang = LangChinese
	case "zh-tw", "zh_tw", "zhtw", "繁體", "繁体":
		lang = LangTraditionalChinese
	case "ja", "jp", "japanese", "日本語":
		lang = LangJapanese
	case "es", "spanish", "español":
		lang = LangSpanish
	case "auto":
		lang = LangAuto
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgLangInvalid))
		return
	}

	e.i18n.SetLang(lang)
	name := langDisplayName(lang)
	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgLangChanged, name))
}

func langDisplayName(lang Language) string {
	switch lang {
	case LangEnglish:
		return "English"
	case LangChinese:
		return "中文"
	case LangTraditionalChinese:
		return "繁體中文"
	case LangJapanese:
		return "日本語"
	case LangSpanish:
		return "Español"
	default:
		return "Auto"
	}
}

func (e *Engine) cmdHelp(p Platform, msg *Message) {
	if !supportsCards(p) {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgHelp))
		return
	}
	e.replyWithCard(p, msg.ReplyCtx, e.renderHelpCard())
}

// cmdStart handles the `/start` slash command.
//
// On Telegram, `/start` is a protocol convention sent by the client when a
// user first opens a bot (or taps the Start button). Without a native
// handler, the message previously fell through to the default branch and
// got forwarded verbatim to the agent — and Claude Code's CLI interprets a
// leading "/" as a slash-command request, replying "Unknown command:
// /start. Did you mean /stats?" instead of greeting the user.
//
// Replying with a localized welcome that names the project keeps the
// behavior consistent with every other Telegram bot framework, and is a
// no-op improvement on platforms where /start has no special meaning.
func (e *Engine) cmdStart(p Platform, msg *Message) {
	name := e.name
	if name == "" {
		name = e.agent.Name()
	}
	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgWelcome), name))
}

const defaultHelpGroup = "session"

type helpCardItem struct {
	command string
	action  string
}

type helpCardGroup struct {
	key      string
	titleKey MsgKey
	items    []helpCardItem
}

func helpCardGroups() []helpCardGroup {
	return []helpCardGroup{
		{
			key:      "session",
			titleKey: MsgHelpSessionSection,
			items: []helpCardItem{
				{command: "/new", action: "act:/new"},
				{command: "/list", action: "nav:/list"},
				{command: "/current", action: "nav:/current"},
				{command: "/switch", action: "nav:/list"},
				{command: "/search", action: "cmd:/search"},
				{command: "/history", action: "nav:/history"},
				{command: "/delete", action: "cmd:/delete"},
				{command: "/name", action: "cmd:/name"},
			},
		},
		{
			key:      "agent",
			titleKey: MsgHelpAgentSection,
			items: []helpCardItem{
				{command: "/model", action: "nav:/model"},
				{command: "/reasoning", action: "nav:/reasoning"},
				{command: "/mode", action: "nav:/mode"},
				{command: "/lang", action: "nav:/lang"},
				{command: "/provider", action: "nav:/provider"},
				{command: "/memory", action: "cmd:/memory"},
				{command: "/allow", action: "cmd:/allow"},
				{command: "/quiet", action: "cmd:/quiet"},
				{command: "/tts", action: "cmd:/tts"},
			},
		},
		{
			key:      "tools",
			titleKey: MsgHelpToolsSection,
			items: []helpCardItem{
				{command: "/shell", action: "cmd:/shell"},
				{command: "/show", action: "cmd:/show"},
				{command: "/cron", action: "nav:/cron"},
				{command: "/heartbeat", action: "nav:/heartbeat"},
				{command: "/commands", action: "nav:/commands"},
				{command: "/alias", action: "nav:/alias"},
				{command: "/skills", action: "nav:/skills"},
				{command: "/compress", action: "cmd:/compress"},
				{command: "/cancel", action: "act:/cancel"},
				{command: "/stop", action: "act:/stop"},
				{command: "/ps", action: "cmd:/ps"},
			},
		},
		{
			key:      "system",
			titleKey: MsgHelpSystemSection,
			items: []helpCardItem{
				{command: "/status", action: "nav:/status"},
				{command: "/doctor", action: "nav:/doctor"},
				{command: "/usage", action: "cmd:/usage"},
				{command: "/config", action: "nav:/config"},
				{command: "/bind", action: "cmd:/bind"},
				{command: "/workspace", action: "cmd:/workspace"},
				{command: "/dir", action: "nav:/dir"},
				{command: "/web", action: "cmd:/web"},
				{command: "/version", action: "nav:/version"},
				{command: "/upgrade", action: "nav:/upgrade"},
				{command: "/restart", action: "cmd:/restart"},
			},
		},
	}
}

func (e *Engine) renderHelpCard() *Card {
	return e.renderHelpGroupCard(defaultHelpGroup)
}

// splitHelpTabRows splits tab buttons into rows. Card-based platforms
// get 2 buttons per row for better layout; others get all in one row.
func splitHelpTabRows(useMultiRow bool, tabs []CardButton) [][]CardButton {
	if useMultiRow {
		rows := make([][]CardButton, 0, (len(tabs)+1)/2)
		for i := 0; i < len(tabs); i += 2 {
			end := i + 2
			if end > len(tabs) {
				end = len(tabs)
			}
			rows = append(rows, tabs[i:end])
		}
		return rows
	}
	return [][]CardButton{tabs}
}

func (e *Engine) renderHelpGroupCard(groupKey string) *Card {
	sectionTitle := func(key MsgKey) string {
		section := e.i18n.T(key)
		if idx := strings.IndexByte(section, '\n'); idx >= 0 {
			return section[:idx]
		}
		return section
	}
	tabLabel := func(key MsgKey) string {
		return strings.Trim(sectionTitle(key), "* ")
	}
	commandText := func(command string) string {
		return "**" + command + "**  " + e.i18n.T(MsgKey(strings.TrimPrefix(command, "/")))
	}

	groups := helpCardGroups()
	current := groups[0]
	normalizedGroup := strings.ToLower(strings.TrimSpace(groupKey))
	for _, group := range groups {
		if group.key == normalizedGroup {
			current = group
			break
		}
	}

	cb := NewCard().Title(e.i18n.T(MsgHelpTitle), "blue")
	var tabs []CardButton
	for _, group := range groups {
		btnType := "default"
		if group.key == current.key {
			btnType = "primary"
		}
		tabs = append(tabs, Btn(tabLabel(group.titleKey), btnType, "nav:/help "+group.key))
	}
	for _, row := range splitHelpTabRows(true, tabs) {
		cb.ButtonsEqual(row...)
	}
	for _, item := range current.items {
		cb.ListItem(commandText(item.command), "▶", item.action)
	}
	cb.Note(e.i18n.T(MsgHelpTip))
	return cb.Build()
}

// GetAllCommands returns all available commands for bot menu registration.
// It includes built-in commands (with localized descriptions) and custom commands.
func (e *Engine) GetAllCommands() []BotCommandInfo {
	var commands []BotCommandInfo

	e.userRolesMu.RLock()
	disabledCmds := e.disabledCmds
	e.userRolesMu.RUnlock()

	// Collect built-in  commands (use primary name, first in names list)
	seenCmds := make(map[string]bool)
	for _, c := range builtinCommands {
		if len(c.names) == 0 {
			continue
		}
		// Use id as primary
		primaryName := c.id
		if seenCmds[primaryName] {
			continue
		}
		seenCmds[primaryName] = true

		// Skip disabled commands
		if disabledCmds[c.id] {
			continue
		}

		commands = append(commands, BotCommandInfo{
			Command:     primaryName,
			Description: e.i18n.T(MsgKey(primaryName)),
		})
	}

	// Collect custom commands from CommandRegistry
	for _, c := range e.commands.ListAll() {
		if seenCmds[strings.ToLower(c.Name)] {
			continue
		}
		seenCmds[strings.ToLower(c.Name)] = true

		desc := c.Description
		if desc == "" {
			desc = "Custom command"
		}

		commands = append(commands, BotCommandInfo{
			Command:     c.Name,
			Description: desc,
		})
	}

	// Collect skills
	for _, s := range e.skills.ListAll() {
		lowerName := strings.ToLower(s.Name)
		if seenCmds[lowerName] {
			continue
		}
		if disabledCmds[lowerName] {
			continue
		}
		seenCmds[lowerName] = true

		desc := s.Description
		if desc == "" {
			desc = "Skill"
		}

		commands = append(commands, BotCommandInfo{
			Command:     s.Name,
			Description: desc,
			IsSkill:     true,
		})
	}

	return commands
}

func (e *Engine) menuCommandsForPlatform(platformName string) ([]BotCommandInfo, bool) {
	commands := e.GetAllCommands()
	if !strings.EqualFold(platformName, "telegram") {
		return commands, false
	}
	return telegramMenuCommandsAllOrNone(commands)
}

func telegramMenuCommandsAllOrNone(commands []BotCommandInfo) ([]BotCommandInfo, bool) {
	var nonSkill []BotCommandInfo
	var skill []BotCommandInfo
	for _, command := range commands {
		if command.IsSkill {
			skill = append(skill, command)
			continue
		}
		nonSkill = append(nonSkill, command)
	}

	if len(telegramMenuEntryNames(append(append([]BotCommandInfo{}, nonSkill...), skill...))) <= telegramBotCommandLimit {
		return commands, false
	}
	return nonSkill, len(skill) > 0
}

func telegramMenuEntryNames(commands []BotCommandInfo) []string {
	var names []string
	seen := make(map[string]bool)
	for _, command := range commands {
		name := sanitizeTelegramMenuCommand(command.Command)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func sanitizeTelegramMenuCommand(cmd string) string {
	cmd = strings.ToLower(cmd)
	var b strings.Builder
	for _, c := range cmd {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	result := b.String()
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	result = strings.Trim(result, "_")
	if len(result) == 0 || result[0] < 'a' || result[0] > 'z' {
		return ""
	}
	if len(result) > 32 {
		result = result[:32]
	}
	return result
}

