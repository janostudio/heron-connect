package core

// engine_cards.go — render* methods for navigable sub-cards, card utilities,
// and card button/layout helpers.
//
// Covers:
//   - renderLang/Model/Mode/Cron/Alias/Config/Skills/Doctor/Version/Upgrade cards
//   - renderUsageCard + i18n label helpers (usageAccountLabel, etc.)
//   - renderStatusCard, renderListCardSafe, renderDirCardSafe
//   - cardBackButton, simpleCard, splitCardTitleBody
//   - cronTimeFormat, formatDurationI18n
//
// Methods here are still func (e *Engine) receivers and live in the
// same package as engine.go, so they can access all Engine fields
// and helpers. The split is purely for readability.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"
)

func (e *Engine) renderLangCard() *Card {
	cur := e.i18n.CurrentLang()
	name := langDisplayName(cur)

	langs := []struct{ code, label string }{
		{"en", "English"}, {"zh", "中文"}, {"zh-TW", "繁體中文"},
		{"ja", "日本語"}, {"es", "Español"}, {"auto", "Auto"},
	}
	var opts []CardSelectOption
	initVal := ""
	for _, l := range langs {
		opts = append(opts, CardSelectOption{Text: l.label, Value: "act:/lang " + l.code})
		if string(cur) == l.code || (cur == LangAuto && l.code == "auto") {
			initVal = "act:/lang " + l.code
		}
	}

	return NewCard().
		Title(e.i18n.T(MsgCardTitleLanguage), "wathet").
		Markdown(e.i18n.Tf(MsgLangCurrent, name)).
		Select(e.i18n.T(MsgLangSelectPlaceholder), opts, initVal).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderModelCard(sessionKey string) *Card {
	if ms := e.getModelSwitchState(sessionKey); ms != nil && ms.phase == "switching" {
		return e.renderModelSwitchingCard(ms.target)
	}

	agent := e.agent
	if sessionKey != "" {
		agent, _ = e.sessionContextForKey(sessionKey)
	}

	switcher, ok := agent.(ModelSwitcher)
	if !ok {
		return e.simpleCard(e.i18n.T(MsgCardTitleModel), "indigo", e.i18n.T(MsgModelNotSupported))
	}

	fetchCtx, cancel := context.WithTimeout(e.ctx, 3*time.Second)
	defer cancel()
	models := switcher.AvailableModels(fetchCtx)
	current := switcher.GetModel()

	var sb strings.Builder
	if current == "" {
		sb.WriteString(e.i18n.T(MsgModelDefault))
	} else {
		sb.WriteString(e.i18n.Tf(MsgModelCurrent, current))
	}

	var opts []CardSelectOption
	initVal := ""
	for i, m := range models {
		label := m.Name
		if m.Alias != "" {
			label = m.Alias + " - " + m.Name
		} else if m.Desc != "" {
			label += " — " + m.Desc
		}
		val := fmt.Sprintf("act:/model switch %d", i+1)
		opts = append(opts, CardSelectOption{Text: label, Value: val})
		if m.Name == current {
			initVal = val
		}
	}

	cb := NewCard().Title(e.i18n.T(MsgCardTitleModel), "indigo").
		Markdown(sb.String()).
		Select(e.i18n.T(MsgModelSelectPlaceholder), opts, initVal).
		Buttons(e.cardBackButton())
	cb.Note(e.i18n.T(MsgModelUsage))
	return cb.Build()
}

func (e *Engine) renderModelSwitchingCard(target string) *Card {
	return NewCard().
		Title(e.i18n.T(MsgCardTitleModel), "orange").
		Markdown(e.i18n.Tf(MsgModelCardSwitching, target)).
		Build()
}

func (e *Engine) renderModelSwitchResultCard(target string, err error) *Card {
	if err != nil {
		return NewCard().
			Title(e.i18n.T(MsgCardTitleModel), "red").
			Markdown(e.i18n.Tf(MsgModelCardSwitchFailed, err)).
			Buttons(e.modelCardBackButton()).
			Build()
	}
	return NewCard().
		Title(e.i18n.T(MsgCardTitleModel), "green").
		Markdown(e.i18n.Tf(MsgModelCardSwitched, target)).
		Buttons(e.modelCardBackButton()).
		Build()
}

func (e *Engine) renderReasoningCard() *Card {
	switcher, ok := e.agent.(ReasoningEffortSwitcher)
	if !ok {
		return e.simpleCard(e.i18n.T(MsgCardTitleReasoning), "orange", e.i18n.T(MsgReasoningNotSupported))
	}

	efforts := switcher.AvailableReasoningEfforts()
	current := switcher.GetReasoningEffort()

	var sb strings.Builder
	if current == "" {
		sb.WriteString(e.i18n.T(MsgReasoningDefault))
	} else {
		sb.WriteString(e.i18n.Tf(MsgReasoningCurrent, current))
	}

	var opts []CardSelectOption
	initVal := ""
	for i, effort := range efforts {
		val := fmt.Sprintf("act:/reasoning %d", i+1)
		opts = append(opts, CardSelectOption{Text: effort, Value: val})
		if effort == current {
			initVal = val
		}
	}

	cb := NewCard().Title(e.i18n.T(MsgCardTitleReasoning), "orange").
		Markdown(sb.String()).
		Select(e.i18n.T(MsgReasoningSelectPlaceholder), opts, initVal).
		Buttons(e.cardBackButton())
	cb.Note(e.i18n.T(MsgReasoningUsage))
	return cb.Build()
}

func (e *Engine) renderModeCard() *Card {
	switcher, ok := e.agent.(ModeSwitcher)
	if !ok {
		return e.simpleCard(e.i18n.T(MsgCardTitleMode), "violet", e.i18n.T(MsgModeNotSupported))
	}

	current := switcher.GetMode()
	modes := switcher.PermissionModes()
	zhLike := e.i18n.IsZhLike()

	var sb strings.Builder
	for _, m := range modes {
		marker := "◻"
		if m.Key == current {
			marker = "▶"
		}
		if zhLike {
			sb.WriteString(fmt.Sprintf("%s **%s** — %s\n", marker, m.NameZh, m.DescZh))
		} else {
			sb.WriteString(fmt.Sprintf("%s **%s** — %s\n", marker, m.Name, m.Desc))
		}
	}

	var opts []CardSelectOption
	initVal := ""
	for _, m := range modes {
		label := m.Name
		if zhLike {
			label = m.NameZh
		}
		val := "act:/mode " + m.Key
		opts = append(opts, CardSelectOption{Text: label, Value: val})
		if m.Key == current {
			initVal = val
		}
	}

	cb := NewCard().Title(e.i18n.T(MsgCardTitleMode), "violet").
		Markdown(sb.String()).
		Select(e.i18n.T(MsgModeSelectPlaceholder), opts, initVal).
		Buttons(e.cardBackButton())
	cb.Note(e.modeUsageText(modes))
	return cb.Build()
}

func (e *Engine) renderListCard(sessionKey string, page int) (*Card, error) {
	agent, sessions := e.sessionContextForKey(sessionKey)
	agentSessions, err := agent.ListSessions(e.ctx)
	if err != nil {
		return nil, fmt.Errorf(e.i18n.T(MsgListError), err)
	}
	agentSessions = e.applySessionFilter(agentSessions, sessions)
	if len(agentSessions) == 0 {
		return e.simpleCard(e.i18n.Tf(MsgCardTitleSessions, agent.Name(), 0), "turquoise", e.i18n.T(MsgListEmpty)), nil
	}

	total := len(agentSessions)
	totalPages := (total + listPageSize - 1) / listPageSize
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * listPageSize
	end := start + listPageSize
	if end > total {
		end = total
	}

	agentName := agent.Name()
	activeSession := sessions.GetOrCreateActive(sessionKey)
	activeAgentID := activeSession.GetAgentSessionID()

	var titleStr string
	if totalPages > 1 {
		titleStr = e.i18n.Tf(MsgCardTitleSessionsPaged, agentName, total, page, totalPages)
	} else {
		titleStr = e.i18n.Tf(MsgCardTitleSessions, agentName, total)
	}

	cb := NewCard().Title(titleStr, "turquoise")
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
				displayName = e.i18n.T(MsgListEmptySummary)
			}
			if len([]rune(displayName)) > 40 {
				displayName = string([]rune(displayName)[:40]) + "…"
			}
		}
		btnType := "default"
		if s.ID == activeAgentID {
			btnType = "primary"
		}
		cb.ListItemBtn(
			e.i18n.Tf(MsgListItem, marker, i+1, displayName, s.MessageCount, s.ModifiedAt.Format("01-02 15:04")),
			fmt.Sprintf("#%d", i+1),
			btnType,
			fmt.Sprintf("act:/switch %d", i+1),
		)
	}

	var navBtns []CardButton
	if page > 1 {
		navBtns = append(navBtns, e.cardPrevButton(fmt.Sprintf("nav:/list %d", page-1)))
	}
	navBtns = append(navBtns, e.cardBackButton())
	if page < totalPages {
		navBtns = append(navBtns, e.cardNextButton(fmt.Sprintf("nav:/list %d", page+1)))
	}
	cb.Buttons(navBtns...)

	if totalPages > 1 {
		cb.Note(fmt.Sprintf(e.i18n.T(MsgListPageHint), page, totalPages))
	}

	return cb.Build(), nil
}

