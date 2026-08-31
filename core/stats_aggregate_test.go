package core

import (
	"strings"
	"testing"
	"time"
)

func aggTestRecords() []TurnRecord {
	loc := time.Local
	mk := func(day int, hour int, kind string, project, session string, mutate func(*TurnRecord)) TurnRecord {
		rec := TurnRecord{
			TS: time.Date(2026, 8, day, hour, 0, 0, 0, loc), Kind: kind,
			Project: project, SessionKey: project + ":u1:c1", SessionID: session,
			Platform: "feishu", Agent: "claudecode", UserID: "u1", UserName: "张三",
		}
		if mutate != nil {
			mutate(&rec)
		}
		return rec
	}
	return []TurnRecord{
		// project-a: session s1 with 2 turns, session s2 with 1 turn (error)
		mk(30, 9, RecordKindSessionCreated, "proj-a", "s1", nil),
		mk(30, 9, RecordKindTurn, "proj-a", "s1", func(r *TurnRecord) {
			r.InputTokens, r.OutputTokens, r.DurationMs = 1000, 500, 60000
			r.ToolCalls, r.Tools = 2, map[string]int{"Bash": 1, "Read": 1}
			r.SessionName = "修复登录"
		}),
		mk(30, 10, RecordKindTurn, "proj-a", "s1", func(r *TurnRecord) {
			r.InputTokens, r.OutputTokens = 2000, 800
			r.Trigger = "cron"
			r.SessionName = "修复登录"
		}),
		mk(30, 11, RecordKindTurn, "proj-a", "s2", func(r *TurnRecord) {
			r.Error = "boom"
			r.InputTokens = 100
		}),
		// project-b: session s3
		mk(31, 15, RecordKindTurn, "proj-b", "s3", func(r *TurnRecord) {
			r.InputTokens, r.OutputTokens, r.ToolCalls = 500, 200, 3
			r.Tools = map[string]int{"Bash": 3}
			r.Platform = "web"
			r.UserName = "李四"
		}),
	}
}

func TestAggregateDashboardReport_TotalsAndBreakdowns(t *testing.T) {
	period := CustomDashboardPeriod(
		time.Date(2026, 8, 30, 0, 0, 0, 0, time.Local),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local),
	)
	r := AggregateDashboardReport(aggTestRecords(), period, DashboardAggregateOptions{MaxTopics: 10})

	if r.Version != 1 {
		t.Errorf("version = %d", r.Version)
	}
	if r.Totals.Turns != 4 || r.Totals.TurnsCron != 1 || r.Totals.TurnsUser != 3 {
		t.Errorf("turns: %+v", r.Totals)
	}
	if r.Totals.SessionsActive != 3 || r.Totals.SessionsNew != 1 {
		t.Errorf("sessions: active=%d new=%d", r.Totals.SessionsActive, r.Totals.SessionsNew)
	}
	if r.Totals.InputTokens != 3600 || r.Totals.OutputTokens != 1500 {
		t.Errorf("tokens: in=%d out=%d", r.Totals.InputTokens, r.Totals.OutputTokens)
	}
	if r.Totals.TotalTokens != 5100 {
		t.Errorf("total tokens = %d", r.Totals.TotalTokens)
	}
	if r.Totals.Errors != 1 || r.Totals.ToolCalls != 5 {
		t.Errorf("errors=%d toolCalls=%d", r.Totals.Errors, r.Totals.ToolCalls)
	}
	if len(r.ByProject) != 2 || r.ByProject[0].Name != "proj-a" {
		t.Errorf("by_project: %+v", r.ByProject)
	}
	if r.ByProject[0].Sessions != 2 || r.ByProject[1].Sessions != 1 {
		t.Errorf("by_project sessions: %+v", r.ByProject)
	}
	// custom period → daily buckets (2 days)
	if len(r.Timeline) != 2 {
		t.Fatalf("timeline buckets = %d, want 2", len(r.Timeline))
	}
	if r.Timeline[0].Turns != 3 || r.Timeline[1].Turns != 1 {
		t.Errorf("timeline turns: %+v", r.Timeline)
	}
	// topics sorted by turns: s1 (2) first
	if len(r.Topics) != 3 || r.Topics[0].SessionID != "s1" || r.Topics[0].Name != "修复登录" {
		t.Errorf("topics: %+v", r.Topics)
	}
	if r.Topics[0].TotalTokens != 4300 {
		t.Errorf("topic s1 tokens = %d", r.Topics[0].TotalTokens)
	}
	// top tools: Bash 4, Read 1
	if len(r.TopTools) != 2 || r.TopTools[0].Name != "Bash" || r.TopTools[0].Calls != 4 {
		t.Errorf("top tools: %+v", r.TopTools)
	}
}

