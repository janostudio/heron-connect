package wecom

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type wecomAccessLogger struct {
	mu      sync.Mutex
	path    string
	project string
}

type wecomAccessRecord struct {
	Project      string    `json:"project,omitempty"`
	Source       string    `json:"source"`
	PromptSentAt time.Time `json:"prompt_sent_at"`
	Allowed      bool      `json:"allowed"`
	UserID       string    `json:"user_id"`
	ChatID       string    `json:"chat_id,omitempty"`
	ChatType     string    `json:"chat_type,omitempty"`
	SessionKey   string    `json:"session_key"`
	MessageID    string    `json:"message_id,omitempty"`
	MsgType      string    `json:"msg_type,omitempty"`
	Reason       string    `json:"reason,omitempty"`
}

func newWecomAccessLogger(dataDir, project string) *wecomAccessLogger {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil
	}
	project = strings.TrimSpace(project)
	return &wecomAccessLogger{
		path: filepath.Join(dataDir, "audit", "wecom_access", sanitizeWecomAuditName(project)+".jsonl"),
		project: project,
	}
}

func sanitizeWecomAuditName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "default"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func (l *wecomAccessLogger) Log(rec wecomAccessRecord) {
	if l == nil {
		return
	}
	rec.Project = l.project
	rec.PromptSentAt = time.Now()

	line, err := json.Marshal(rec)
	if err != nil {
		slog.Warn("wecom: marshal access record failed", "error", err)
		return
	}
	line = append(line, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		slog.Warn("wecom: create access log dir failed", "path", l.path, "error", err)
		return
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Warn("wecom: open access log failed", "path", l.path, "error", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(line); err != nil {
		slog.Warn("wecom: write access log failed", "path", l.path, "error", err)
	}
}