// dirCardTruncPath shortens absolute paths for card list rows.
func dirCardTruncPath(absPath string) string {
	r := []rune(absPath)
	if len(r) <= 56 {
		return absPath
	}
	return string(r[:53]) + "…"
}

func (e *Engine) renderDirCard(sessionKey string, page int) (*Card, error) {
	agent, _ := e.sessionContextForKey(sessionKey)
	switcher, ok := agent.(WorkDirSwitcher)
	if !ok {
		return nil, fmt.Errorf("%s", e.i18n.T(MsgDirNotSupported))
	}
	currentDir := switcher.GetWorkDir()
	var history []string
	if e.dirHistory != nil {
		history = e.dirHistory.List(e.name)
	}
	total := len(history)
	totalPages := 1
	if total > 0 {
		totalPages = (total + dirCardPageSize - 1) / dirCardPageSize
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * dirCardPageSize
	end := start + dirCardPageSize
	if end > total {
		end = total
	}

	cb := NewCard().Title(e.i18n.T(MsgDirCardTitle), "turquoise")
	cb.Markdown(e.i18n.Tf(MsgDirCurrent, currentDir))
	if total == 0 {
		cb.Note(e.i18n.T(MsgDirCardEmptyHistory))
	} else {
		cb.Divider()
		for i := start; i < end; i++ {
			dir := history[i]
			marker := "◻"
			if dir == currentDir {
				marker = "▶"
			}
			btnType := "default"
			if dir == currentDir {
				btnType = "primary"
			}
			displayPath := dirCardTruncPath(dir)
			cb.ListItemBtn(
				fmt.Sprintf("%s **%d.** `%s`", marker, i+1, displayPath),
				fmt.Sprintf("#%d", i+1),
				btnType,
				fmt.Sprintf("act:/dir select %d", i+1),
			)
		}
	}

	var actionRow []CardButton
	if e.dirHistory != nil && len(history) >= 2 {
		actionRow = append(actionRow, DefaultBtn(e.i18n.T(MsgDirCardPrev), "act:/dir prev"))
	}
	actionRow = append(actionRow, DefaultBtn(e.i18n.T(MsgDirCardReset), "act:/dir reset"))
	cb.Buttons(actionRow...)

	var navBtns []CardButton
	if totalPages > 1 && page > 1 {
		navBtns = append(navBtns, e.cardPrevButton(fmt.Sprintf("nav:/dir %d", page-1)))
	}
	navBtns = append(navBtns, e.cardBackButton())
	if totalPages > 1 && page < totalPages {
		navBtns = append(navBtns, e.cardNextButton(fmt.Sprintf("nav:/dir %d", page+1)))
	}
	cb.Buttons(navBtns...)

	if totalPages > 1 {
		cb.Note(fmt.Sprintf(e.i18n.T(MsgDirCardPageHint), page, totalPages))
	}

	return cb.Build(), nil
}

// ──────────────────────────────────────────────────────────────
// Navigable sub-cards (for in-place card updates)
// ──────────────────────────────────────────────────────────────

func (e *Engine) renderCurrentCard(sessionKey string) *Card {
	_, sessions := e.sessionContextForKey(sessionKey)
	s := sessions.GetOrCreateActive(sessionKey)
	agentID := s.GetAgentSessionID()
	if agentID == "" {
		agentID = e.i18n.T(MsgSessionNotStarted)
	}
	content := fmt.Sprintf(e.i18n.T(MsgCurrentSession), s.Name, agentID, len(s.History))
	return NewCard().
		Title(e.i18n.T(MsgCardTitleCurrentSession), "turquoise").
		Markdown(content).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderHistoryCard(sessionKey string) *Card {
	agent, sessions := e.sessionContextForKey(sessionKey)
	s := sessions.GetOrCreateActive(sessionKey)
	entries := s.GetHistory(10)

	agentSID := s.GetAgentSessionID()
	if len(entries) == 0 && agentSID != "" {
		if hp, ok := agent.(HistoryProvider); ok {
			if agentEntries, err := hp.GetSessionHistory(e.ctx, agentSID, 10); err == nil {
				entries = agentEntries
			}
		}
	}

	if len(entries) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleHistory), "turquoise", e.i18n.T(MsgHistoryEmpty))
	}

	var sb strings.Builder
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

	return NewCard().
		Title(e.i18n.Tf(MsgCardTitleHistoryLast, len(entries)), "turquoise").
		Markdown(sb.String()).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderProviderCard() *Card {
	switcher, ok := e.agent.(ProviderSwitcher)
	if !ok {
		return e.simpleCard(e.i18n.T(MsgCardTitleProvider), "indigo", e.i18n.T(MsgProviderNotSupported))
	}

	current := switcher.GetActiveProvider()
	providers := switcher.ListProviders()

	if current == nil && len(providers) == 0 {
		cb := NewCard().Title(e.i18n.T(MsgCardTitleProvider), "indigo").
			Markdown(e.i18n.T(MsgProviderNone))
		cb.Buttons(PrimaryBtn("➕ "+e.i18n.T(MsgCardTitleProviderAdd), "nav:/provider/add"), e.cardBackButton())
		return cb.Build()
	}

	var body strings.Builder
	if current != nil {
		body.WriteString(fmt.Sprintf(e.i18n.T(MsgProviderCurrent), current.Name))
		body.WriteString("\n\n")
	}

	cb := NewCard().Title(e.i18n.T(MsgCardTitleProvider), "indigo").Markdown(body.String())
	if len(providers) > 0 {
		var opts []CardSelectOption
		initVal := ""
		if current != nil {
			opts = append(opts, CardSelectOption{
				Text:  "🚫 " + e.i18n.T(MsgProviderClearOption),
				Value: "act:/provider clear",
			})
		}
		for _, prov := range providers {
			label := prov.Name
			if prov.BaseURL != "" {
				label += " (" + prov.BaseURL + ")"
			}
			val := "act:/provider " + prov.Name
			opts = append(opts, CardSelectOption{Text: label, Value: val})
			if current != nil && prov.Name == current.Name {
				initVal = val
			}
		}
		cb.Select(e.i18n.T(MsgProviderSelectPlaceholder), opts, initVal)
	}
	cb.Buttons(PrimaryBtn("➕ "+e.i18n.T(MsgCardTitleProviderAdd), "nav:/provider/add"), e.cardBackButton())
	return cb.Build()
}

