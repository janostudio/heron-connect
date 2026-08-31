package core

// stats_recorder.go — turn-level usage metrics for the project dashboard.
//
// The recorder appends one JSON line per completed turn (and one per new
// session) to a per-day JSONL file under <data_dir>/metrics/. Records carry
// dimensions + counts only — never message content. The dashboard
// aggregation layer (stats_aggregate.go) reads these files to build
// time-windowed UsageReport values.
//
// Configuration: the [dashboard] config section. A nil recorder on an engine
// means collection is off ([dashboard] disabled or collect=false) and all
// Record calls are no-ops.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Turn record kinds.
const (
	RecordKindTurn           = "turn"
	RecordKindSessionCreated = "session_created"
)

// TurnRecord is one line in <data_dir>/metrics/turns-YYYY-MM-DD.jsonl.
type TurnRecord struct {
	TS               time.Time    `json:"ts"`
	Kind             string       `json:"kind"`
	Project          string       `json:"project"`
	SessionKey       string       `json:"session_key,omitempty"`
	SessionID        string       `json:"session_id,omitempty"`
	AgentSessionID   string       `json:"agent_session_id,omitempty"`
	SessionName      string       `json:"session_name,omitempty"`
	Platform         string       `json:"platform,omitempty"`
	Agent            string       `json:"agent,omitempty"`
	UserID           string       `json:"user_id,omitempty"`
	UserName         string       `json:"user_name,omitempty"`
	Trigger          string       `json:"trigger,omitempty"` // "user" | "cron"
	DurationMs       int64        `json:"duration_ms,omitempty"`
	InputTokens      int          `json:"input_tokens,omitempty"`
	OutputTokens     int          `json:"output_tokens,omitempty"`
	CacheReadTokens  int          `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int          `json:"cache_write_tokens,omitempty"`
	TokensEstimated  bool         `json:"tokens_estimated,omitempty"`
	ToolCalls        int          `json:"tool_calls,omitempty"`
	Tools            map[string]int `json:"tools,omitempty"`
	ResponseChars    int          `json:"response_chars,omitempty"`
	Silent           bool         `json:"silent,omitempty"`
	Error            string       `json:"error,omitempty"`
}

// TurnRecorder appends turn records to daily JSONL files. A single instance
// may be shared by all engines in the process (records carry the project
// dimension); writes are serialized by a mutex.
type TurnRecorder struct {
	mu            sync.Mutex
	dir           string
	retentionDays int
	day           string
	f             *os.File
}

// NewTurnRecorder creates the metrics dir under dataDir and prunes expired
// files (retentionDays <= 0 keeps everything).
func NewTurnRecorder(dataDir string, retentionDays int) (*TurnRecorder, error) {
	dir := filepath.Join(dataDir, "metrics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create metrics dir: %w", err)
	}
	r := &TurnRecorder{dir: dir, retentionDays: retentionDays}
	r.prune()
	return r, nil
}

// Dir returns the metrics directory.
func (r *TurnRecorder) Dir() string {
	if r == nil {
		return ""
	}
	return r.dir
}

// Record appends one record to the file of rec.TS's day. Failures are logged
// and never propagated — metrics must not affect the message flow.
func (r *TurnRecorder) Record(rec TurnRecord) {
	if r == nil || rec.Kind == "" {
		return
	}
	if rec.TS.IsZero() {
		rec.TS = time.Now()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	day := rec.TS.Format("2006-01-02")

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil || day != r.day {
		if err := r.rotateLocked(day); err != nil {
			slog.Warn("stats recorder: open metrics file failed", "error", err)
			return
		}
	}
	if _, err := r.f.Write(append(line, '\n')); err != nil {
		slog.Warn("stats recorder: append failed", "error", err)
	}
}

func (r *TurnRecorder) rotateLocked(day string) error {
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
	f, err := os.OpenFile(filepath.Join(r.dir, "turns-"+day+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	r.f = f
	r.day = day
	return nil
}

// Close closes the current file handle.
func (r *TurnRecorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
}

// prune deletes metrics files whose day is older than retentionDays.
func (r *TurnRecorder) prune() {
	if r == nil || r.retentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -r.retentionDays).Format("2006-01-02")
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasPrefix(name, "turns-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, "turns-"), ".jsonl")
		if len(day) == 10 && day < cutoff {
			if err := os.Remove(filepath.Join(r.dir, name)); err == nil {
				slog.Info("stats recorder: pruned expired metrics file", "file", name)
			}
		}
	}
}

// MetricsFilesBetween returns the metrics file paths whose calendar day
// falls in [start's day, end's day] (inclusive). Days are aligned to local
// calendar dates: a turn record at any hour of the end day lives in that
// day's file, so the end day must be included even when end's time-of-day
// equals start's (a strict d.Before(end) loop drops it when both derive
// from time.Now() in the same clock tick).
func MetricsFilesBetween(dir string, start, end time.Time) []string {
	if dir == "" || end.Before(start) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make(map[string]bool, len(entries))
	for _, de := range entries {
		if !de.IsDir() {
			names[de.Name()] = true
		}
	}
	var files []string
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	for d := day; !d.After(endDay); d = d.AddDate(0, 0, 1) {
		name := "turns-" + d.Format("2006-01-02") + ".jsonl"
		if names[name] {
			files = append(files, filepath.Join(dir, name))
		}
	}
	return files
}

// ReadTurnRecords parses records from files, keeping only those with
// start <= TS < end. Malformed lines are skipped silently.
func ReadTurnRecords(files []string, start, end time.Time) []TurnRecord {
	var recs []TurnRecord
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec TurnRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec.TS.Before(start) || !rec.TS.Before(end) {
				continue
			}
			recs = append(recs, rec)
		}
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].TS.Before(recs[j].TS) })
	return recs
}

// sessionKeyPlatform returns the leading platform segment of a session key
// (e.g. "feishu:ou_x:oc_y" → "feishu"). Empty when the key has no colon.
func sessionKeyPlatform(key string) string {
	if idx := strings.Index(key, ":"); idx > 0 {
		return key[:idx]
	}
	return ""
}

// sessionKeyUserID returns the second segment of a session key
// (e.g. "feishu:ou_x:oc_y" → "ou_x"). Empty when absent.
func sessionKeyUserID(key string) string {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// recordSessionCreated writes a session_created record. Called when a new
// interactive agent session spawns (not a resume).
func (e *Engine) recordSessionCreated(session *Session, sessions *SessionManager, sessionKey string) {
	if e.statsRecorder == nil || session == nil {
		return
	}
	userName := ""
	if um := sessions.GetUserMeta(sessionKey); um != nil {
		userName = um.UserName
	}
	e.statsRecorder.Record(TurnRecord{
		TS:         time.Now(),
		Kind:       RecordKindSessionCreated,
		Project:    e.name,
		SessionKey: sessionKey,
		SessionID:  session.ID,
		Platform:   sessionKeyPlatform(sessionKey),
		UserID:     sessionKeyUserID(sessionKey),
		UserName:   userName,
	})
}

// statsTurnInput carries the values collected at turn completion.
type statsTurnInput struct {
	session       *Session
	sessionKey    string
	platformName  string
	agentName     string
	userID        string
	userName      string
	msgID         string
	turnStart     time.Time
	duration      time.Duration
	inputTokens   int
	outputTokens  int
	tokensPlausible bool
	contextEstimate int
	toolCalls     int
	tools         map[string]int
	responseChars int
	silent        bool
	err           string
}

// recordTurnStats writes one turn record (success or error) and emits the
// turn.complete hook event. Token fallback: when the SDK-reported usage is
// not plausible, the context estimate is recorded as input tokens with
// tokens_estimated=true.
func (e *Engine) recordTurnStats(in statsTurnInput) {
	session := in.session
	if session == nil {
		return
	}
	inputTokens, outputTokens := in.inputTokens, in.outputTokens
	estimated := false
	if !in.tokensPlausible && in.contextEstimate > 0 {
		inputTokens = in.contextEstimate
		estimated = true
	}
	trigger := "user"
	if in.userID == "cron" {
		trigger = "cron"
	}
	rec := TurnRecord{
		TS:              in.turnStart,
		Kind:            RecordKindTurn,
		Project:         e.name,
		SessionKey:      in.sessionKey,
		SessionID:       session.ID,
		AgentSessionID:  session.GetAgentSessionID(),
		SessionName:     session.GetName(),
		Platform:        in.platformName,
		Agent:           in.agentName,
		UserID:          in.userID,
		UserName:        in.userName,
		Trigger:         trigger,
		DurationMs:      in.duration.Milliseconds(),
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		TokensEstimated: estimated,
		ToolCalls:       in.toolCalls,
		Tools:           in.tools,
		ResponseChars:   in.responseChars,
		Silent:          in.silent,
		Error:           in.err,
	}
	e.statsRecorder.Record(rec)

	platform := in.platformName
	if platform == "" {
		platform = sessionKeyPlatform(in.sessionKey)
	}
	e.hooks.Emit(HookEvent{
		Event:      HookEventTurnComplete,
		SessionKey: in.sessionKey,
		Platform:   platform,
		Content:    in.msgID,
		Extra: map[string]any{
			"session_id":    session.ID,
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"estimated":     estimated,
			"duration_ms":   in.duration.Milliseconds(),
			"tool_calls":    in.toolCalls,
			"silent":        in.silent,
			"error":         in.err,
		},
	})
}

// turnStatsFromState extracts the per-turn user identity captured at turn
// start (set right before processInteractiveEvents runs).
func (e *Engine) turnStatsFromState(state *interactiveState, sessions *SessionManager, sessionKey string) (userID, userName string) {
	if state != nil {
		state.mu.Lock()
		userID = state.turnUserID
		userName = state.turnUserName
		state.mu.Unlock()
	}
	if userName == "" && sessions != nil {
		if um := sessions.GetUserMeta(sessionKey); um != nil {
			userName = um.UserName
		}
	}
	return userID, userName
}
