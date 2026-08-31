package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTurnRecorder_RecordAndRotate(t *testing.T) {
	dir := t.TempDir()
	r, err := NewTurnRecorder(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	day1 := time.Date(2026, 8, 30, 10, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 8, 31, 18, 0, 0, 0, time.Local)

	r.Record(TurnRecord{TS: day1, Kind: RecordKindTurn, Project: "p1", SessionID: "s1", InputTokens: 100})
	r.Record(TurnRecord{TS: day1, Kind: RecordKindSessionCreated, Project: "p1", SessionID: "s1"})
	r.Record(TurnRecord{TS: day2, Kind: RecordKindTurn, Project: "p2", SessionID: "s2", ToolCalls: 3, Tools: map[string]int{"Bash": 3}})

	metricsDir := filepath.Join(dir, "metrics")
	for name, want := range map[string]int{
		"turns-2026-08-30.jsonl": 2,
		"turns-2026-08-31.jsonl": 1,
	} {
		data, err := os.ReadFile(filepath.Join(metricsDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := 0
		for _, l := range splitLines(string(data)) {
			if l == "" {
				continue
			}
			var rec TurnRecord
			if err := json.Unmarshal([]byte(l), &rec); err != nil {
				t.Fatalf("%s: malformed line %q: %v", name, l, err)
			}
			lines++
		}
		if lines != want {
			t.Errorf("%s: got %d lines, want %d", name, lines, want)
		}
	}
}

func TestTurnRecorder_Prune(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	if err := os.MkdirAll(metricsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(metricsDir, "turns-2020-01-01.jsonl")
	recent := filepath.Join(metricsDir, "turns-2026-08-31.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recent, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := NewTurnRecorder(dir, 90); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("expired file not pruned")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent file pruned: %v", err)
	}
}

func TestMetricsFilesBetween(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"2026-08-29", "2026-08-30", "2026-08-31"} {
		p := filepath.Join(dir, "turns-"+d+".jsonl")
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Date(2026, 8, 30, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	files := MetricsFilesBetween(dir, start, end)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %v", len(files), files)
	}
	if filepath.Base(files[0]) != "turns-2026-08-30.jsonl" || filepath.Base(files[1]) != "turns-2026-08-31.jsonl" {
		t.Errorf("unexpected files: %v", files)
	}
}

func TestReadTurnRecords_WindowAndMalformed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "turns-2026-08-30.jsonl")
	lines := []string{
		`{"ts":"2026-08-30T09:00:00+08:00","kind":"turn","project":"p1","session_id":"s1","input_tokens":10}`,
		`not json`,
		`{"ts":"2026-08-30T23:59:59+08:00","kind":"turn","project":"p1","session_id":"s1","input_tokens":20}`,
		`{"ts":"2026-08-31T00:00:01+08:00","kind":"turn","project":"p1","session_id":"s1","input_tokens":30}`,
	}
	if err := os.WriteFile(p, []byte(joinNewlines(lines)), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 8, 30, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 8, 31, 0, 0, 0, 0, time.Local)
	recs := ReadTurnRecords([]string{p}, start, end)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (malformed skipped, out-of-window excluded)", len(recs))
	}
	if recs[0].InputTokens != 10 || recs[1].InputTokens != 20 {
		t.Errorf("unexpected records: %+v", recs)
	}
}

func TestSessionKeyHelpers(t *testing.T) {
	if got := sessionKeyPlatform("feishu:ou_x:oc_y"); got != "feishu" {
		t.Errorf("platform: got %q", got)
	}
	if got := sessionKeyUserID("feishu:ou_x:oc_y"); got != "ou_x" {
		t.Errorf("userID: got %q", got)
	}
	if got := sessionKeyPlatform("no-colon"); got != "" {
		t.Errorf("platform(no colon): got %q", got)
	}
	if got := sessionKeyUserID("only"); got != "" {
		t.Errorf("userID(short): got %q", got)
	}
}

func TestRecordTurnStats_TokenFallbackAndTrigger(t *testing.T) {
	dir := t.TempDir()
	r, err := NewTurnRecorder(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	e := &Engine{name: "proj-a", statsRecorder: r}
	session := &Session{ID: "s1"}
	session.SetAgentSessionID("cli-1", "claudecode")

	// SDK tokens plausible → used as-is, not estimated.
	e.recordTurnStats(statsTurnInput{
		session: session, sessionKey: "feishu:u1:c1", platformName: "feishu",
		agentName: "claudecode", userID: "u1", userName: "张三",
		turnStart: time.Now(), duration: 5 * time.Second,
		inputTokens: 12000, outputTokens: 3000, tokensPlausible: true,
		toolCalls: 2, tools: map[string]int{"Bash": 2},
	})

	// SDK tokens implausible → context estimate recorded as estimated input.
	e.recordTurnStats(statsTurnInput{
		session: session, sessionKey: "feishu:u1:c1", platformName: "feishu",
		agentName: "claudecode", userID: "cron", userName: "cron",
		turnStart: time.Now(), duration: time.Second,
		inputTokens: 0, tokensPlausible: false, contextEstimate: 8000,
	})

	files := MetricsFilesBetween(r.Dir(), time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	recs := ReadTurnRecords(files, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	first, second := recs[0], recs[1]
	if first.InputTokens != 12000 || first.TokensEstimated {
		t.Errorf("first record tokens: got in=%d estimated=%v", first.InputTokens, first.TokensEstimated)
	}
	if first.Trigger != "user" {
		t.Errorf("first trigger: got %q, want user", first.Trigger)
	}
	if second.InputTokens != 8000 || !second.TokensEstimated {
		t.Errorf("second record tokens: got in=%d estimated=%v (want 8000/true)", second.InputTokens, second.TokensEstimated)
	}
	if second.Trigger != "cron" {
		t.Errorf("second trigger: got %q, want cron", second.Trigger)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func joinNewlines(lines []string) string {
	res := ""
	for i, l := range lines {
		if i > 0 {
			res += "\n"
		}
		res += l
	}
	return res
}

func TestMetricsFilesBetween_EndDayInclusiveWhenTimesEqual(t *testing.T) {
	dir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	p := filepath.Join(dir, "turns-"+today+".jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Regression: start+7d == end to the nanosecond (both from time.Now() in
	// the same clock tick) must still include the end day's file.
	now := time.Now()
	start := now.AddDate(0, 0, -7)
	end := start.AddDate(0, 0, 7) // exactly == now's tick, nanos identical
	files := MetricsFilesBetween(dir, start, end)
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1 (end day must be inclusive): %v", len(files), files)
	}
}