func (e *Engine) renderProviderAddCard(sessionKey string) *Card {
	if pa := e.getPendingProviderAdd(sessionKey); pa != nil {
		switch pa.phase {
		case "preset":
			body := fmt.Sprintf(e.i18n.T(MsgProviderAddApiKeyPrompt), pa.name)
			if pa.inviteURL != "" {
				body += "\n\n" + fmt.Sprintf(e.i18n.T(MsgProviderAddInviteHint), pa.inviteURL)
			}
			cb := NewCard().Title(e.i18n.T(MsgCardTitleProviderAdd), "indigo").
				Markdown(body)
			cb.Buttons(DefaultBtn(e.i18n.T(MsgCardBack), "act:/provider/add-cancel"))
			return cb.Build()
		case "other":
			cb := NewCard().Title(e.i18n.T(MsgCardTitleProviderAdd), "indigo").
				Markdown(e.i18n.T(MsgProviderAddUsage))
			cb.Buttons(DefaultBtn(e.i18n.T(MsgCardBack), "act:/provider/add-cancel"))
			return cb.Build()
		}
	}

	// Show preset selection card
	agentType := e.agent.Name()
	lang := e.i18n.CurrentLang()

	cb := NewCard().Title(e.i18n.T(MsgCardTitleProviderAdd), "indigo").
		Markdown(e.i18n.T(MsgProviderAddPickHint))

	presets, err := FetchProviderPresets()
	if err == nil && presets != nil {
		for _, preset := range presets.Providers {
			if !preset.SupportsAgent(agentType) {
				continue
			}
			desc := preset.Description
			if lang == LangChinese || lang == LangTraditionalChinese {
				if preset.DescriptionZh != "" {
					desc = preset.DescriptionZh
				}
			}
			label := preset.DisplayName
			if desc != "" {
				label += " — " + desc
			}
			cb.ListItem(label, preset.DisplayName, "act:/provider/add "+preset.Name)
		}
	}

	// Show linkable global providers not yet in this project
	if e.listGlobalProvidersFunc != nil {
		globals, gErr := e.listGlobalProvidersFunc(agentType)
		if gErr == nil && len(globals) > 0 {
			var existing map[string]bool
			if sw, ok := e.agent.(ProviderSwitcher); ok {
				existing = make(map[string]bool)
				for _, p := range sw.ListProviders() {
					existing[p.Name] = true
				}
			}
			var linkable []ProviderConfig
			for _, g := range globals {
				if existing[g.Name] {
					continue
				}
				linkable = append(linkable, g)
			}
			if len(linkable) > 0 {
				cb.Divider()
				cb.Markdown("🔗 " + e.i18n.T(MsgProviderLinkGlobal))
				for _, g := range linkable {
					label := g.Name
					if g.Model != "" {
						label += " · " + g.Model
					}
					cb.ListItem(label, g.Name, "act:/provider/link "+g.Name)
				}
			}
		}
	}

	cb.Divider()
	cb.Buttons(
		DefaultBtn("✏️ "+e.i18n.T(MsgProviderAddOther), "act:/provider/add-other"),
		DefaultBtn(e.i18n.T(MsgCardBack), "nav:/provider"),
	)
	return cb.Build()
}

