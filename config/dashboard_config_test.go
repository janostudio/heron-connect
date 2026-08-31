package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDashboardConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

const dashboardBaseTOML = `
[[projects]]
name = "demo"

[projects.agent]
type = "claudecode"

[projects.agent.options]
mode = "default"

[[projects.platforms]]
type = "telegram"

[projects.platforms.options]
token = "test-token"
`

func TestLoad_DashboardDefaults(t *testing.T) {
	cfgPath := writeDashboardConfig(t, dashboardBaseTOML)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	d := cfg.Dashboard
	if !d.IsEnabled() {
		t.Errorf("IsEnabled() = false, want true (default)")
	}
	if !d.ShouldCollect() {
		t.Errorf("ShouldCollect() = false, want true (default)")
	}
	if got := d.GetRetentionDays(); got != 90 {
		t.Errorf("GetRetentionDays() = %d, want 90", got)
	}
	if !d.GetIncludeMessageExcerpt() {
		t.Errorf("GetIncludeMessageExcerpt() = false, want true (default)")
	}
	if got := d.GetMaxTopics(); got != 10 {
		t.Errorf("GetMaxTopics() = %d, want 10", got)
	}
	if got := d.GetInsightsPath(); got != "dashboards/insights.json" {
		t.Errorf("GetInsightsPath() = %q", got)
	}
	if got := d.GetHTMLPath(); got != "dashboards/index.html" {
		t.Errorf("GetHTMLPath() = %q", got)
	}
	if got := d.GetReportsDir(); got != "reports" {
		t.Errorf("GetReportsDir() = %q", got)
	}
}

func TestLoad_DashboardExplicit(t *testing.T) {
	body := dashboardBaseTOML + `
[dashboard]
enabled = false
collect = false
retention_days = 30
include_message_excerpt = false
max_topics = 5
insights_path = "custom/insights.json"
html_path = "custom/board.html"
reports_dir = "archive"
public_base_url = "https://dash.example.com"
`
	cfgPath := writeDashboardConfig(t, body)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	d := cfg.Dashboard
	if d.IsEnabled() {
		t.Errorf("IsEnabled() = true, want false")
	}
	if d.ShouldCollect() {
		t.Errorf("ShouldCollect() = true, want false")
	}
	if got := d.GetRetentionDays(); got != 30 {
		t.Errorf("GetRetentionDays() = %d, want 30", got)
	}
	if d.GetIncludeMessageExcerpt() {
		t.Errorf("GetIncludeMessageExcerpt() = true, want false")
	}
	if got := d.GetMaxTopics(); got != 5 {
		t.Errorf("GetMaxTopics() = %d, want 5", got)
	}
	if got := d.GetInsightsPath(); got != "custom/insights.json" {
		t.Errorf("GetInsightsPath() = %q", got)
	}
	if got := d.GetHTMLPath(); got != "custom/board.html" {
		t.Errorf("GetHTMLPath() = %q", got)
	}
	if got := d.GetReportsDir(); got != "archive" {
		t.Errorf("GetReportsDir() = %q", got)
	}
	if d.PublicBaseURL != "https://dash.example.com" {
		t.Errorf("PublicBaseURL = %q", d.PublicBaseURL)
	}
}
