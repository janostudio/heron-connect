package management

// mgmt_dashboard.go — dashboard REST API: usage statistics aggregation
// (/api/v1/dashboard*) and the business report index (/api/v1/reports).
//
// Presence-driven: the web project dashboard lights up zones based on what
// data exists. Statistics endpoints return 404 when the feature is disabled
// or collection is off; the web hides the zone on error.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// DashboardSettings carries everything the dashboard handlers need,
// injected once at startup from the [dashboard] config section.
type DashboardSettings struct {
	Enabled               bool
	Collect               bool
	MetricsDir            string
	MaxTopics             int
	IncludeMessageExcerpt bool
	InsightsPath          string
	HTMLPath              string
	ReportsDir            string
	PublicBaseURL         string
}

// SetDashboardSettings attaches dashboard configuration. nil disables all
// dashboard endpoints.
func (m *ManagementServer) SetDashboardSettings(s *DashboardSettings) {
	m.mu.Lock()
	m.dashboard = s
	m.mu.Unlock()
}

// dashboardSettings returns the current settings (nil = feature off).
func (m *ManagementServer) dashboardSettings() *DashboardSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dashboard
}

const maxDashboardCustomSpanDays = 90

// handleDashboard serves GET /api/v1/dashboard?period=&date=&start=&end=&project=
func (m *ManagementServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ds := m.requireDashboardCollect(w)
	if ds == nil {
		return
	}
	period, err := resolveDashboardPeriodParams(r)
	if err != nil {
		mgmtError(w, http.StatusBadRequest, err.Error())
		return
	}
	report := m.buildDashboardReport(ds, period, r.URL.Query().Get("project"))
	mgmtJSON(w, http.StatusOK, report)
}

// handleDashboardRoutes serves /api/v1/dashboard/{summary,settings,sessions/<project>/<id>}.
func (m *ManagementServer) handleDashboardRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/dashboard/")
	parts := strings.SplitN(rest, "/", 3)
	switch parts[0] {
	case "settings":
		m.handleDashboardSettings(w, r)
	case "summary":
		m.handleDashboardSummary(w, r)
	case "sessions":
		if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
			mgmtError(w, http.StatusBadRequest, "expected /api/v1/dashboard/sessions/<project>/<session_id>")
			return
		}
		m.handleDashboardSessionDetail(w, r, parts[1], parts[2])
	default:
		mgmtError(w, http.StatusNotFound, "unknown dashboard route")
	}
}

// handleDashboardSettings serves GET /api/v1/dashboard/settings — tells the
// web client where to probe business zones and whether stats exist.
func (m *ManagementServer) handleDashboardSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ds := m.requireDashboardEnabled(w)
	if ds == nil {
		return
	}
	mgmtJSON(w, http.StatusOK, map[string]any{
		"enabled":      true,
		"collect":      ds.Collect,
		"insights_path": ds.InsightsPath,
		"html_path":    ds.HTMLPath,
		"reports_dir":  ds.ReportsDir,
	})
}

// handleDashboardSummary serves GET /api/v1/dashboard/summary?project= —
// today + this week in one shot (Dashboard homepage strip).
func (m *ManagementServer) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ds := m.requireDashboardCollect(w)
	if ds == nil {
		return
	}
	now := time.Now()
	project := r.URL.Query().Get("project")
	mgmtJSON(w, http.StatusOK, map[string]any{
		"today": m.buildDashboardReport(ds, core.DayDashboardPeriod(now), project),
		"week":  m.buildDashboardReport(ds, core.WeekDashboardPeriod(now), project),
	})
}

// handleDashboardSessionDetail serves GET /api/v1/dashboard/sessions/<project>/<id>
// — turn-level detail for one session (dashboard drawer).
func (m *ManagementServer) handleDashboardSessionDetail(w http.ResponseWriter, r *http.Request, project, sessionID string) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ds := m.requireDashboardCollect(w)
	if ds == nil {
		return
	}
	m.mu.RLock()
	engine, ok := m.engines[project]
	m.mu.RUnlock()
	if !ok {
		mgmtError(w, http.StatusNotFound, "unknown project")
		return
	}

	start, end := time.Now().AddDate(0, 0, -7), time.Now()
	if s := r.URL.Query().Get("start"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
			start = t
		}
	}
	if e := r.URL.Query().Get("end"); e != "" {
		if t, err := time.ParseInLocation("2006-01-02", e, time.Local); err == nil {
			end = t.AddDate(0, 0, 1)
		}
	}

	files := core.MetricsFilesBetween(ds.MetricsDir, start, end)
	recs := core.ReadTurnRecords(files, start, end)
	turns := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		if rec.Kind != core.RecordKindTurn || rec.Project != project || rec.SessionID != sessionID {
			continue
		}
		turns = append(turns, map[string]any{
			"ts":                rec.TS,
			"trigger":           rec.Trigger,
			"duration_ms":       rec.DurationMs,
			"input_tokens":      rec.InputTokens,
			"output_tokens":     rec.OutputTokens,
			"cache_read_tokens": rec.CacheReadTokens,
			"cache_write_tokens": rec.CacheWriteTokens,
			"tokens_estimated":  rec.TokensEstimated,
			"tool_calls":        rec.ToolCalls,
			"tools":             rec.Tools,
			"error":             rec.Error,
		})
	}
	sessionName := ""
	if s := findSessionByID(engine, sessionID); s != nil {
		sessionName = s.Name
	}
	mgmtJSON(w, http.StatusOK, map[string]any{
		"session_id":   sessionID,
		"session_name": sessionName,
		"project":      project,
		"start":        start,
		"end":          end,
		"turns":        turns,
	})
}