func (e *Engine) executeProviderLink(sessionKey, name string) {
	name = strings.TrimSpace(name)
	if name == "" || e.listGlobalProvidersFunc == nil {
		return
	}
	agentType := e.agent.Name()
	globals, err := e.listGlobalProvidersFunc(agentType)
	if err != nil {
		slog.Warn("provider link: list global providers", "error", err)
		return
	}
	var target *ProviderConfig
	for i := range globals {
		if globals[i].Name == name {
			target = &globals[i]
			break
		}
	}
	if target == nil {
		slog.Warn("provider link: global provider not found or incompatible agent type", "name", name, "agentType", agentType)
		return
	}

	sw, ok := e.agent.(ProviderSwitcher)
	if !ok {
		return
	}
	for _, p := range sw.ListProviders() {
		if p.Name == name {
			return // already linked
		}
	}
	updated := append(sw.ListProviders(), *target)
	sw.SetProviders(updated)

	// Save the updated provider_refs
	if e.providerRefsSaveFunc != nil {
		refs := make([]string, 0, len(updated))
		for _, p := range updated {
			refs = append(refs, p.Name)
		}
		if err := e.providerRefsSaveFunc(refs); err != nil {
			slog.Error("provider link: save refs", "error", err)
		}
	}
}

func (e *Engine) renderCronCard(sessionKey string, userID string) *Card {
	if e.cronScheduler == nil {
		return e.simpleCard(e.i18n.T(MsgCardTitleCron), "orange", e.i18n.T(MsgCronNotAvailable))
	}

	jobs := e.cronScheduler.Store().ListBySessionKey(sessionKey)
	if len(jobs) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleCron), "orange", e.i18n.T(MsgCronEmpty))
	}

	lang := e.i18n.CurrentLang()
	now := time.Now()

	cb := NewCard().Title(e.i18n.T(MsgCardTitleCron), "orange")
	cb.Markdown(fmt.Sprintf(e.i18n.T(MsgCronListTitle), len(jobs)))

	for _, j := range jobs {
		status := "✅"
		if !j.Enabled {
			status = "⏸"
		}

		desc := j.Description
		if desc == "" {
			if j.IsShellJob() {
				desc = "🖥 " + truncateStr(j.Exec, 60)
			} else {
				desc = truncateStr(j.Prompt, 60)
			}
		}
		if j.Mute {
			desc += " [mute]"
		}

		human := CronExprToHuman(j.CronExpr, lang)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%s %s\n", status, desc))
		sb.WriteString(e.i18n.Tf(MsgCronIDLabel, j.ID))
		sb.WriteString(e.i18n.Tf(MsgCronScheduleLabel, human, j.CronExpr))
		nextRun := e.cronScheduler.NextRun(j.ID)
		if !nextRun.IsZero() {
			fmtStr := cronTimeFormat(nextRun, now)
			sb.WriteString(e.i18n.Tf(MsgCronNextRunLabel, nextRun.Format(fmtStr)))
		}
		if !j.LastRun.IsZero() {
			fmtStr := cronTimeFormat(j.LastRun, now)
			sb.WriteString(e.i18n.Tf(MsgCronLastRunLabel, j.LastRun.Format(fmtStr)))
			if j.LastError != "" {
				sb.WriteString(e.i18n.Tf(MsgCronFailedSuffix, truncateStr(j.LastError, 40)))
			}
			sb.WriteString("\n")
		}
		cb.Markdown(sb.String())

		var btns []CardButton
		if j.Enabled {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgCronBtnDisable), fmt.Sprintf("act:/cron disable %s", j.ID)))
		} else {
			btns = append(btns, PrimaryBtn(e.i18n.T(MsgCronBtnEnable), fmt.Sprintf("act:/cron enable %s", j.ID)))
		}
		if j.Mute {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgCronBtnUnmute), fmt.Sprintf("act:/cron unmute %s", j.ID)))
		} else {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgCronBtnMute), fmt.Sprintf("act:/cron mute %s", j.ID)))
		}
		btns = append(btns, DangerBtn(e.i18n.T(MsgCronBtnDelete), fmt.Sprintf("act:/cron delete %s", j.ID)))
		cb.ButtonsEqual(btns...)
	}

	cb.Divider()
	cb.Note(e.i18n.T(MsgCronCardHint))
	cb.Buttons(e.cardBackButton())
	return cb.Build()
}

