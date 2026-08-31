package core

// stats_aggregate.go — dashboard report aggregation.
//
// Pure functions: TurnRecord slice in → DashboardReport (versioned JSON
// contract) out. Consumers: the management REST API (/api/v1/dashboard*),
// the /dashboard IM command, and the {{dashboard.*}} cron prompt template
// variables. The engine contributes only objective metrics; business
// enrichment (summary/tags/metrics) lives in the business-generated
// insights.json, rendered by the web client.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Dashboard period types.
const (
	DashboardPeriodDay    = "day"
	DashboardPeriodWeek   = "week"
	DashboardPeriodMonth  = "month"
	DashboardPeriodCustom = "custom"
)

// DashboardPeriod is a resolved aggregation window.
type DashboardPeriod struct {
	Type  string    `json:"type"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Label string    `json:"label"`
}

// DayDashboardPeriod returns the local calendar day containing t.
func DayDashboardPeriod(t time.Time) DashboardPeriod {
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return DashboardPeriod{
		Type:  DashboardPeriodDay,
		Start: start,
		End:   start.AddDate(0, 0, 1),
		Label: start.Format("2006-01-02"),
	}
}

// WeekDashboardPeriod returns the Monday-start week containing t.
func WeekDashboardPeriod(t time.Time) DashboardPeriod {
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	weekday := (int(start.Weekday()) + 6) % 7 // Monday = 0
	start = start.AddDate(0, 0, -weekday)
	_, week := start.ISOWeek()
	return DashboardPeriod{
		Type:  DashboardPeriodWeek,
		Start: start,
		End:   start.AddDate(0, 0, 7),
		Label: fmt.Sprintf("%d-W%02d", start.Year(), week),
	}
}

// MonthDashboardPeriod returns the calendar month containing t.
func MonthDashboardPeriod(t time.Time) DashboardPeriod {
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	return DashboardPeriod{
		Type:  DashboardPeriodMonth,
		Start: start,
		End:   start.AddDate(0, 1, 0),
		Label: start.Format("2006-01"),
	}
}

// CustomDashboardPeriod builds a custom window (start inclusive, end
// exclusive). Caller validates the span.
func CustomDashboardPeriod(start, end time.Time) DashboardPeriod {
	return DashboardPeriod{
		Type:  DashboardPeriodCustom,
		Start: start,
		End:   end,
		Label: start.Format("2006-01-02") + "~" + end.AddDate(0, 0, -1).Format("2006-01-02"),
	}
}

// DashboardScope records what the report covers.
type DashboardScope struct {
	Project string `json:"project"` // "all" or a project name
}

// DashboardTotals aggregates window-level counters.
type DashboardTotals struct {
	SessionsActive    int   `json:"sessions_active"`
	SessionsNew       int   `json:"sessions_new"`
	Turns             int   `json:"turns"`
	TurnsUser         int   `json:"turns_user"`
	TurnsCron         int   `json:"turns_cron"`
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	CacheWriteTokens  int64 `json:"cache_write_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	TokensEstimated   bool  `json:"tokens_estimated"`
	ActiveMs          int64 `json:"active_ms"`
	ToolCalls         int   `json:"tool_calls"`
	Errors            int   `json:"errors"`
}

// DashboardBucket is one timeline slot.
type DashboardBucket struct {
	Label     string `json:"label"`
	Turns     int    `json:"turns"`
	Tokens    int64  `json:"tokens"`
	ToolCalls int    `json:"tool_calls"`
}

// DashboardBreakdown is one grouped dimension row.
type DashboardBreakdown struct {
	Name        string `json:"name"`
	Sessions    int    `json:"sessions"`
	Turns       int    `json:"turns"`
	TotalTokens int64  `json:"total_tokens"`
	ToolCalls   int    `json:"tool_calls"`
	Errors      int    `json:"errors"`
	ActiveMs    int64  `json:"active_ms"`
}

// DashboardTopic is one per-session summary row.
type DashboardTopic struct {
	SessionID      string    `json:"session_id"`
	AgentSessionID string    `json:"agent_session_id,omitempty"`
	Name           string    `json:"name"`
	Project        string    `json:"project"`
	Platform       string    `json:"platform"`
	UserName       string    `json:"user_name,omitempty"`
	Turns          int       `json:"turns"`
	TotalTokens    int64     `json:"total_tokens"`
	ToolCalls      int       `json:"tool_calls"`
	ActiveMs       int64     `json:"active_ms"`
	FirstMessage   string    `json:"first_message,omitempty"`
	LastActive     time.Time `json:"last_active"`
}

