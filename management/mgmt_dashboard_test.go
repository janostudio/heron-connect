package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/janostudio/heron-connect/core"
)

func newDashboardTestServer(t *testing.T, settings *DashboardSettings, workDir string) (*ManagementServer, *httptest.Server) {
	t.Helper()
	mgmt := NewManagementServer(0, "", nil)
	mgmt.RegisterEngine("p", core.NewEngine("p", &stubAgent{}, nil, "", core.LangEnglish))
	mgmt.RegisterProjectWorkDir("p", workDir)
	if settings != nil {
		mgmt.SetDashboardSettings(settings)
	}
	ts := httptest.NewServer(mgmt.buildHandler(http.NewServeMux()))
	t.Cleanup(ts.Close)
	return mgmt, ts
}

func writeMetrics(t *testing.T, metricsDir string) {
	t.Helper()
	if err := os.MkdirAll(metricsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	day := time.Now().Format("2006-01-02")
	line1 := `{"ts":"` + time.Now().Add(-time.Hour).Format(time.RFC3339) + `","kind":"turn","project":"p","session_id":"s1","session_name":"demo","platform":"feishu","agent":"claudecode","user_id":"u1","user_name":"张三","input_tokens":1000,"output_tokens":500,"tool_calls":2,"duration_ms":60000}`
	line2 := `{"ts":"` + time.Now().Format(time.RFC3339) + `","kind":"turn","project":"p","session_id":"s1","input_tokens":200,"output_tokens":100,"trigger":"cron"}`
	p := filepath.Join(metricsDir, "turns-"+day+".jsonl")
	if err := os.WriteFile(p, []byte(line1+"\n"+line2+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp.StatusCode, body
}

func TestDashboardDisabled_Returns404(t *testing.T) {
	_, ts := newDashboardTestServer(t, nil, t.TempDir())
	status, body := getJSON(t, ts.URL+"/api/v1/dashboard?period=day")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if body["ok"] != false {
		t.Errorf("body = %v", body)
	}
}

func TestDashboardCollectOff_Returns404(t *testing.T) {
	_, ts := newDashboardTestServer(t, &DashboardSettings{
		Enabled: true, Collect: false, InsightsPath: "dashboards/insights.json",
	}, t.TempDir())
	status, _ := getJSON(t, ts.URL+"/api/v1/dashboard?period=day")
	if status != http.StatusNotFound {
		t.Fatalf("stats endpoint status = %d, want 404 (collect off)", status)
	}
	// Settings endpoint still works in display-only mode.
	status, body := getJSON(t, ts.URL+"/api/v1/dashboard/settings")
	if status != http.StatusOK {
		t.Fatalf("settings status = %d", status)
	}
	data := body["data"].(map[string]any)
	if data["collect"] != false {
		t.Errorf("settings.collect = %v", data["collect"])
	}
}

func TestDashboardReportAggregates(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	writeMetrics(t, metricsDir)
	_, ts := newDashboardTestServer(t, &DashboardSettings{
		Enabled: true, Collect: true, MetricsDir: metricsDir, MaxTopics: 10,
		IncludeMessageExcerpt: true, InsightsPath: "dashboards/insights.json", ReportsDir: "reports",
	}, dir)

	status, body := getJSON(t, ts.URL+"/api/v1/dashboard?period=day")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	data := body["data"].(map[string]any)
	totals := data["totals"].(map[string]any)
	if totals["turns"] != float64(2) {
		t.Errorf("turns = %v", totals["turns"])
	}
	if totals["turns_cron"] != float64(1) {
		t.Errorf("turns_cron = %v", totals["turns_cron"])
	}
	if totals["input_tokens"] != float64(1200) {
		t.Errorf("input_tokens = %v", totals["input_tokens"])
	}
	if totals["sessions_active"] != float64(1) {
		t.Errorf("sessions_active = %v", totals["sessions_active"])
	}
	topics := data["topics"].([]any)
	if len(topics) != 1 {
		t.Fatalf("topics = %v", topics)
	}
	topic := topics[0].(map[string]any)
	if topic["name"] != "demo" {
		t.Errorf("topic name = %v", topic["name"])
	}
}

func TestDashboardSummary(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	writeMetrics(t, metricsDir)
	_, ts := newDashboardTestServer(t, &DashboardSettings{
		Enabled: true, Collect: true, MetricsDir: metricsDir,
	}, dir)

	status, body := getJSON(t, ts.URL+"/api/v1/dashboard/summary")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	data := body["data"].(map[string]any)
	if _, ok := data["today"]; !ok {
		t.Error("summary missing today")
	}
	if _, ok := data["week"]; !ok {
		t.Error("summary missing week")
	}
}

func TestDashboardSessionDetail(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	writeMetrics(t, metricsDir)
	_, ts := newDashboardTestServer(t, &DashboardSettings{
		Enabled: true, Collect: true, MetricsDir: metricsDir,
	}, dir)

	status, body := getJSON(t, ts.URL+"/api/v1/dashboard/sessions/p/s1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	data := body["data"].(map[string]any)
	turns := data["turns"].([]any)
	if len(turns) != 2 {
		t.Fatalf("turns = %v", turns)
	}
}

func TestReportsIndexWithManifest(t *testing.T) {
	dir := t.TempDir()
	reportDir := filepath.Join(dir, "reports", "2026-08-30")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "token-day.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "token-day.manifest.json"), []byte(`{"title":"Token 消耗日报","type":"token"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "daily-summary.md"), []byte("# summary"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, ts := newDashboardTestServer(t, &DashboardSettings{
		Enabled: true, Collect: true, ReportsDir: "reports",
	}, dir)

	status, body := getJSON(t, ts.URL+"/api/v1/reports?project=p")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	data := body["data"].(map[string]any)
	reports := data["reports"].([]any)
	if len(reports) != 2 {
		t.Fatalf("reports = %v", reports)
	}
	first := reports[0].(map[string]any)
	_ = first
	// Newest first; both files created in the same second — order by name is
	// not guaranteed, so just assert set membership via a map.
	titles := map[string]bool{}
	for _, r := range reports {
		re := r.(map[string]any)
		titles[re["title"].(string)] = true
		if re["date"] != "2026-08-30" {
			t.Errorf("date = %v, want 2026-08-30", re["date"])
		}
	}
	if !titles["Token 消耗日报"] || !titles["daily-summary"] {
		t.Errorf("titles = %v", titles)
	}

	// type filter
	status, body = getJSON(t, ts.URL+"/api/v1/reports?project=p&type=token")
	if status != http.StatusOK {
		t.Fatalf("filter status = %d", status)
	}
	reports = body["data"].(map[string]any)["reports"].([]any)
	if len(reports) != 1 || reports[0].(map[string]any)["title"] != "Token 消耗日报" {
		t.Errorf("filtered reports = %v", reports)
	}
}