func (e *Engine) renderCommandsCard() *Card {
	cmds := e.commands.ListAll()
	if len(cmds) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleCommands), "purple", e.i18n.T(MsgCommandsEmpty))
	}

	var sb strings.Builder
	sb.WriteString(e.i18n.Tf(MsgCommandsTitle, len(cmds)))
	for _, c := range cmds {
		tag := ""
		if c.Source == "agent" {
			tag = e.i18n.T(MsgCommandsTagAgent)
		} else if c.Exec != "" {
			tag = e.i18n.T(MsgCommandsTagShell)
		}
		desc := c.Description
		if desc == "" {
			if c.Exec != "" {
				desc = "$ " + truncateStr(c.Exec, 60)
			} else {
				desc = truncateStr(c.Prompt, 60)
			}
		}
		sb.WriteString(fmt.Sprintf("/%s%s — %s\n", c.Name, tag, desc))
	}

	return NewCard().Title(e.i18n.T(MsgCardTitleCommands), "purple").
		Markdown(sb.String()).
		Note(e.i18n.T(MsgCommandsHint)).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderAliasCard() *Card {
	e.aliasMu.RLock()
	defer e.aliasMu.RUnlock()

	if len(e.aliases) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleAlias), "purple", e.i18n.T(MsgAliasEmpty))
	}

	names := make([]string, 0, len(e.aliases))
	for n := range e.aliases {
		names = append(names, n)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgAliasListHeader), len(e.aliases)))
	sb.WriteString("\n")
	for _, n := range names {
		sb.WriteString(fmt.Sprintf("`%s` → `%s`\n", n, e.aliases[n]))
	}

	return NewCard().Title(e.i18n.T(MsgCardTitleAlias), "purple").
		Markdown(sb.String()).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderConfigCard() *Card {
	items := e.configItems()
	isZh := e.i18n.IsZhLike()

	var sb strings.Builder
	sb.WriteString(e.i18n.T(MsgConfigTitle))
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("`%s` = `%s`\n  %s\n\n", item.key, item.getFunc(), item.description(isZh)))
	}

	return NewCard().Title(e.i18n.T(MsgCardTitleConfig), "grey").
		Markdown(sb.String()).
		Note(e.i18n.T(MsgConfigHint)).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderSkillsCard() *Card {
	skills := e.skills.ListAll()
	if len(skills) == 0 {
		return e.simpleCard(e.i18n.T(MsgCardTitleSkills), "purple", e.i18n.T(MsgSkillsEmpty))
	}

	var sb strings.Builder
	sb.WriteString(e.i18n.Tf(MsgSkillsTitle, e.agent.Name(), len(skills)))
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("  /%s — %s\n", s.Name, s.Description))
	}

	return NewCard().Title(e.i18n.T(MsgCardTitleSkills), "purple").
		Markdown(sb.String()).
		Note(e.i18n.T(MsgSkillsHint)).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderDoctorCard() *Card {
	results := RunDoctorChecks(e.ctx, e.agent, e.platforms)
	report := FormatDoctorResults(results, e.i18n)
	return NewCard().
		Title(e.i18n.T(MsgCardTitleDoctor), "orange").
		Markdown(report).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderVersionCard() *Card {
	return NewCard().
		Title(e.i18n.T(MsgCardTitleVersion), "grey").
		Markdown(VersionInfo).
		Buttons(e.cardBackButton()).
		Build()
}

func (e *Engine) renderUpgradeCard() *Card {
	title := e.i18n.T(MsgCardTitleUpgrade)
	cur := CurrentVersion
	if cur == "" || cur == "dev" {
		return e.simpleCard(title, "grey", e.i18n.T(MsgUpgradeDevBuild))
	}

	type result struct {
		release *ReleaseInfo
		err     error
	}
	ch := make(chan result, 1)
	useGitee := e.i18n.IsZhLike()
	go func() {
		r, err := CheckForUpdate(cur, useGitee)
		ch <- result{r, err}
	}()

	var content string
	select {
	case res := <-ch:
		if res.err != nil {
			content = e.i18n.Tf(MsgError, res.err)
		} else if res.release == nil {
			content = fmt.Sprintf(e.i18n.T(MsgUpgradeUpToDate), cur)
		} else {
			body := res.release.Body
			if len([]rune(body)) > 300 {
				body = string([]rune(body)[:300]) + "…"
			}
			content = fmt.Sprintf(e.i18n.T(MsgUpgradeAvailable), cur, res.release.TagName, body)
		}
	case <-time.After(8 * time.Second):
		content = "⏱ " + e.i18n.T(MsgUpgradeChecking) + e.i18n.T(MsgUpgradeTimeoutSuffix)
	}

	return NewCard().
		Title(title, "grey").
		Markdown(content).
		Buttons(e.cardBackButton()).
		Build()
}
func (e *Engine) renderUsageCard(report *UsageReport) *Card {
	lang := e.i18n.CurrentLang()
	return NewCard().
		Title(usageCardTitle(lang), "indigo").
		Markdown(strings.TrimSpace(formatUsageReport(report, lang))).
		Buttons(e.cardBackButton()).
		Build()
}

func formatUsageResetTime(lang Language, resetAfterSeconds int) string {
	if resetAfterSeconds <= 0 {
		switch lang {
		case LangChinese, LangTraditionalChinese:
			return "-"
		case LangJapanese:
			return "-"
		case LangSpanish:
			return "-"
		default:
			return "-"
		}
	}
	return formatDurationI18n(time.Duration(resetAfterSeconds)*time.Second, lang)
}

func usageAccountLabel(lang Language) string {
	switch lang {
	case LangChinese:
		return "账号："
	case LangTraditionalChinese:
		return "帳號："
	case LangJapanese:
		return "アカウント: "
	case LangSpanish:
		return "Cuenta: "
	default:
		return "Account: "
	}
}

func usageWindowLabel(lang Language, seconds int) string {
	switch seconds {
	case 18000:
		switch lang {
		case LangChinese:
			return "5小时限额"
		case LangTraditionalChinese:
			return "5小時限額"
		case LangJapanese:
			return "5時間枠"
		case LangSpanish:
			return "Límite 5h"
		default:
			return "5h limit"
		}
	case 604800:
		switch lang {
		case LangChinese:
			return "7日限额"
		case LangTraditionalChinese:
			return "7日限額"
		case LangJapanese:
			return "7日枠"
		case LangSpanish:
			return "Límite 7d"
		default:
			return "7d limit"
		}
	default:
		switch lang {
		case LangChinese, LangTraditionalChinese:
			return formatDurationI18n(time.Duration(seconds)*time.Second, lang) + "限额"
		case LangJapanese:
			return formatDurationI18n(time.Duration(seconds)*time.Second, lang) + "枠"
		case LangSpanish:
			return "Límite " + formatDurationI18n(time.Duration(seconds)*time.Second, lang)
		default:
			return formatDurationI18n(time.Duration(seconds)*time.Second, lang) + " limit"
		}
	}
}

func usageRemainingLabel(lang Language) string {
	switch lang {
	case LangChinese:
		return "剩余"
	case LangTraditionalChinese:
		return "剩餘"
	case LangJapanese:
		return "残り"
	case LangSpanish:
		return "restante"
	default:
		return "Remaining"
	}
}

func usageResetLabel(lang Language) string {
	switch lang {
	case LangChinese:
		return "重置"
	case LangTraditionalChinese:
		return "重置"
	case LangJapanese:
		return "リセット"
	case LangSpanish:
		return "Reinicio"
	default:
		return "Resets"
	}
}

func usageColon(lang Language) string {
	switch lang {
	case LangChinese, LangTraditionalChinese:
		return "："
	default:
		return ": "
	}
}

func usageCardTitle(lang Language) string {
	switch lang {
	case LangChinese:
		return "Usage"
	case LangTraditionalChinese:
		return "Usage"
	case LangJapanese:
		return "Usage"
	case LangSpanish:
		return "Usage"
	default:
		return "Usage"
	}
}

func usageUnavailableText(lang Language) string {
	switch lang {
	case LangChinese:
		return "暂无 usage 信息。"
	case LangTraditionalChinese:
		return "暫無 usage 資訊。"
	case LangJapanese:
		return "usage 情報はありません。"
	case LangSpanish:
		return "No hay datos de usage."
	default:
		return "Usage unavailable."
	}
}

func splitCardTitleBody(content string) (string, string) {
	content = strings.TrimSpace(content)
	parts := strings.SplitN(content, "\n\n", 2)
	title := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return title, ""
	}
	return title, strings.TrimSpace(parts[1])
}