// DashboardToolCount is a top-tool row.
type DashboardToolCount struct {
	Name  string `json:"name"`
	Calls int    `json:"calls"`
}

// DashboardUserCount is a top-user row.
type DashboardUserCount struct {
	Name        string `json:"name"`
	Turns       int    `json:"turns"`
	TotalTokens int64  `json:"total_tokens"`
}

// DashboardReport is the versioned aggregate contract shared by the web
// dashboard, the /dashboard IM command and the {{dashboard.*}} template
// variables. (Named DashboardReport because UsageReport is the account
// quota type in interfaces.go.)
type DashboardReport struct {
	Version     int                  `json:"version"`
	Period      DashboardPeriod      `json:"period"`
	Scope       DashboardScope       `json:"scope"`
	GeneratedAt time.Time            `json:"generated_at"`
	Totals      DashboardTotals      `json:"totals"`
	Timeline    []DashboardBucket    `json:"timeline"`
	ByProject   []DashboardBreakdown `json:"by_project"`
	ByPlatform  []DashboardBreakdown `json:"by_platform"`
	ByAgent     []DashboardBreakdown `json:"by_agent"`
	Topics      []DashboardTopic     `json:"topics"`
	TopTools    []DashboardToolCount `json:"top_tools"`
	TopUsers    []DashboardUserCount `json:"top_users"`
}

// DashboardAggregateOptions tunes aggregation.
type DashboardAggregateOptions struct {
	Project   string // "" or "all" = all projects; otherwise filter to one
	MaxTopics int    // 0 = default 10
}

