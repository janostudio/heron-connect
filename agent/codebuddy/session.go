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
//
// CodeBuddy always runs in resident mode: the process is started once with
// `--input-format stream-json` and stays alive across turns, reading prompts
// from stdin as stream-json lines. This is not optional — the CLI emits
// control_request/can_use_tool frames and blocks on stdin until a matching
// control_response arrives, so a per-turn spawn (whose process has no stdin
// channel to reply on) deadlocks the moment the agent asks for permission.
// Resident mode also gives us CancelTurn: control_request/interrupt aborts the
// running turn, and a new prompt can be injected immediately.
//
// The interruptible field is retained as the mode switch but is always set by
// Agent.New; the per-turn branches remain only as defensive fallbacks.
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

	// cmdMu guards osCmd, which is written by the spawning path (or resident
	// startup) and read by Close for force-kill.
	cmdMu sync.Mutex
	osCmd *exec.Cmd // for force-kill on Close timeout

	// toolNameByID caches tool_use_id → readable tool name across the
	// assistant→user turn boundary. The CLI's user-message stream emits
	// tool_result blocks with only tool_use_id (no name), so the adapter
	// must remember the name emitted in the prior assistant tool_use block.
	// Uses sync.Map so handleAssistant (write) and handleUser (read) can
	// run concurrently if events arrive out-of-order on different goroutines.
	toolNameByID sync.Map // string → string

	// interruptible enables the resident-process mode (see the type comment).
	interruptible bool

	// stdin is the resident process's stdin, used to write prompts and control
	// frames. nil in per-turn mode.
	stdinMu sync.Mutex
	stdin   io.WriteCloser

	// turnEpoch is the epoch the engine stamped for the current turn; every
	// event this session emits carries it so the engine can discard leftovers
	// from an interrupted turn. Zero means "unstamped" (engine accepts all).
	turnEpoch atomic.Uint64

	// nextRequestID mints unique request_id values for control_request frames.
	nextRequestID atomic.Int64

	// turnSeq counts foreground turns started on this session. Bumped when a
	// prompt is written, it lets the background-task bookkeeping say "this
	// task belongs to turn N". See turnFinishedSeq / handleTaskEvent.
	turnSeq atomic.Uint64

	// turnFinishedSeq records the highest turn number whose result has been
	// seen. A background task is relayable once the turn that STARTED it has
	// finished — a watermark rather than a boolean, so a task that outlives
	// several later turns still reports in instead of being suppressed by
	// whatever turn happens to be running when it ends.
	turnFinishedSeq atomic.Uint64

	// tasksMu guards tasks, the background-task registry.
	tasksMu sync.Mutex
	// tasks maps task_id → the last lifecycle state seen for that background
	// task. It serves two purposes:
	//
	//  1. De-duplication. task_notification is the authoritative terminal
	//     signal, but task_updated may also carry a terminal status (the CLI
	//     docs note CC sometimes emits ONLY task_updated for a terminal
	//     transition), and the drain-wait bypass stream can replay frames the
	//     foreground stream already delivered. Emitting a platform message per
	//     frame would spam the user with the same completion several times.
	//  2. Context. task_notification carries only task_id/status/summary;
	//     the human-readable label lives in task_started.description, which
	//     arrives earlier on a different frame.
	tasks map[string]*backgroundTask
}

// backgroundTask is the per-task state tracked across the task_* event family.
type backgroundTask struct {
	description string
	// startedInTurn is the foreground turn number during which this task was
	// first observed (i.e. the turn whose tool call spawned it). Its terminal
	// frame is relayable only once turnFinishedSeq has reached this value, so
	// a task finishing inside its own turn cannot cut that turn's reply short.
	startedInTurn uint64
	// notified is set once a terminal notification has been relayed, so a
	// replayed or late-arriving terminal frame is dropped instead of
	// producing a duplicate message.
	notified bool
}

// isTerminalTaskStatus reports whether a status value ends a background
// task's life. task_updated uses the full six-value enum (pending, running,
// paused, completed, failed, killed) while task_notification collapses to
// three (completed, failed, stopped) — the union is what matters here.
func isTerminalTaskStatus(status string) bool {
	switch status {
	case "completed", "failed", "stopped", "killed", "cancelled":
		return true
	default:
		return false
	}
}