func (e *Engine) cardBackButton() CardButton {
	return DefaultBtn(e.i18n.T(MsgCardBack), "nav:/help")
}

func (e *Engine) modelCardBackButton() CardButton {
	return DefaultBtn(e.i18n.T(MsgCardBack), "nav:/model")
}

func (e *Engine) cardPrevButton(action string) CardButton {
	return DefaultBtn(e.i18n.T(MsgCardPrev), action)
}

func (e *Engine) cardNextButton(action string) CardButton {
	return DefaultBtn(e.i18n.T(MsgCardNext), action)
}

// simpleCard builds a card with a title, markdown body and a single Back button.
// Used to reduce repetition across render functions that share this pattern.
func (e *Engine) simpleCard(title, color, content string) *Card {
	return NewCard().Title(title, color).Markdown(content).Buttons(e.cardBackButton()).Build()
}

// renderListCardSafe wraps renderListCard and returns an error card on failure.
func (e *Engine) renderListCardSafe(sessionKey string, page int) *Card {
	card, err := e.renderListCard(sessionKey, page)
	if err != nil {
		agent, _ := e.sessionContextForKey(sessionKey)
		return e.simpleCard(e.i18n.Tf(MsgCardTitleSessions, agent.Name(), 0), "red", err.Error())
	}
	return card
}

