package codebuddy

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

	"github.com/chenhg5/cc-connect/core"
)

// codebuddySession manages a multi-turn CodeBuddy Code conversation.
// Each Send() spawns `codebuddy -p <prompt> --output-format stream-json`.
// Subsequent turns use `--resume <sessionID>` to continue the conversation.
type codebuddySession struct {
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

func newCodeBuddySession(ctx context.Context, workDir, model, mode, resumeID string, extraEnv []string) (*codebuddySession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	cs := &codebuddySession{
		workDir:  workDir,
		model:    model,
		mode:     mode,
		extraEnv: extraEnv,
		events:   make(chan core.Event, 64),
		ctx:      sessionCtx,
		cancel:   cancel,
	}
	cs.alive.Store(true)

	if resumeID != "" && resumeID != core.ContinueSession {
		cs.sessionID.Store(resumeID)
	}

	return cs, nil
}

func (cs *codebuddySession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if len(images) > 0 {
		slog.Warn("codebuddySession: images not supported, ignoring")
	}
	if len(files) > 0 {
		filePaths := core.SaveFilesToDisk(cs.workDir, files)
		prompt = core.AppendFileRefs(prompt, filePaths)
	}
	if !cs.alive.Load() {
		return fmt.Errorf("session is closed")
	}

	args := []string{"-p", prompt, "--output-format", "stream-json"}

	sid := cs.CurrentSessionID()
	if sid != "" {
		args = append(args, "--resume", sid)
	}

	if cs.mode == "yolo" {
		args = append(args, "--dangerously-skip-permissions")
	}

	if cs.model != "" {
		args = append(args, "--model", cs.model)
	}

	slog.Debug("codebuddySession: launching", "resume", sid != "", "args_len", len(args))

	cmd := exec.CommandContext(cs.ctx, "codebuddy", args...)
	cmd.Dir = cs.workDir
	core.PrepareCmdForKill(cmd)
	if len(cs.extraEnv) > 0 {
		cmd.Env = core.MergeEnv(os.Environ(), cs.extraEnv)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codebuddySession: stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codebuddySession: start: %w", err)
	}
	cs.osCmd = cmd

	cs.wg.Add(1)
	go cs.readLoop(cmd, stdout, &stderrBuf)

	return nil
}

func (cs *codebuddySession) readLoop(cmd *exec.Cmd, stdout io.ReadCloser, stderrBuf *bytes.Buffer) {
	defer cs.wg.Done()

	var gotResult bool
	var nonJSONLines []string
	var pendingText string

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var raw streamEvent
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			slog.Debug("codebuddySession: non-JSON line", "line", truncStr(line, 100))
			nonJSONLines = append(nonJSONLines, line)
			continue
		}

		switch raw.Type {
		case "system":
			if raw.Subtype == "init" && raw.SessionID != "" {
				cs.sessionID.Store(raw.SessionID)
				slog.Debug("codebuddySession: init", "session_id", raw.SessionID)
			}

		case "assistant":
			pendingText = cs.handleAssistant(&raw, pendingText)

		case "user":
			// user messages contain tool results — emit as EventToolResult
			cs.handleUser(&raw)

		case "result":
			gotResult = true
			cs.handleResult(&raw, pendingText)
			pendingText = ""

		case "file-history-snapshot":
			// internal housekeeping — skip

		default:
			slog.Debug("codebuddySession: unhandled event type", "type", raw.Type)
		}
	}

	scanErr := scanner.Err()
	if scanErr != nil {
		slog.Error("codebuddySession: scanner error", "error", scanErr)
	}

	exitErr := cmd.Wait()

	if gotResult {
		if exitErr != nil {
			stderrMsg := strings.TrimSpace(stderrBuf.String())
			if stderrMsg != "" {
				slog.Warn("codebuddySession: process exited with error after result", "error", exitErr, "stderr", truncStr(stderrMsg, 200))
			}
		}
		return
	}

	// No result event — emit fallback
	if len(nonJSONLines) > 0 {
		slog.Warn("codebuddySession: no result event, falling back to plain-text output", "lines", len(nonJSONLines))
		text := strings.Join(nonJSONLines, "\n")
		evt := core.Event{Type: core.EventResult, Content: text, SessionID: cs.CurrentSessionID(), Done: true}
		select {
		case cs.events <- evt:
		case <-cs.ctx.Done():
		}
	} else if exitErr != nil {
		stderrMsg := strings.TrimSpace(stderrBuf.String())
		if stderrMsg == "" {
			stderrMsg = exitErr.Error()
		}
		slog.Error("codebuddySession: process failed with no result", "error", exitErr, "stderr", truncStr(stderrMsg, 200))
		evt := core.Event{Type: core.EventError, Error: fmt.Errorf("%s", stderrMsg)}
		select {
		case cs.events <- evt:
		case <-cs.ctx.Done():
		}
	} else if scanErr != nil {
		evt := core.Event{Type: core.EventError, Error: fmt.Errorf("read stdout: %w", scanErr)}
		select {
		case cs.events <- evt:
		case <-cs.ctx.Done():
		}
	} else {
		slog.Warn("codebuddySession: process exited with no output and no result event")
		evt := core.Event{Type: core.EventResult, Content: "", SessionID: cs.CurrentSessionID(), Done: true}
		select {
		case cs.events <- evt:
		case <-cs.ctx.Done():
		}
	}
}