// handleTaskEvent processes the background-task event family
// (system/subtype=task_started|task_progress|task_updated|task_notification).
//
// This family is how the CLI reports work that outlives the turn that started
// it: a Bash command run with run_in_background, a background subagent, or a
// workflow run. Two delivery windows exist:
//
//   - MID-TURN: task_started / a running task_updated arrive while the
//     foreground turn is still streaming. These are progress only. They MUST
//     NOT emit an EventResult — the engine's foreground consumer treats
//     EventResult as "the turn is over" (it finalizes the progress card,
//     writes history and fires auto-titling), so relaying one here would cut
//     the user's reply short.
//
//   - BETWEEN TURNS: after the foreground turn's result, the CLI re-opens a
//     drain-wait bypass stream to report the task reaching a terminal state.
//     Nothing is consuming the event channel at that point except the engine's
//     unsolicited reader (core/engine_turn.go runUnsolicitedReader), which
//     exists precisely to relay such events to the platform.
//
// Which window applies is decided per task: each task records the turn number
// it was spawned in, and its terminal frame is relayable once that turn has
// finished (turnFinishedSeq >= startedInTurn). Comparing against a watermark
// rather than "is a turn running right now" matters — a long-running task that
// completes while the user is already in a later turn still reports in, which
// is what the user expects after asking for background work.
//
// Before this handler existed the whole family fell into readLoop's default
// branch and was dropped at debug level, so a background task's completion was
// invisible to the user: the agent reported "started" and then nothing ever
// came back.
//
// Returns whether the event belonged to this family.
func (cs *codebuddySession) handleTaskEvent(ev *streamEvent) bool {
	switch ev.Subtype {
	case "task_started", "task_progress", "task_updated", "task_notification":
	default:
		return false
	}

	if ev.TaskID == "" {
		slog.Debug("codebuddySession: task event without task_id", "subtype", ev.Subtype)
		return true
	}

	cs.tasksMu.Lock()
	if cs.tasks == nil {
		cs.tasks = make(map[string]*backgroundTask)
	}
	task, ok := cs.tasks[ev.TaskID]
	if !ok {
		// First sighting of this task: it was spawned by the turn running
		// right now, so remember which turn that was.
		task = &backgroundTask{startedInTurn: cs.turnSeq.Load()}
		cs.tasks[ev.TaskID] = task
	}
	if ev.Description != "" {
		task.description = ev.Description
	}

	// Determine whether this frame carries a terminal state, and what text
	// to relay. task_notification carries `status` + `summary` at top level;
	// task_updated carries the transition inside `patch`.
	status := ev.Status
	summary := ev.Summary
	if status == "" && len(ev.Patch) > 0 {
		var patch struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(ev.Patch, &patch); err == nil {
			status = patch.Status
			if summary == "" {
				summary = patch.Summary
			}
		}
	}

	terminal := isTerminalTaskStatus(status)
	// Dedup: relay a terminal state exactly once per task, and only once the
	// turn that started it has finished. The authoritative frame is
	// task_notification, but a terminal task_updated is honoured too because
	// the CLI documents that it may be the only terminal frame.
	startedInTurn := task.startedInTurn
	shouldRelay := terminal && !task.notified && cs.turnFinishedSeq.Load() >= startedInTurn
	if shouldRelay {
		task.notified = true
	}
	description := task.description
	cs.tasksMu.Unlock()

	slog.Debug("codebuddySession: task event",
		"subtype", ev.Subtype, "task_id", ev.TaskID, "status", status,
		"terminal", terminal, "started_in_turn", startedInTurn, "relay", shouldRelay)

	if !shouldRelay {
		return true
	}

	cs.emit(core.Event{
		Type:      core.EventResult,
		Content:   formatTaskCompletion(ev.TaskID, description, status, summary, ev.OutputFile),
		SessionID: cs.CurrentSessionID(),
		Done:      true,
	})
	return true
}