// renderDirCardSafe wraps renderDirCard and returns an error card on failure.
func (e *Engine) renderDirCardSafe(sessionKey string, page int) *Card {
	card, err := e.renderDirCard(sessionKey, page)
	if err != nil {
		return e.simpleCard(e.i18n.T(MsgDirCardTitle), "red", err.Error())
	}
	return card
}

func (e *Engine) renderStatusCard(sessionKey string, userID string) *Card {
	agent, sessions := e.sessionContextForKey(sessionKey)
	platNames := make([]string, len(e.platforms))
	for i, pl := range e.platforms {
		platNames[i] = pl.Name()
	}
	platformStr := strings.Join(platNames, ", ")
	if len(platNames) == 0 {
		platformStr = "-"
	}

	workDirStr := ""
	if wd, ok := agent.(interface{ GetWorkDir() string }); ok {
		workDirStr = strings.TrimSpace(wd.GetWorkDir())
	}
	if workDirStr == "" {
		workDirStr, _ = os.Getwd()
	}

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

	s := sessions.GetOrCreateActive(sessionKey)
	sessionDisplayName := sessions.GetSessionName(s.GetAgentSessionID())
	if sessionDisplayName == "" {
		sessionDisplayName = s.GetName()
	}
	sessionStr := e.i18n.Tf(MsgStatusSession, sessionDisplayName, len(s.History))

	var cronStr string
	if e.cronScheduler != nil {
		if jobs := e.cronScheduler.Store().ListBySessionKey(sessionKey); len(jobs) > 0 {
			enabledCount := 0
			for _, j := range jobs {
				if j.Enabled {
					enabledCount++
				}
			}
			cronStr = e.i18n.Tf(MsgStatusCron, len(jobs), enabledCount)
		}
	}

	sessionKeyStr := e.i18n.Tf(MsgStatusSessionKey, sessionKey)

	agentSIDStr := ""
	if agentSID := s.GetAgentSessionID(); agentSID != "" {
		agentSIDStr = e.i18n.Tf(MsgStatusAgentSID, agentSID)
	}

	userIDStr := ""
	if userID != "" {
		userIDStr = e.i18n.Tf(MsgStatusUserID, userID)
	}

	statusText := e.i18n.Tf(MsgStatusTitle,
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
	)
	title, body := splitCardTitleBody(statusText)

	return NewCard().
		Title(title, "green").
		Markdown(body).
		Buttons(e.cardBackButton()).
		Build()
}

func cronTimeFormat(t, now time.Time) string {
	if t.Year() != now.Year() {
		return "2006-01-02 15:04"
	}
	return "01-02 15:04"
}

func formatDurationI18n(d time.Duration, lang Language) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	switch lang {
	case LangChinese, LangTraditionalChinese:
		if days > 0 {
			return fmt.Sprintf("%d天 %d小时 %d分钟", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%d小时 %d分钟", hours, minutes)
		}
		return fmt.Sprintf("%d分钟", minutes)
	case LangJapanese:
		if days > 0 {
			return fmt.Sprintf("%d日 %d時間 %d分", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%d時間 %d分", hours, minutes)
		}
		return fmt.Sprintf("%d分", minutes)
	case LangSpanish:
		if days > 0 {
			return fmt.Sprintf("%d días %dh %dm", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dm", minutes)
	default:
		if days > 0 {
			return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dm", minutes)
	}
}

