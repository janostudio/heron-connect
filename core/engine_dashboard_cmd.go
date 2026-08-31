package core

// engine_dashboard_cmd.go — the /dashboard IM command: on-demand usage
// statistics for the current project, rendered as a card on CardSender
// platforms and markdown elsewhere.

import (
	"fmt"
	"strings"
	"time"
)

// cmdDashboard handles /dashboard [today|yesterday|week|lastweek].
func (e *Engine) cmdDashboard(p Platform, msg *Message, args []string) {
	if e.statsRecorder == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDashboardNotCollected))
		return
	}

	now := time.Now()
	var period DashboardPeriod
	switch strings.ToLower(strings.Join(args, "")) {
	case "", "today":
		period = DayDashboardPeriod(now)
	case "yesterday":
		period = DayDashboardPeriod(now.AddDate(0, 0, -1))
	case "week":
		period = WeekDashboardPeriod(now)
	case "lastweek", "last_week":
		period = WeekDashboardPeriod(now.AddDate(0, 0, -7))
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDashboardUsage))
		return
	}

	report := e.buildEngineDashboardReport(period, 10)
	if report == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDashboardNotCollected))
		return
	}
	if report.Totals.Turns == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgDashboardNoData))
		return
	}

	if supportsCards(p) {
		e.replyWithCard(p, msg.ReplyCtx, e.renderDashboardCard(report))
		return
	}
	e.reply(p, msg.ReplyCtx, RenderDashboardMarkdown(report))
}

// renderDashboardCard builds the /dashboard statistics card.
func (e *Engine) renderDashboardCard(r *DashboardReport) *Card {
	periodName := map[string]string{
		DashboardPeriodDay: "今日", DashboardPeriodWeek: "本周",
		DashboardPeriodMonth: "本月", DashboardPeriodCustom: "区间",
	}[r.Period.Type]
	card := &Card{
		Header: &CardHeader{Title: fmt.Sprintf("📊 %s统计 · %s", periodName, r.Period.Label), Color: "blue"},
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**会话** %d（新 %d）｜**轮次** %d（用户 %d / 定时 %d）\n",
		r.Totals.SessionsActive, r.Totals.SessionsNew, r.Totals.Turns, r.Totals.TurnsUser, r.Totals.TurnsCron)
	fmt.Fprintf(&b, "**Token** 输入 %s / 输出 %s（合计 %s）\n",
		formatTokenCount(r.Totals.InputTokens), formatTokenCount(r.Totals.OutputTokens), formatTokenCount(r.Totals.TotalTokens))
	fmt.Fprintf(&b, "**工具** %d 次｜**错误** %d｜**耗时** %d 分钟",
		r.Totals.ToolCalls, r.Totals.Errors, r.Totals.ActiveMs/60000)
	card.Elements = append(card.Elements, CardMarkdown{Content: b.String()})

	if len(r.Topics) > 0 {
		var tb strings.Builder
		for i, t := range r.Topics {
			if i >= 5 {
				break
			}
			name := t.Name
			if name == "" {
				name = t.SessionID
			}
			fmt.Fprintf(&tb, "• %s — %d轮 · %s tok\n", name, t.Turns, formatTokenCount(t.TotalTokens))
		}
		card.Elements = append(card.Elements, CardDivider{}, CardMarkdown{Content: strings.TrimRight(tb.String(), "\n")})
	}

	note := fmt.Sprintf("数据截至 %s · 范围：%s", r.GeneratedAt.Format("15:04"), e.name)
	if r.Totals.TokensEstimated {
		note += " · 部分token为估算值"
	}
	card.Elements = append(card.Elements, CardNote{Text: note})
	return card
}