// requireDashboardEnabled returns settings when the dashboard feature is on,
// otherwise writes a 404 and returns nil.
func (m *ManagementServer) requireDashboardEnabled(w http.ResponseWriter) *DashboardSettings {
	ds := m.dashboardSettings()
	if ds == nil || !ds.Enabled {
		mgmtError(w, http.StatusNotFound, "dashboard is disabled ([dashboard] enabled=false)")
		return nil
	}
	return ds
}

// requireDashboardCollect additionally requires engine-side collection.
func (m *ManagementServer) requireDashboardCollect(w http.ResponseWriter) *DashboardSettings {
	ds := m.requireDashboardEnabled(w)
	if ds == nil {
		return nil
	}
	if !ds.Collect {
		mgmtError(w, http.StatusNotFound, "dashboard collection is off ([dashboard] collect=false)")
		return nil
	}
	return ds
}

// buildDashboardReport aggregates metrics for the window, optionally scoped
// to one project, and enriches topics with first-message excerpts from
// engine session snapshots.
func (m *ManagementServer) buildDashboardReport(ds *DashboardSettings, period core.DashboardPeriod, project string) *core.DashboardReport {
	files := core.MetricsFilesBetween(ds.MetricsDir, period.Start, period.End)
	recs := core.ReadTurnRecords(files, period.Start, period.End)
	report := core.AggregateDashboardReport(recs, period, core.DashboardAggregateOptions{
		Project:   project,
		MaxTopics: ds.MaxTopics,
	})

	if ds.IncludeMessageExcerpt {
		for i := range report.Topics {
			t := &report.Topics[i]
			m.mu.RLock()
			engine, ok := m.engines[t.Project]
			m.mu.RUnlock()
			if !ok {
				continue
			}
			if s := findSessionByID(engine, t.SessionID); s != nil {
				t.FirstMessage = firstUserMessageExcerpt(s, 100)
			}
		}
	}
	return report
}

// resolveDashboardPeriodParams parses period/date/start/end query params.
func resolveDashboardPeriodParams(r *http.Request) (core.DashboardPeriod, error) {
	q := r.URL.Query()
	periodType := q.Get("period")
	if periodType == "" {
		periodType = "day"
	}
	now := time.Now()
	parseDate := func(key string, fallback time.Time) (time.Time, error) {
		v := q.Get(key)
		if v == "" {
			return fallback, nil
		}
		t, err := time.ParseInLocation("2006-01-02", v, time.Local)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid %s %q (want YYYY-MM-DD)", key, v)
		}
		return t, nil
	}

	switch periodType {
	case "day":
		date, err := parseDate("date", now)
		if err != nil {
			return core.DashboardPeriod{}, err
		}
		return core.DayDashboardPeriod(date), nil
	case "week":
		date, err := parseDate("date", now)
		if err != nil {
			return core.DashboardPeriod{}, err
		}
		return core.WeekDashboardPeriod(date), nil
	case "month":
		date, err := parseDate("date", now)
		if err != nil {
			return core.DashboardPeriod{}, err
		}
		return core.MonthDashboardPeriod(date), nil
	case "custom":
		start, err := parseDate("start", time.Time{})
		if err != nil {
			return core.DashboardPeriod{}, err
		}
		end, err := parseDate("end", time.Time{})
		if err != nil {
			return core.DashboardPeriod{}, err
		}
		if start.IsZero() || end.IsZero() {
			return core.DashboardPeriod{}, fmt.Errorf("custom period requires start and end")
		}
		end = end.AddDate(0, 0, 1)
		if !start.Before(end) {
			return core.DashboardPeriod{}, fmt.Errorf("start must be before end")
		}
		if end.Sub(start).Hours()/24 > maxDashboardCustomSpanDays {
			return core.DashboardPeriod{}, fmt.Errorf("custom span exceeds %d days", maxDashboardCustomSpanDays)
		}
		return core.CustomDashboardPeriod(start, end), nil
	default:
		return core.DashboardPeriod{}, fmt.Errorf("invalid period %q (want day|week|month|custom)", periodType)
	}
}

// findSessionByID locates an engine session by ID.
func findSessionByID(e *core.Engine, sessionID string) *core.SessionSnapshot {
	if e == nil {
		return nil
	}
	for _, s := range e.GetSessions().AllSessions() {
		if s.ID == sessionID {
			snap := s.Snapshot()
			return &snap
		}
	}
	return nil
}