// AggregateDashboardReport folds records within period.Start/End (records
// are expected pre-filtered to the window by ReadTurnRecords) into a
// DashboardReport.
func AggregateDashboardReport(recs []TurnRecord, period DashboardPeriod, opt DashboardAggregateOptions) *DashboardReport {
	if opt.MaxTopics <= 0 {
		opt.MaxTopics = 10
	}
	project := opt.Project
	if project == "" {
		project = "all"
	}
	r := &DashboardReport{
		Version:     1,
		Period:      period,
		Scope:       DashboardScope{Project: project},
		GeneratedAt: time.Now(),
	}

	filtered := make([]TurnRecord, 0, len(recs))
	for _, rec := range recs {
		// Defense in depth: enforce the window even if the caller did not
		// pre-filter (ReadTurnRecords already does).
		if rec.TS.Before(period.Start) || !rec.TS.Before(period.End) {
			continue
		}
		if opt.Project != "" && opt.Project != "all" && rec.Project != opt.Project {
			continue
		}
		filtered = append(filtered, rec)
	}

	activeSessions := map[string]bool{}
	newSessions := map[string]bool{}
	byProject := map[string]*DashboardBreakdown{}
	byPlatform := map[string]*DashboardBreakdown{}
	byAgent := map[string]*DashboardBreakdown{}
	projectSessions := map[string]map[string]bool{}
	platformSessions := map[string]map[string]bool{}
	agentSessions := map[string]map[string]bool{}
	toolCounts := map[string]int{}
	type sessionAgg struct {
		turns, toolCalls  int
		tokens, activeMs  int64
		name, agentSID    string
		platform, project string
		userName          string
		last              time.Time
	}
	sessionAggs := map[string]*sessionAgg{}
	userAggs := map[string]*DashboardUserCount{}

	getBreakdown := func(m map[string]*DashboardBreakdown, sessions map[string]map[string]bool, key string) *DashboardBreakdown {
		if key == "" {
			key = "(unknown)"
		}
		b := m[key]
		if b == nil {
			b = &DashboardBreakdown{Name: key}
			m[key] = b
			sessions[key] = map[string]bool{}
		}
		return b
	}

	for _, rec := range filtered {
		switch rec.Kind {
		case RecordKindSessionCreated:
			if rec.SessionID != "" {
				newSessions[rec.SessionID] = true
			}
			continue
		case RecordKindTurn:
		default:
			continue
		}

		r.Totals.Turns++
		if rec.Trigger == "cron" {
			r.Totals.TurnsCron++
		} else {
			r.Totals.TurnsUser++
		}
		r.Totals.InputTokens += int64(rec.InputTokens)
		r.Totals.OutputTokens += int64(rec.OutputTokens)
		r.Totals.CacheReadTokens += int64(rec.CacheReadTokens)
		r.Totals.CacheWriteTokens += int64(rec.CacheWriteTokens)
		if rec.TokensEstimated {
			r.Totals.TokensEstimated = true
		}
		r.Totals.ActiveMs += rec.DurationMs
		r.Totals.ToolCalls += rec.ToolCalls
		if rec.Error != "" {
			r.Totals.Errors++
		}

		if rec.SessionID != "" {
			activeSessions[rec.SessionID] = true
		}

		pb := getBreakdown(byProject, projectSessions, rec.Project)
		pb.Turns++
		pb.TotalTokens += int64(rec.InputTokens + rec.OutputTokens)
		pb.ToolCalls += rec.ToolCalls
		pb.ActiveMs += rec.DurationMs
		if rec.Error != "" {
			pb.Errors++
		}
		if rec.SessionID != "" {
			projectSessions[pb.Name][rec.SessionID] = true
		}

		fb := getBreakdown(byPlatform, platformSessions, rec.Platform)
		fb.Turns++
		fb.TotalTokens += int64(rec.InputTokens + rec.OutputTokens)
		fb.ToolCalls += rec.ToolCalls
		fb.ActiveMs += rec.DurationMs
		if rec.Error != "" {
			fb.Errors++
		}
		if rec.SessionID != "" {
			platformSessions[fb.Name][rec.SessionID] = true
		}

		ab := getBreakdown(byAgent, agentSessions, rec.Agent)
		ab.Turns++
		ab.TotalTokens += int64(rec.InputTokens + rec.OutputTokens)
		ab.ToolCalls += rec.ToolCalls
		ab.ActiveMs += rec.DurationMs
		if rec.Error != "" {
			ab.Errors++
		}
		if rec.SessionID != "" {
			agentSessions[ab.Name][rec.SessionID] = true
		}

		for name, calls := range rec.Tools {
			toolCounts[name] += calls
		}

		if rec.SessionID != "" {
			agg := sessionAggs[rec.SessionID]
			if agg == nil {
				agg = &sessionAgg{}
				sessionAggs[rec.SessionID] = agg
			}
			agg.turns++
			agg.toolCalls += rec.ToolCalls
			agg.tokens += int64(rec.InputTokens + rec.OutputTokens)
			agg.activeMs += rec.DurationMs
			if rec.SessionName != "" {
				agg.name = rec.SessionName
			}
			if rec.AgentSessionID != "" {
				agg.agentSID = rec.AgentSessionID
			}
			if rec.Platform != "" {
				agg.platform = rec.Platform
			}
			if rec.Project != "" {
				agg.project = rec.Project
			}
			if rec.UserName != "" {
				agg.userName = rec.UserName
			}
			if rec.TS.After(agg.last) {
				agg.last = rec.TS
			}
		}

		userKey := rec.UserName
		if userKey == "" {
			userKey = rec.UserID
		}
		if userKey != "" {
			ua := userAggs[userKey]
			if ua == nil {
				ua = &DashboardUserCount{Name: userKey}
				userAggs[userKey] = ua
			}
			ua.Turns++
			ua.TotalTokens += int64(rec.InputTokens + rec.OutputTokens)
		}
	}

	r.Totals.SessionsActive = len(activeSessions)
	r.Totals.SessionsNew = len(newSessions)
	r.Totals.TotalTokens = r.Totals.InputTokens + r.Totals.OutputTokens

	finalize := func(m map[string]*DashboardBreakdown, sessions map[string]map[string]bool) []DashboardBreakdown {
		out := make([]DashboardBreakdown, 0, len(m))
		for _, b := range m {
			b.Sessions = len(sessions[b.Name])
			out = append(out, *b)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Turns != out[j].Turns {
				return out[i].Turns > out[j].Turns
			}
			return out[i].TotalTokens > out[j].TotalTokens
		})
		return out
	}
	r.ByProject = finalize(byProject, projectSessions)
	r.ByPlatform = finalize(byPlatform, platformSessions)
	r.ByAgent = finalize(byAgent, agentSessions)

	// Timeline: day → hourly buckets; week/month/custom → daily buckets.
	if period.Type == DashboardPeriodDay {
		for h := 0; h < 24; h++ {
			r.Timeline = append(r.Timeline, DashboardBucket{Label: fmt.Sprintf("%02d:00", h)})
		}
	} else {
		for d := period.Start; d.Before(period.End); d = d.AddDate(0, 0, 1) {
			r.Timeline = append(r.Timeline, DashboardBucket{Label: d.Format("01-02")})
		}
	}
	bucketIndex := func(ts time.Time) int {
		if period.Type == DashboardPeriodDay {
			return ts.Hour()
		}
		return int(ts.Sub(period.Start).Hours() / 24)
	}
	for _, rec := range filtered {
		if rec.Kind != RecordKindTurn {
			continue
		}
		idx := bucketIndex(rec.TS)
		if idx < 0 || idx >= len(r.Timeline) {
			continue
		}
		r.Timeline[idx].Turns++
		r.Timeline[idx].Tokens += int64(rec.InputTokens + rec.OutputTokens)
		r.Timeline[idx].ToolCalls += rec.ToolCalls
	}

	// Topics: top sessions by turns.
	for sid, agg := range sessionAggs {
		r.Topics = append(r.Topics, DashboardTopic{
			SessionID:      sid,
			AgentSessionID: agg.agentSID,
			Name:           agg.name,
			Project:        agg.project,
			Platform:       agg.platform,
			UserName:       agg.userName,
			Turns:          agg.turns,
			TotalTokens:    agg.tokens,
			ToolCalls:      agg.toolCalls,
			ActiveMs:       agg.activeMs,
			LastActive:     agg.last,
		})
	}
	sort.Slice(r.Topics, func(i, j int) bool {
		if r.Topics[i].Turns != r.Topics[j].Turns {
			return r.Topics[i].Turns > r.Topics[j].Turns
		}
		return r.Topics[i].TotalTokens > r.Topics[j].TotalTokens
	})
	if len(r.Topics) > opt.MaxTopics {
		r.Topics = r.Topics[:opt.MaxTopics]
	}

	for name, calls := range toolCounts {
		r.TopTools = append(r.TopTools, DashboardToolCount{Name: name, Calls: calls})
	}
	sort.Slice(r.TopTools, func(i, j int) bool { return r.TopTools[i].Calls > r.TopTools[j].Calls })
	if len(r.TopTools) > 10 {
		r.TopTools = r.TopTools[:10]
	}
	for _, ua := range userAggs {
		r.TopUsers = append(r.TopUsers, *ua)
	}
	sort.Slice(r.TopUsers, func(i, j int) bool {
		if r.TopUsers[i].Turns != r.TopUsers[j].Turns {
			return r.TopUsers[i].Turns > r.TopUsers[j].Turns
		}
		return r.TopUsers[i].TotalTokens > r.TopUsers[j].TotalTokens
	})
	if len(r.TopUsers) > 10 {
		r.TopUsers = r.TopUsers[:10]
	}

	return r
}