// taskDescriptionMaxLen bounds how much of the CLI-supplied task description
// is forwarded to a chat platform. task_started.description (for workflow
// tasks, workflow_name) and task_notification.summary embed model-generated
// and user-supplied text, so they are treated as untrusted: a long or
// multi-line value would blow past platform message limits or break the
// one-line layout.
const taskDescriptionMaxLen = 200

// formatTaskCompletion renders the user-facing line for a finished background
// task. The CLI's own summary is preferred when present; otherwise the
// description recorded at task_started is used so the message still says
// which command finished rather than only an opaque task id.
func formatTaskCompletion(taskID, description, status, summary, outputFile string) string {
	label := sanitizeTaskText(summary)
	if label == "" {
		label = sanitizeTaskText(description)
	}
	if label == "" {
		label = taskID
	}

	var b strings.Builder
	switch status {
	case "completed":
		b.WriteString("后台任务已完成：")
	case "failed":
		b.WriteString("后台任务失败：")
	case "stopped", "killed", "cancelled":
		b.WriteString("后台任务已停止：")
	default:
		b.WriteString("后台任务结束：")
	}
	b.WriteString(label)

	if outputFile != "" {
		b.WriteString("\n输出文件：")
		b.WriteString(sanitizeTaskText(outputFile))
	}
	return b.String()
}

// sanitizeTaskText collapses whitespace and truncates CLI-supplied text to a
// platform-safe length. The value is embedded in an outgoing chat message, so
// embedded newlines (which would forge additional lines) are flattened.
func sanitizeTaskText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= taskDescriptionMaxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:taskDescriptionMaxLen]) + "…"
}

// baseArgs builds the codebuddy CLI flags shared by both modes.
func baseArgs(sid, mode, model string, extraArgs []string) []string {
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

	return append(args, extraArgs...)
}

// launchArgs builds the per-turn codebuddy CLI argument list. The prompt is
// passed as the positional argument after the "--" end-of-options marker: the
// CLI rejects tokens starting with "-" as unknown options, so without the
// marker any prompt beginning with "-" (e.g. custom command files whose YAML
// frontmatter starts with "---") fails with "error: unknown option".
func launchArgs(prompt, sid, mode, model string, extraArgs []string) []string {
	return append(baseArgs(sid, mode, model, extraArgs), "--", prompt)
}

// residentArgs builds the resident-process argument list. The prompt is NOT a
// positional argument here: --input-format stream-json makes the CLI read
// prompts from stdin as JSON lines, and a trailing positional prompt would
// conflict with that stream. Also no end-of-options marker is needed, since no
// prompt text reaches the command line.
func residentArgs(sid, mode, model string, extraArgs []string) []string {
	return append([]string{"--input-format", "stream-json"},
		baseArgs(sid, mode, model, extraArgs)...)
}

func newCodeBuddySession(ctx context.Context, workDir, model, mode, resumeID string, extraArgs, extraEnv []string, interruptible bool) (*codebuddySession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)

	cs := &codebuddySession{
		workDir:       workDir,
		model:         model,
		mode:          mode,
		extraArgs:     extraArgs,
		extraEnv:      extraEnv,
		events:        make(chan core.Event, 64),
		ctx:           sessionCtx,
		cancel:        cancel,
		interruptible: interruptible,
	}
	cs.alive.Store(true)

	if resumeID != "" && resumeID != core.ContinueSession {
		cs.sessionID.Store(resumeID)
	}

	if interruptible {
		if err := cs.startResident(); err != nil {
			cancel()
			return nil, err
		}
	}

	return cs, nil
}

// startResident launches the long-lived process whose stdin carries prompts and
// control frames. Only used when interruptible is on; the per-turn path keeps
// spawning inside Send.
func (cs *codebuddySession) startResident() error {
	sid := cs.CurrentSessionID()
	args := residentArgs(sid, cs.mode, cs.model, cs.extraArgs)

	slog.Debug("codebuddySession: starting resident process", "resume", sid != "", "args_len", len(args))

	cmd := exec.CommandContext(cs.ctx, "codebuddy", args...)
	cmd.Dir = cs.workDir
	core.PrepareCmdForKill(cmd)
	if len(cs.extraEnv) > 0 {
		cmd.Env = core.MergeEnv(os.Environ(), cs.extraEnv)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codebuddySession: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codebuddySession: stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codebuddySession: start resident: %w", err)
	}

	cs.cmdMu.Lock()
	cs.osCmd = cmd
	cs.cmdMu.Unlock()
	cs.stdinMu.Lock()
	cs.stdin = stdin
	cs.stdinMu.Unlock()

	cs.wg.Add(1)
	go cs.readLoop(cmd, stdout, &stderrBuf)
	return nil
}

