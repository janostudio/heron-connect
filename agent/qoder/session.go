package qoder

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/janostudio/heron-connect/core"
)

// qoderSession manages a multi-turn Qoder conversation.
// Each Send() spawns `qodercli -p <prompt> -f stream-json -q`.
// Subsequent turns use `-r <sessionID>` to resume the conversation.
type qoderSession struct {
	workDir   string
	model     string
	mode      string
	extraEnv  []string
	events    chan core.Event
	sessionID atomic.Value // stores string
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	alive     atomic.Bool
	osCmd     *exec.Cmd // for force-kill on Close timeout
}

func newQoderSession(ctx context.Context, workDir, model, mode, resumeID string, extraEnv []string) (*qoderSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	qs := &qoderSession{
		workDir:  workDir,
		model:    model,
		mode:     mode,
		extraEnv: extraEnv,
		events:   make(chan core.Event, 64),
		ctx:      sessionCtx,
		cancel:   cancel,
	}
	qs.alive.Store(true)

	if resumeID != "" && resumeID != core.ContinueSession {
		qs.sessionID.Store(resumeID)
	}

	return qs, nil
}

func (qs *qoderSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if len(images) > 0 {
		slog.Warn("qoderSession: images not supported, ignoring")
	}
	if len(files) > 0 {
		filePaths := core.SaveFilesToDisk(qs.workDir, files)
		prompt = core.AppendFileRefs(prompt, filePaths)
	}
	if !qs.alive.Load() {
		return fmt.Errorf("session is closed")
	}

	args := []string{"-p", prompt, "-f", "stream-json", "-q", "-w", qs.workDir}

	sid := qs.CurrentSessionID()
	if sid != "" {
		args = append(args, "-r", sid)
	}

	if qs.mode == "yolo" {
		args = append(args, "--dangerously-skip-permissions")
	}

	if qs.model != "" {
		args = append(args, "--model", qs.model)
	}

	slog.Debug("qoderSession: launching", "resume", sid != "", "args_len", len(args))

	cmd := exec.CommandContext(qs.ctx, "qodercli", args...)
	cmd.Dir = qs.workDir
	core.PrepareCmdForKill(cmd)
	if len(qs.extraEnv) > 0 {
		cmd.Env = core.MergeEnv(os.Environ(), qs.extraEnv)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("qoderSession: stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("qoderSession: start: %w", err)
	}
	qs.osCmd = cmd

	qs.wg.Add(1)
	go qs.readLoop(cmd, stdout, &stderrBuf)

	return nil
}

func (qs *qoderSession) readLoop(cmd *exec.Cmd, stdout io.ReadCloser, stderrBuf *bytes.Buffer) {
	defer qs.wg.Done()

	var gotResult bool
	var nonJSONLines []string
	// sawSessionID guards against subagent session-id poisoning: only the
	// FIRST event carrying a session id belongs to the top-level conversation.
	// Subagent (child) sessions emit events with their own ids mid-conversation;
	// accepting those would overwrite the top-level id, the engine would
	// persist the child id, and the next --resume would target a child session
	// with no backing store → silent zero-output exit.
	var sawSessionID bool

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var raw streamEvent
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			slog.Debug("qoderSession: non-JSON line", "line", truncStr(line, 100))
			nonJSONLines = append(nonJSONLines, line)
			continue
		}

		if raw.SessionID != "" {
			if !sawSessionID {
				sawSessionID = true
				qs.sessionID.Store(raw.SessionID)
			} else if raw.SessionID != qs.CurrentSessionID() {
				slog.Debug("qoderSession: ignoring non-primary session id (subagent)", "session_id", raw.SessionID)
			}
		}
		if qs.handleEvent(&raw) {
			gotResult = true
		}
	}

	scanErr := scanner.Err()
	if scanErr != nil {
		slog.Error("qoderSession: scanner error", "error", scanErr)
	}

	// Wait for process to exit.
	exitErr := cmd.Wait()

	// If we already got a result event, the turn completed normally.
	if gotResult {
		if exitErr != nil {
			stderrMsg := strings.TrimSpace(stderrBuf.String())
			if stderrMsg != "" {
				slog.Warn("qoderSession: process exited with error after result", "error", exitErr, "stderr", truncStr(stderrMsg, 200))
			}
		}
		return
	}

	// No result event was received — emit a fallback to prevent the engine
	// from hanging forever on the events channel.
	stderrMsg := strings.TrimSpace(stderrBuf.String())
	evt := qs.exitFallbackEvent(nonJSONLines, exitErr, scanErr, stderrMsg)
	if evt.Type == core.EventError {
		// Error fallback means the turn is dead. When the exit was silent
		// (zero output) the tracked session id itself is suspect — mark the
		// session dead so the engine tears down the interactive state and,
		// for unrecoverable bindings, detaches the persisted agent_session_id
		// (next message then starts a fresh agent session).
		qs.alive.Store(false)
	}
	select {
	case qs.events <- evt:
	case <-qs.ctx.Done():
	}
}