// ── stream-json event structures ─────────────────────────────

type streamEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	UUID      string          `json:"uuid"`
	SessionID string          `json:"session_id"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	Message   *streamMessage  `json:"message"`
}

type streamMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
}

type contentItem struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	ToolUseID  string          `json:"tool_use_id"`
	Content    json.RawMessage `json:"content"`
}

// ── event handling ───────────────────────────────────────────

// handleAssistant processes an assistant message event.
// For complete messages (stop_reason present), emits EventText/EventToolUse.
// Returns any pending text from delta fragments (not used by codebuddy but kept for consistency).
func (cs *codebuddySession) handleAssistant(ev *streamEvent, pendingText string) string {
	if ev.Message == nil {
		return pendingText
	}

	// Only emit on complete messages (stop_reason set) to avoid partial output
	if ev.Message.StopReason == "" {
		return pendingText
	}

	var items []contentItem
	if err := json.Unmarshal(ev.Message.Content, &items); err != nil {
		return pendingText
	}

	for _, item := range items {
		switch item.Type {
		case "text":
			if item.Text != "" {
				evt := core.Event{Type: core.EventText, Content: item.Text}
				select {
				case cs.events <- evt:
				case <-cs.ctx.Done():
					return ""
				}
			}

		case "tool_use":
			inputPreview := extractToolPreview(item.Input)
			evt := core.Event{Type: core.EventToolUse, ToolName: item.Name, ToolInput: inputPreview}
			select {
			case cs.events <- evt:
			case <-cs.ctx.Done():
				return ""
			}
		}
	}

	return ""
}

// handleUser processes a user message event (tool results from the CLI).
func (cs *codebuddySession) handleUser(ev *streamEvent) {
	if ev.Message == nil {
		return
	}

	var items []contentItem
	if err := json.Unmarshal(ev.Message.Content, &items); err != nil {
		return
	}

	for _, item := range items {
		if item.Type != "tool_result" {
			continue
		}

		// Extract the tool result text from nested content
		resultText := extractToolResultText(item.Content)
		evt := core.Event{
			Type:      core.EventToolResult,
			ToolName:  item.ToolUseID,
			Content:   resultText,
			SessionID: cs.CurrentSessionID(),
		}
		select {
		case cs.events <- evt:
		case <-cs.ctx.Done():
			return
		}
	}
}

// handleResult processes the final result event.
func (cs *codebuddySession) handleResult(ev *streamEvent, pendingText string) {
	finalText := ev.Result
	if finalText == "" && pendingText != "" {
		finalText = pendingText
	}

	evt := core.Event{
		Type:      core.EventResult,
		Content:   finalText,
		SessionID: cs.CurrentSessionID(),
		Done:      true,
	}
	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
	}
}

func (cs *codebuddySession) RespondPermission(_ string, _ core.PermissionResult) error {
	return nil
}

func (cs *codebuddySession) Events() <-chan core.Event {
	return cs.events
}

func (cs *codebuddySession) CurrentSessionID() string {
	v, _ := cs.sessionID.Load().(string)
	return v
}

func (cs *codebuddySession) Alive() bool {
	return cs.alive.Load()
}

func (cs *codebuddySession) Close() error {
	cs.alive.Store(false)
	cs.cancel()
	done := make(chan struct{})
	go func() {
		cs.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		_ = core.ForceKillProcessGroup(cs.osCmd)
	}
	close(cs.events)
	return nil
}

func (cs *codebuddySession) CancelTurn() {
	slog.Debug("codebuddySession: CancelTurn not supported")
}

// ── helpers ──────────────────────────────────────────────────

// extractToolPreview parses the JSON input of a tool call and returns a short preview string.
func extractToolPreview(inputJSON json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(inputJSON, &m); err != nil {
		return string(inputJSON)
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
	return string(inputJSON)
}

// extractToolResultText extracts readable text from a tool_result content array.
func extractToolResultText(contentJSON json.RawMessage) string {
	var items []contentItem
	if err := json.Unmarshal(contentJSON, &items); err != nil {
		return ""
	}
	var parts []string
	for _, item := range items {
		if item.Type == "text" && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func truncStr(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "..."
}
