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

	"github.com/janostudio/heron-connect/core"
)

// codebuddySession manages a multi-turn CodeBuddy Code conversation.
// Each Send() spawns `codebuddy -p --output-format stream-json ... -- <prompt>`.
// Subsequent turns use `--resume <sessionID>` to continue the conversation.
type codebuddySession struct {
	workDir   string
	model     string
	mode      string
	extraArgs []string
	extraEnv  []string
	events    chan core.Event
	sessionID atomic.Value // stores string
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	alive     atomic.Bool
	osCmd     *exec.Cmd // for force-kill on Close timeout
	// toolNameByID caches tool_use_id → readable tool name across the
	// assistant→user turn boundary. The CLI's user-message stream emits
	// tool_result blocks with only tool_use_id (no name), so the adapter
	// must remember the name emitted in the prior assistant tool_use block.
	// Uses sync.Map so handleAssistant (write) and handleUser (read) can
	// run concurrently if events arrive out-of-order on different goroutines.
	toolNameByID sync.Map // string → string
}

// launchArgs builds the codebuddy CLI argument list. The prompt is passed as
// the positional argument after the "--" end-of-options marker: the CLI
// rejects tokens starting with "-" as unknown options, so without the marker
// any prompt beginning with "-" (e.g. custom command files whose YAML
// frontmatter starts with "---") fails with "error: unknown option".
func launchArgs(prompt, sid, mode, model string, extraArgs []string) []string {
	args := []string{"-p", "--output-format", "stream-json"}

	if sid != "" {
		args = append(args, "--resume", sid)
	}

	if mode == "yolo" {
		args = append(args, "--dangerously-skip-permissions")
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	// Extra args from config are appended before the end-of-options marker
	// so they are treated as codebuddy CLI options, not prompt text.
	args = append(args, extraArgs...)

	return append(args, "--", prompt)
}

func newCodeBuddySession(ctx context.Context, workDir, model, mode, resumeID string, extraArgs, extraEnv []string) (*codebuddySession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	cs := &codebuddySession{
		workDir:   workDir,
		model:     model,
		mode:      mode,
		extraArgs: extraArgs,
		extraEnv:  extraEnv,
		events:    make(chan core.Event, 64),
		ctx:       sessionCtx,
		cancel:    cancel,
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

	sid := cs.CurrentSessionID()
	args := launchArgs(prompt, sid, cs.mode, cs.model, cs.extraArgs)

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

// shouldTrackInitSessionID reports whether a system/init event's session id
// may update the tracked top-level session id: only the FIRST init of a
// process run qualifies. Subagent (child) sessions also emit init events with
// their own ids mid-conversation; accepting those would overwrite the
// top-level id, the engine would persist the child id as the session's
// agent_session_id, and the next --resume would target a child session that
// has no .jsonl on disk → silent zero-output exit.
func shouldTrackInitSessionID(sawInit bool, subtype, sessionID string) bool {
	return !sawInit && subtype == "init" && sessionID != ""
}

func (cs *codebuddySession) readLoop(cmd *exec.Cmd, stdout io.ReadCloser, stderrBuf *bytes.Buffer) {
	defer cs.wg.Done()

	var gotResult bool
	var nonJSONLines []string
	var pendingText string
	// sawInit: only the first system/init of a process run belongs to the
	// top-level conversation — see shouldTrackInitSessionID.
	var sawInit bool

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
				if shouldTrackInitSessionID(sawInit, raw.Subtype, raw.SessionID) {
					sawInit = true
					cs.sessionID.Store(raw.SessionID)
					slog.Debug("codebuddySession: init", "session_id", raw.SessionID)
				} else {
					slog.Debug("codebuddySession: ignoring non-primary init (subagent)", "session_id", raw.SessionID)
				}
			}

		case "assistant":
			pendingText = cs.handleAssistant(&raw, pendingText)

		case "user":
			// user messages contain tool results — emit as EventToolResult
			cs.handleUser(&raw)

		case "result":
			if cs.handleResult(&raw, pendingText) {
				gotResult = true
			}
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
	stderrMsg := strings.TrimSpace(stderrBuf.String())
	evt := exitFallbackEvent(nonJSONLines, exitErr, scanErr, stderrMsg, cs.CurrentSessionID())
	if evt.Type == core.EventError {
		// Error fallback means the turn is dead. When the exit was silent
		// (zero output) the tracked session id itself is suspect — typically
		// --resume targeted a session with no backing store. Mark the
		// session dead so the engine tears down the interactive state and,
		// for unrecoverable bindings, detaches the persisted agent_session_id
		// (next message then starts a fresh agent session).
		cs.alive.Store(false)
	}
	select {
	case cs.events <- evt:
	case <-cs.ctx.Done():
	}
}

// exitFallbackEvent builds the terminal event when the CLI process exits
// without a result event. Priority: plain-text stdout lines > process error >
// scanner error > silent clean exit. A silent clean exit (exit 0, nothing on
// stdout or stderr) previously surfaced as an empty result — the user just saw
// "(空响应)" and the turn looked successful while the CLI had actually failed;
// it is now an explicit error so the turn visibly fails and the log carries
// whatever the CLI wrote to stderr.
func exitFallbackEvent(nonJSONLines []string, exitErr, scanErr error, stderrMsg, sessionID string) core.Event {
	switch {
	case len(nonJSONLines) > 0:
		slog.Warn("codebuddySession: no result event, falling back to plain-text output", "lines", len(nonJSONLines))
		return core.Event{Type: core.EventResult, Content: strings.Join(nonJSONLines, "\n"), SessionID: sessionID, Done: true}
	case exitErr != nil:
		msg := stderrMsg
		if msg == "" {
			msg = exitErr.Error()
		}
		slog.Error("codebuddySession: process failed with no result", "error", exitErr, "stderr", truncStr(msg, 200))
		return core.Event{Type: core.EventError, Error: fmt.Errorf("%s", msg)}
	case scanErr != nil:
		return core.Event{Type: core.EventError, Error: fmt.Errorf("read stdout: %w", scanErr)}
	default:
		// Clean exit (0) with zero stdout. stderr may still hold the real
		// reason (auth notice, resume failure, CLI internal message) — log it
		// and surface it; never silently swallow a zero-output turn. Marked
		// unrecoverable: the persisted agent_session_id cannot be resumed
		// (no backing session), so the engine detaches it for a fresh start.
		slog.Warn("codebuddySession: process exited with no output and no result event", "stderr", truncStr(stderrMsg, 200))
		if stderrMsg != "" {
			return core.Event{Type: core.EventError, Error: fmt.Errorf("%s", stderrMsg), Metadata: map[string]any{core.EventMetadataSessionUnrecoverable: true}}
		}
		return core.Event{Type: core.EventError, Error: fmt.Errorf("process exited with no output and no result event"), Metadata: map[string]any{core.EventMetadataSessionUnrecoverable: true}}
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
	// Thinking is a defensive fallback for codebuddy protocol variants that
	// emit the thinking block under a "thinking" key instead of "text".
	// Anthropic-style protocols use "thinking"; codebuddy CLI may use either
	// depending on version, so we accept both.
	Thinking string `json:"thinking"`
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
			// Cache tool_use_id → readable name so handleUser can resolve
			// the same name when the CLI returns the tool_result block (which
			// only carries tool_use_id, not name).
			if item.ID != "" && item.Name != "" {
				cs.toolNameByID.Store(item.ID, item.Name)
			}
			evt := core.Event{Type: core.EventToolUse, ToolName: item.Name, ToolID: item.ID, ToolInput: inputPreview}
			select {
			case cs.events <- evt:
			case <-cs.ctx.Done():
				return ""
			}

		case "thinking":
			// Accept either the Anthropic-style "thinking" field or a "text"
			// field under the same block, depending on the CLI protocol
			// variant. Both must be non-empty to emit a thinking event.
			thinking := item.Thinking
			if thinking == "" {
				thinking = item.Text
			}
			if thinking != "" {
				evt := core.Event{Type: core.EventThinking, Content: thinking}
				select {
				case cs.events <- evt:
				case <-cs.ctx.Done():
					return ""
				}
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
		// Resolve readable tool name from the assistant-side cache; the CLI
		// only sends tool_use_id on tool_result blocks, not the name.
		// Fall back to the raw id so the entry is still identifiable when
		// the cache is cold (e.g. CLI reordered, or output was truncated).
		toolName := item.ToolUseID
		if v, ok := cs.toolNameByID.Load(item.ToolUseID); ok {
			if name, ok := v.(string); ok && name != "" {
				toolName = name
			}
		}
		evt := core.Event{
			Type:      core.EventToolResult,
			ToolName:  toolName,
			ToolID:    item.ToolUseID,
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
// handleResult emits the terminal EventResult. It returns whether the result
// carried non-empty content — a result event with empty text (model/API
// returned nothing) must NOT be treated as a successful turn, so the caller
// falls through to exitFallbackEvent which surfaces stderr as an EventError.
func (cs *codebuddySession) handleResult(ev *streamEvent, pendingText string) bool {
	finalText := ev.Result
	if finalText == "" && pendingText != "" {
		finalText = pendingText
	}
	if strings.TrimSpace(finalText) == "" {
		slog.Warn("codebuddySession: result event with empty content", "session_id", cs.CurrentSessionID())
		return false
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
	return true
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