// writeJSON writes one newline-delimited JSON frame to the resident process
// stdin. Only valid in interruptible mode.
func (cs *codebuddySession) writeJSON(v any) error {
	cs.stdinMu.Lock()
	defer cs.stdinMu.Unlock()
	if cs.stdin == nil {
		return fmt.Errorf("codebuddySession: resident stdin is not available")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := cs.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	return nil
}

// emit tags an event with the current turn epoch and sends it to the event
// channel. Every event this session produces goes through here.
func (cs *codebuddySession) emit(ev core.Event) {
	ev.TurnEpoch = cs.turnEpoch.Load()
	select {
	case cs.events <- ev:
	case <-cs.ctx.Done():
	}
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

	// Resident mode: the process is already running and reads prompts from
	// stdin as stream-json lines. No spawn, no positional argument.
	if cs.interruptible {
		slog.Debug("codebuddySession: sending prompt to resident process")
		// A new foreground turn is starting. Bumping the turn counter means
		// a task frame arriving before this turn's result is attributed to
		// this turn and won't be relayed as a between-turns completion. See
		// handleTaskEvent.
		cs.turnSeq.Add(1)
		return cs.writeJSON(map[string]any{
			"type": "user",
			"message": map[string]any{
				"role":    "user",
				"content": prompt,
			},
		})
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
	cs.cmdMu.Lock()
	cs.osCmd = cmd
	cs.cmdMu.Unlock()

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
					// A mid-conversation init: either a subagent's child
					// session or the drain-wait bypass stream (re-emitted
					// after the foreground turn's result). Neither may
					// overwrite the tracked top-level id.
					slog.Debug("codebuddySession: ignoring non-primary init (subagent/drain)", "session_id", raw.SessionID)
				}
			} else if cs.handleTaskEvent(&raw) {
				// Background-task lifecycle event — handled. See
				// handleTaskEvent for when the terminal frame becomes a
				// relayable EventResult.
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
			// Raise the finished-turn watermark: any background task spawned
			// in this turn (or an earlier one) is now safe to relay. A
			// watermark rather than a boolean, so a task that outlives later
			// turns still reports in. See handleTaskEvent.
			cs.turnFinishedSeq.Store(cs.turnSeq.Load())
			if cs.interruptible {
				// Resident mode: the process outlives the turn, so the scanner
				// must keep reading for the next prompt. Reset the per-turn
				// bookkeeping and carry on instead of breaking out to the
				// exit path.
				gotResult = false
				nonJSONLines = nil
				sawInit = true // only the very first init of the process counts
			}

		case "control_request":
			cs.handleControlRequest(&raw)

		case "control_cancel_request":
			slog.Debug("codebuddySession: permission cancelled", "request_id", raw.RequestID)

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

	// In resident mode, stdout closing means the process is gone: the session
	// is dead regardless of whether the last turn produced a result.
	if cs.interruptible {
		cs.alive.Store(false)
		if exitErr != nil {
			stderrMsg := strings.TrimSpace(stderrBuf.String())
			if stderrMsg != "" {
				slog.Error("codebuddySession: resident process failed", "error", exitErr, "stderr", truncStr(stderrMsg, 200))
				cs.emit(core.Event{Type: core.EventError, Error: fmt.Errorf("%s", stderrMsg)})
			}
		}
		return
	}

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
	cs.emit(evt)
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
	Type      string         `json:"type"`
	Subtype   string         `json:"subtype"`
	UUID      string         `json:"uuid"`
	SessionID string         `json:"session_id"`
	Result    string         `json:"result"`
	IsError   bool           `json:"is_error"`
	Message   *streamMessage `json:"message"`

	// RequestID and Request carry control_request frames. The CLI blocks on
	// stdin until a matching control_response arrives, so these must be
	// surfaced to the engine as a permission prompt rather than ignored —
	// see handleControlRequest.
	RequestID string              `json:"request_id"`
	Request   *controlRequestBody `json:"request"`

	// ── background-task event family (system/subtype=task_*) ──
	//
	// The CLI emits task_started / task_progress / task_updated /
	// task_notification as `system` frames carrying these fields at the top
	// level (not nested under "message"). See handleSystemSubtype.
	TaskID      string          `json:"task_id"`
	TaskType    string          `json:"task_type"`
	Description string          `json:"description"`
	Summary     string          `json:"summary"`
	OutputFile  string          `json:"output_file"`
	Status      string          `json:"status"`
	Patch       json.RawMessage `json:"patch"`
}

// controlRequestBody is the "request" object of a control_request frame.
// Only the can_use_tool subtype is defined today; the rest are ignored.
type controlRequestBody struct {
	Subtype   string         `json:"subtype"`
	ToolName  string         `json:"tool_name"`
	ToolUseID string         `json:"tool_use_id"`
	Input     map[string]any `json:"input"`
}

type streamMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
}