// exitFallbackEvent builds the terminal event when the CLI process exits
// without a result event. A silent clean exit (exit 0, nothing on stdout or
// stderr) previously surfaced as an empty result — the user just saw
// "(空响应)" while the CLI had actually failed; it is now an explicit error
// so the turn visibly fails and the log carries whatever the CLI wrote to
// stderr.
func (qs *qoderSession) exitFallbackEvent(nonJSONLines []string, exitErr, scanErr error, stderrMsg string) core.Event {
	switch {
	case len(nonJSONLines) > 0:
		// qodercli produced plain text instead of stream-json; forward it
		// as a result so the user at least sees the response.
		slog.Warn("qoderSession: no result event, falling back to plain-text output", "lines", len(nonJSONLines))
		return core.Event{Type: core.EventResult, Content: strings.Join(nonJSONLines, "\n"), SessionID: qs.CurrentSessionID(), Done: true}
	case exitErr != nil:
		// Process failed with no usable output.
		msg := stderrMsg
		if msg == "" {
			msg = exitErr.Error()
		}
		slog.Error("qoderSession: process failed with no result", "error", exitErr, "stderr", truncStr(msg, 200))
		return core.Event{Type: core.EventError, Error: fmt.Errorf("%s", msg)}
	case scanErr != nil:
		// Scanner error with no output.
		return core.Event{Type: core.EventError, Error: fmt.Errorf("read stdout: %w", scanErr)}
	default:
		// Process exited cleanly but produced nothing at all. stderr may hold
		// the real reason — log it and surface it; never silently swallow a
		// zero-output turn. Marked unrecoverable: the persisted
		// agent_session_id cannot be resumed (no backing session), so the
		// engine detaches it for a fresh start.
		slog.Warn("qoderSession: process exited with no output and no result event", "stderr", truncStr(stderrMsg, 200))
		if stderrMsg != "" {
			return core.Event{Type: core.EventError, Error: fmt.Errorf("%s", stderrMsg), Metadata: map[string]any{core.EventMetadataSessionUnrecoverable: true}}
		}
		return core.Event{Type: core.EventError, Error: fmt.Errorf("process exited with no output and no result event"), Metadata: map[string]any{core.EventMetadataSessionUnrecoverable: true}}
	}
}

// ── stream-json event structures ─────────────────────────────

type streamEvent struct {
	Type      string         `json:"type"`
	Subtype   string         `json:"subtype"`
	SessionID string         `json:"session_id"`
	Done      bool           `json:"done"`
	Message   *streamMessage `json:"message"`
	Result    string         `json:"result"` // qodercli 0.2.x: final text in top-level result field
}

type streamMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Status     string          `json:"status"`
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
}

type contentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Name     string `json:"name"`
	Input    string `json:"input"`
	Reason   string `json:"reason"`
	Content  string `json:"content"`
	Finished bool   `json:"finished"`
}

// ── event handling ───────────────────────────────────────────