// firstUserMessageExcerpt returns the first user history entry, truncated.
func firstUserMessageExcerpt(s *core.SessionSnapshot, maxRunes int) string {
	for _, h := range s.History {
		if h.Role != "user" {
			continue
		}
		content := strings.TrimSpace(h.Content)
		if content == "" {
			continue
		}
		runes := []rune(content)
		if len(runes) > maxRunes {
			return string(runes[:maxRunes]) + "…"
		}
		return content
	}
	return ""
}

// ──────────────────────────────────────────────────────────────
// Report index: GET /api/v1/reports?project=&type=&limit=
// ──────────────────────────────────────────────────────────────

type reportManifest struct {
	Title        string `json:"title"`
	Type         string `json:"type"`
	GeneratedBy  string `json:"generated_by"`
}

type reportEntry struct {
	Path    string `json:"path"` // relative to the project work dir
	Title   string `json:"title"`
	Type    string `json:"type,omitempty"`
	Format  string `json:"format"` // "html" | "md"
	Date    string `json:"date,omitempty"`
	Size    int64  `json:"size"`
	MTime   int64  `json:"mtime"`
	URL     string `json:"url"` // /api/v1/files/<project>/<path>
}

const (
	maxReportScanDepth = 3
	maxReportEntries   = 500
)

// handleReports serves the business report index for one project.
func (m *ManagementServer) handleReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ds := m.requireDashboardEnabled(w)
	if ds == nil {
		return
	}
	project := r.URL.Query().Get("project")
	if project == "" {
		mgmtError(w, http.StatusBadRequest, "project is required")
		return
	}
	m.mu.RLock()
	engine, engineOK := m.engines[project]
	workDir := m.projectWorkDirs[project]
	m.mu.RUnlock()
	if !engineOK || workDir == "" {
		mgmtError(w, http.StatusNotFound, "unknown project or no work dir")
		return
	}
	_ = engine

	filterType := r.URL.Query().Get("type")
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	root := filepath.Join(workDir, ds.ReportsDir)
	entries := scanReportFiles(root, root, 0)

	// The /api/v1/files/<project>/<path> endpoint resolves <path> relative to
	// the project work dir (not the reports dir), so the URL for a report must
	// carry the reports subdir prefix. Populate the otherwise-unused URL field
	// so the web client doesn't have to guess the base directory.
	reportsPrefix := strings.Trim(filepath.ToSlash(ds.ReportsDir), "/")

	out := make([]reportEntry, 0, len(entries))
	for _, e := range entries {
		if filterType != "" && e.Type != filterType {
			continue
		}
		if reportsPrefix != "" {
			e.URL = reportsPrefix + "/" + e.Path
		} else {
			e.URL = e.Path
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	mgmtJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"reports": out,
	})
}

// scanReportFiles walks the report archive collecting .html/.md files with
// optional sidecar manifests.
func scanReportFiles(root, dir string, depth int) []reportEntry {
	if depth > maxReportScanDepth {
		return nil
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []reportEntry
	for _, de := range dirEntries {
		full := filepath.Join(dir, de.Name())
		if de.IsDir() {
			out = append(out, scanReportFiles(root, full, depth+1)...)
			continue
		}
		ext := strings.ToLower(filepath.Ext(de.Name()))
		if ext != ".html" && ext != ".md" && ext != ".markdown" {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			continue
		}
		format := "md"
		if ext == ".html" {
			format = "html"
		}
		entry := reportEntry{
			Path:   filepath.ToSlash(rel),
			Title:  strings.TrimSuffix(de.Name(), filepath.Ext(de.Name())),
			Format: format,
			Size:   info.Size(),
			MTime:  info.ModTime().Unix(),
			Date:   reportDateFromPath(rel),
		}
		// Sidecar manifest: <slug>.manifest.json next to the report.
		manifestPath := strings.TrimSuffix(full, ext) + ".manifest.json"
		if data, err := os.ReadFile(manifestPath); err == nil {
			var mf reportManifest
			if json.Unmarshal(data, &mf) == nil {
				if mf.Title != "" {
					entry.Title = mf.Title
				}
				entry.Type = mf.Type
			}
		}
		out = append(out, entry)
		if len(out) >= maxReportEntries {
			break
		}
	}
	// Newest first.
	sort.Slice(out, func(i, j int) bool { return out[i].MTime > out[j].MTime })
	return out
}

// reportDateFromPath extracts a yyyy-mm-dd segment from a report path
// (accepts both 2026-08-31 and 20260831 dir names).
func reportDateFromPath(rel string) string {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if len(seg) == 10 && seg[4] == '-' && seg[7] == '-' {
			return seg
		}
		if len(seg) == 8 {
			if _, err := time.Parse("20060102", seg); err == nil {
				return seg[:4] + "-" + seg[4:6] + "-" + seg[6:]
			}
		}
	}
	return ""
}