type contentItem struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
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
				cs.emit(core.Event{Type: core.EventText, Content: item.Text})
			}

		case "tool_use":
			inputPreview := extractToolPreview(item.Input)
			// Cache tool_use_id → readable name so handleUser can resolve
			// the same name when the CLI returns the tool_result block (which
			// only carries tool_use_id, not name).
			if item.ID != "" && item.Name != "" {
				cs.toolNameByID.Store(item.ID, item.Name)
			}
			cs.emit(core.Event{Type: core.EventToolUse, ToolName: item.Name, ToolID: item.ID, ToolInput: inputPreview})

		case "thinking":
			// Accept either the Anthropic-style "thinking" field or a "text"
			// field under the same block, depending on the CLI protocol
			// variant. Both must be non-empty to emit a thinking event.
			thinking := item.Thinking
			if thinking == "" {
				thinking = item.Text
			}
			if thinking != "" {
				cs.emit(core.Event{Type: core.EventThinking, Content: thinking})
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
		cs.emit(core.Event{
			Type:      core.EventToolResult,
			ToolName:  toolName,
			ToolID:    item.ToolUseID,
			Content:   resultText,
			SessionID: cs.CurrentSessionID(),
		})
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

	cs.emit(core.Event{
		Type:      core.EventResult,
		Content:   finalText,
		SessionID: cs.CurrentSessionID(),
		Done:      true,
	})
	return true
}

// handleControlRequest turns a CLI control_request into an
// EventPermissionRequest for the engine.
//
// This is load-bearing, not cosmetic: the CLI emits control_request and then
// blocks on stdin until a matching control_response arrives. Ignoring the
// frame — as the adapter did before it handled this event type — leaves the
// CLI waiting forever, so no result event is ever produced and the turn hangs
// indefinitely (ExitPlanMode blocks the whole turn; in --permission-mode plan
// every tool call does). See RespondPermission for the reply path.
func (cs *codebuddySession) handleControlRequest(ev *streamEvent) {
	if ev.RequestID == "" || ev.Request == nil {
		slog.Debug("codebuddySession: malformed control_request, ignoring")
		return
	}
	if ev.Request.Subtype != "can_use_tool" {
		// Unknown subtype: the CLI would still block on a response we don't
		// know how to build. Reply with a denial so the turn can make
		// progress instead of deadlocking.
		slog.Warn("codebuddySession: unknown control_request subtype", "subtype", ev.Request.Subtype, "request_id", ev.RequestID)
		_ = cs.RespondPermission(ev.RequestID, core.PermissionResult{
			Behavior: "deny",
			Message:  "unsupported control request subtype: " + ev.Request.Subtype,
		})
		return
	}

	if !cs.interruptible {
		// No stdin channel exists to carry the control_response, so the CLI
		// can never be unblocked. Surface it loudly: silently dropping this
		// is exactly the infinite hang this handler exists to prevent.
		// The Agent forces resident mode, so this should be unreachable.
		slog.Error("codebuddySession: control_request in non-resident mode cannot be answered", "request_id", ev.RequestID, "tool", ev.Request.ToolName)
		cs.emit(core.Event{
			Type:  core.EventError,
			Error: fmt.Errorf("codebuddy: permission request for %q requires resident (interruptible) mode", ev.Request.ToolName),
		})
		return
	}

	slog.Info("codebuddySession: permission request", "request_id", ev.RequestID, "tool", ev.Request.ToolName)
	cs.emit(core.Event{
		Type:         core.EventPermissionRequest,
		RequestID:    ev.RequestID,
		ToolName:     ev.Request.ToolName,
		ToolInput:    extractToolPreview(mustJSON(ev.Request.Input)),
		ToolInputRaw: ev.Request.Input,
	})
}