func (qs *qoderSession) handleEvent(ev *streamEvent) bool {
	switch ev.Type {
	case "system":
		slog.Debug("qoderSession: init", "session_id", ev.SessionID)

	case "assistant":
		qs.handleAssistant(ev)

	case "result":
		return qs.handleResult(ev)
	}
	return false
}

func (qs *qoderSession) handleAssistant(ev *streamEvent) {
	if ev.Message == nil {
		return
	}

	// qodercli <0.2: uses Status="finished" to indicate final message
	// qodercli 0.2.x: Status is empty/null, uses StopReason="end_turn"/"tool_use"
	isFinished := ev.Message.Status == "finished" ||
		ev.Message.StopReason == "end_turn" ||
		ev.Message.StopReason == "tool_use"
	if !isFinished {
		return
	}

	var items []contentItem
	if err := json.Unmarshal(ev.Message.Content, &items); err != nil {
		return
	}

	for _, item := range items {
		switch item.Type {
		case "text":
			if item.Text != "" {
				evt := core.Event{Type: core.EventText, Content: item.Text}
				select {
				case qs.events <- evt:
				case <-qs.ctx.Done():
					return
				}
			}

		case "function":
			inputPreview := extractToolPreview(item.Input)
			evt := core.Event{Type: core.EventToolUse, ToolName: item.Name, ToolInput: inputPreview}
			select {
			case qs.events <- evt:
			case <-qs.ctx.Done():
				return
			}
		}
	}
}

func (qs *qoderSession) handleResult(ev *streamEvent) bool {
	var finalText string

	// qodercli <0.2: result text is in message.content[].text
	if ev.Message != nil {
		var items []contentItem
		if err := json.Unmarshal(ev.Message.Content, &items); err == nil {
			for _, item := range items {
				if item.Type == "text" && item.Text != "" {
					finalText = item.Text
				}
			}
		}
	}

	// qodercli 0.2.x: result text is in top-level "result" field
	if finalText == "" && ev.Result != "" {
		finalText = ev.Result
	}

	// Empty result (model/API returned nothing) must not surface as a
	// successful empty reply. Return false so readLoop does NOT set gotResult
	// and instead emits its exitFallbackEvent (which surfaces stderr as an
	// explicit EventError).
	if strings.TrimSpace(finalText) == "" {
		slog.Warn("qoderSession: result event with empty content", "session_id", qs.CurrentSessionID())
		return false
	}

	evt := core.Event{Type: core.EventResult, Content: finalText, SessionID: qs.CurrentSessionID(), Done: true}
	select {
	case qs.events <- evt:
	case <-qs.ctx.Done():
	}
	return true
}

func (qs *qoderSession) RespondPermission(_ string, _ core.PermissionResult) error {
	return nil
}

func (qs *qoderSession) Events() <-chan core.Event {
	return qs.events
}

func (qs *qoderSession) CurrentSessionID() string {
	v, _ := qs.sessionID.Load().(string)
	return v
}

func (qs *qoderSession) Alive() bool {
	return qs.alive.Load()
}

func (qs *qoderSession) Close() error {
	qs.alive.Store(false)
	qs.cancel()
	done := make(chan struct{})
	go func() {
		qs.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		_ = core.ForceKillProcessGroup(qs.osCmd)
	}
	close(qs.events)
	return nil
}

func (qs *qoderSession) CancelTurn() {
	slog.Debug("agent: CancelTurn not supported for this session type")
}

// ── helpers ──────────────────────────────────────────────────

// extractToolPreview parses the JSON input of a tool call and returns a short preview string.
func extractToolPreview(inputJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &m); err != nil {
		return inputJSON
	}
	if cmd, ok := m["command"].(string); ok {
		return cmd
	}
	if file, ok := m["file_path"].(string); ok {
		return file
	}
	if pattern, ok := m["pattern"].(string); ok {
		return pattern
	}
	if query, ok := m["query"].(string); ok {
		return query
	}
	return inputJSON
}

func truncStr(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "..."
}