func TestAggregateDashboardReport_ProjectFilter(t *testing.T) {
	period := CustomDashboardPeriod(
		time.Date(2026, 8, 30, 0, 0, 0, 0, time.Local),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local),
	)
	r := AggregateDashboardReport(aggTestRecords(), period, DashboardAggregateOptions{Project: "proj-b"})
	if r.Scope.Project != "proj-b" {
		t.Errorf("scope = %q", r.Scope.Project)
	}
	if r.Totals.Turns != 1 || r.Totals.InputTokens != 500 {
		t.Errorf("totals: %+v", r.Totals)
	}
	if len(r.Topics) != 1 || r.Topics[0].SessionID != "s3" {
		t.Errorf("topics: %+v", r.Topics)
	}
}

func TestAggregateDashboardReport_DayPeriodHourlyBuckets(t *testing.T) {
	period := DayDashboardPeriod(time.Date(2026, 8, 30, 12, 0, 0, 0, time.Local))
	r := AggregateDashboardReport(aggTestRecords(), period, DashboardAggregateOptions{})
	if len(r.Timeline) != 24 {
		t.Fatalf("timeline buckets = %d, want 24", len(r.Timeline))
	}
	if r.Timeline[9].Turns != 1 || r.Timeline[10].Turns != 1 || r.Timeline[11].Turns != 1 || r.Timeline[15].Turns != 0 {
		t.Errorf("hourly buckets wrong: 9h=%d 10h=%d 11h=%d 15h=%d",
			r.Timeline[9].Turns, r.Timeline[10].Turns, r.Timeline[11].Turns, r.Timeline[15].Turns)
	}
}

func TestWeekDashboardPeriod_MondayStart(t *testing.T) {
	// 2026-08-30 is a Sunday → week starts Monday 2026-08-24
	p := WeekDashboardPeriod(time.Date(2026, 8, 30, 12, 0, 0, 0, time.Local))
	want := "2026-08-24"
	if got := p.Start.Format("2006-01-02"); got != want {
		t.Errorf("week start = %s, want %s", got, want)
	}
	if !strings.Contains(p.Label, "-W") {
		t.Errorf("label = %q", p.Label)
	}
}

func TestRenderDashboardMarkdown(t *testing.T) {
	period := DayDashboardPeriod(time.Date(2026, 8, 30, 12, 0, 0, 0, time.Local))
	r := AggregateDashboardReport(aggTestRecords(), period, DashboardAggregateOptions{})
	md := RenderDashboardMarkdown(r)
	for _, want := range []string{
		"# 统计数据：今日 2026-08-30",
		"会话: 2（新会话 1）",
		"修复登录",
		"## 分组",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestResolveDashboardTemplateVars(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewTurnRecorder(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	now := time.Now()
	rec.Record(TurnRecord{
		TS: now, Kind: RecordKindTurn, Project: "proj-a", SessionID: "s1",
		InputTokens: 1000, OutputTokens: 500, SessionName: "demo",
	})

	e := &Engine{name: "proj-a", statsRecorder: rec}
	prompt := "今日日报：{{dashboard.today}} 未知变量 {{dashboard.nope}} 保留"
	out := e.resolveDashboardTemplateVars(prompt)

	if !strings.Contains(out, "今日日报：# 统计数据") {
		t.Errorf("today var not resolved:\n%s", out[:min(len(out), 200)])
	}
	if !strings.Contains(out, "{{dashboard.nope}}") {
		t.Errorf("unrecognized variable should stay as-is:\n%s", out)
	}
	if strings.Contains(out, "{{dashboard.today}}") {
		t.Errorf("today var left unresolved")
	}

	// JSON variant
	outJSON := e.resolveDashboardTemplateVars("data: {{dashboard.today:json}}")
	if !strings.Contains(outJSON, `"version":1`) {
		t.Errorf("json variant not resolved: %s", outJSON[:min(len(outJSON), 200)])
	}

	// Engine without recorder → placeholder note.
	e2 := &Engine{name: "proj-a"}
	out2 := e2.resolveDashboardTemplateVars("{{dashboard.today}}")
	if !strings.Contains(out2, "not collected") {
		t.Errorf("nil recorder should yield note, got: %q", out2)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