// mustJSON marshals a tool-input map for preview extraction, yielding an empty
// raw message when marshalling fails so extractToolPreview falls back cleanly.
func mustJSON(v map[string]any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

// RespondPermission writes the control_response the CLI is blocked on. Only
// meaningful in resident mode: the reply has to travel back over the resident
// process's stdin, which the per-turn path does not have.
func (cs *codebuddySession) RespondPermission(requestID string, result core.PermissionResult) error {
	if !cs.interruptible {
		return fmt.Errorf("codebuddySession: RespondPermission requires resident (interruptible) mode")
	}

	var permResponse map[string]any
	if result.Behavior == "allow" {
		updatedInput := result.UpdatedInput
		if updatedInput == nil {
			updatedInput = map[string]any{}
		}
		permResponse = map[string]any{
			"behavior":     "allow",
			"updatedInput": updatedInput,
		}
	} else {
		msg := result.Message
		if msg == "" {
			msg = "The user denied this tool use. Stop and wait for the user's instructions."
		}
		permResponse = map[string]any{
			"behavior": "deny",
			"message":  msg,
		}
	}

	controlResponse := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   permResponse,
		},
	}

	slog.Debug("codebuddySession: permission response", "request_id", requestID, "behavior", result.Behavior)
	return cs.writeJSON(controlResponse)
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

	// Resident mode: closing stdin first lets the CLI exit on its own, which
	// keeps the transcript write-out clean. Mirrors the claudecode shutdown.
	if cs.interruptible {
		cs.stdinMu.Lock()
		if cs.stdin != nil {
			_ = cs.stdin.Close()
			cs.stdin = nil
		}
		cs.stdinMu.Unlock()
	}

	done := make(chan struct{})
	go func() {
		cs.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		cs.cmdMu.Lock()
		cmd := cs.osCmd
		cs.cmdMu.Unlock()
		if cmd != nil {
			_ = core.ForceKillProcessGroup(cmd)
		}
	}
	close(cs.events)
	return nil
}

// CancelTurn asks the CodeBuddy process to abort the in-flight turn via a
// control_request/interrupt frame. Only meaningful in resident mode (the
// per-turn process has no stdin channel and exits with its turn anyway).
//
// The process stays alive and can accept the next prompt, so the engine can
// inject a new message right away.
func (cs *codebuddySession) CancelTurn() {
	if !cs.interruptible {
		slog.Debug("codebuddySession: CancelTurn not supported in per-turn mode")
		return
	}
	if !cs.alive.Load() {
		slog.Debug("codebuddySession: CancelTurn on a dead session, ignoring")
		return
	}

	reqID := fmt.Sprintf("interrupt-%d", cs.nextRequestID.Add(1))
	controlRequest := map[string]any{
		"type":       "control_request",
		"request_id": reqID,
		"request": map[string]any{
			"subtype": "interrupt",
		},
	}
	if err := cs.writeJSON(controlRequest); err != nil {
		slog.Warn("codebuddySession: interrupt request failed", "request_id", reqID, "error", err)
		return
	}
	slog.Debug("codebuddySession: interrupt requested", "request_id", reqID)
}

// Interruptible reports whether this session supports mid-turn interruption
// with immediate prompt injection.
func (cs *codebuddySession) Interruptible() bool { return cs.interruptible }

// SetTurnEpoch records the epoch the engine stamped for the current turn.
// Every event emitted afterwards carries it (see emit).
func (cs *codebuddySession) SetTurnEpoch(epoch uint64) { cs.turnEpoch.Store(epoch) }

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