// MetricsDir returns the metrics directory under dataDir.
func MetricsDir(dataDir string) string {
	return filepath.Join(dataDir, "metrics")
}

// formatTokenCount renders a compact token count (e.g. 1.4M, 45k).
func formatTokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// RenderDashboardMarkdown renders a report as the canonical markdown
// injected into {{dashboard.*}} template variables (design §3.3 — the
// rendering is part of the contract so agents see a stable structure).
func RenderDashboardMarkdown(r *DashboardReport) string {
	if r == nil {
		return "(no data)"
	}
	var b strings.Builder
	periodName := map[string]string{
		DashboardPeriodDay: "今日", DashboardPeriodWeek: "本周",
		DashboardPeriodMonth: "本月", DashboardPeriodCustom: "自定义区间",
	}[r.Period.Type]
	if periodName == "" {
		periodName = r.Period.Type
	}
	scopeName := r.Scope.Project
	if scopeName == "all" {
		scopeName = "全部项目"
	}
	fmt.Fprintf(&b, "# 统计数据：%s %s（范围：%s，截至 %s）\n\n",
		periodName, r.Period.Label, scopeName, r.GeneratedAt.Format("15:04"))

	fmt.Fprintf(&b, "## 总览\n")
	fmt.Fprintf(&b, "- 会话: %d（新会话 %d）｜轮次: %d（用户 %d / 定时任务 %d）\n",
		r.Totals.SessionsActive, r.Totals.SessionsNew, r.Totals.Turns, r.Totals.TurnsUser, r.Totals.TurnsCron)
	estimatedNote := ""
	if r.Totals.TokensEstimated {
		estimatedNote = " ※部分为估算值"
	}
	fmt.Fprintf(&b, "- Token: 输入 %s / 输出 %s（合计 %s）%s\n",
		formatTokenCount(r.Totals.InputTokens), formatTokenCount(r.Totals.OutputTokens),
		formatTokenCount(r.Totals.TotalTokens), estimatedNote)
	fmt.Fprintf(&b, "- 工具调用: %d 次｜错误: %d 次｜累计耗时: %d 分钟\n\n",
		r.Totals.ToolCalls, r.Totals.Errors, r.Totals.ActiveMs/60000)

	if len(r.Topics) > 0 {
		fmt.Fprintf(&b, "## 会话列表（按轮次排序）\n")
		fmt.Fprintf(&b, "| 会话 | 项目 | 平台 | 用户 | 轮次 | Token | 最后活跃 |\n")
		fmt.Fprintf(&b, "|---|---|---|---|---|---|---|\n")
		for _, t := range r.Topics {
			name := t.Name
			if name == "" {
				name = t.SessionID
			}
			user := t.UserName
			if user == "" {
				user = "-"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s | %s |\n",
				name, t.Project, t.Platform, user, t.Turns,
				formatTokenCount(t.TotalTokens), t.LastActive.Format("15:04"))
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(r.ByProject) > 0 || len(r.TopTools) > 0 {
		fmt.Fprintf(&b, "## 分组\n")
		if len(r.ByProject) > 0 {
			parts := make([]string, 0, len(r.ByProject))
			for _, p := range r.ByProject {
				parts = append(parts, fmt.Sprintf("%s %d轮/%s", p.Name, p.Turns, formatTokenCount(p.TotalTokens)))
			}
			fmt.Fprintf(&b, "- 项目: %s\n", strings.Join(parts, " · "))
		}
		if len(r.ByPlatform) > 0 {
			parts := make([]string, 0, len(r.ByPlatform))
			for _, p := range r.ByPlatform {
				parts = append(parts, fmt.Sprintf("%s %d轮", p.Name, p.Turns))
			}
			fmt.Fprintf(&b, "- 平台: %s\n", strings.Join(parts, " · "))
		}
		if len(r.TopTools) > 0 {
			parts := make([]string, 0, len(r.TopTools))
			for _, t := range r.TopTools {
				parts = append(parts, fmt.Sprintf("%s %d", t.Name, t.Calls))
			}
			fmt.Fprintf(&b, "- 工具 Top: %s\n", strings.Join(parts, " · "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// dashboardTemplateVarRe matches {{dashboard.today}}, {{dashboard.week:json}} etc.
var dashboardTemplateVarRe = regexp.MustCompile(`\{\{dashboard\.([a-z_]+)(:json)?\}\}`)

// resolveDashboardTemplateVars substitutes {{dashboard.*}} variables in a
// cron prompt with rendered reports (markdown by default, raw JSON with
// the :json suffix). Unrecognized variables are left as-is.
func (e *Engine) resolveDashboardTemplateVars(prompt string) string {
	if !strings.Contains(prompt, "{{dashboard.") {
		return prompt
	}
	now := time.Now()
	return dashboardTemplateVarRe.ReplaceAllStringFunc(prompt, func(m string) string {
		sub := dashboardTemplateVarRe.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		name, asJSON := sub[1], sub[2] == ":json"
		var period DashboardPeriod
		switch name {
		case "today":
			period = DayDashboardPeriod(now)
		case "yesterday":
			period = DayDashboardPeriod(now.AddDate(0, 0, -1))
		case "week":
			period = WeekDashboardPeriod(now)
		case "last_week":
			period = WeekDashboardPeriod(now.AddDate(0, 0, -7))
		default:
			return m // unrecognized: keep as-is (backward compatible)
		}
		report := e.buildEngineDashboardReport(period, 10)
		if report == nil {
			return "(dashboard statistics not collected)"
		}
		if asJSON {
			data, err := json.Marshal(report)
			if err != nil {
				return m
			}
			return string(data)
		}
		return RenderDashboardMarkdown(report)
	})
}

// buildEngineDashboardReport aggregates metrics for this engine's project.
// Returns nil when collection is disabled.
func (e *Engine) buildEngineDashboardReport(period DashboardPeriod, maxTopics int) *DashboardReport {
	if e.statsRecorder == nil {
		return nil
	}
	files := MetricsFilesBetween(e.statsRecorder.Dir(), period.Start, period.End)
	recs := ReadTurnRecords(files, period.Start, period.End)
	return AggregateDashboardReport(recs, period, DashboardAggregateOptions{Project: e.name, MaxTopics: maxTopics})
}
