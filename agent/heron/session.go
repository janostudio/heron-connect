package heron

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// emptyReplyPlaceholder is shown when Heron ends a turn without any reply
// text. It stays a successful result on purpose: the turn did end normally,
// and only a non-error terminal event keeps the session's background reader
// alive for the work that is still running.
const emptyReplyPlaceholder = "（本轮无回复）"

const (
	heronJSONRPCParseError     = -32700
	heronJSONRPCInvalidRequest = -32600
	heronJSONRPCMethodNotFound = -32601
	heronJSONRPCInvalidParams  = -32602
)

type heronSession struct {
	workDir  string
	command  string
	args     []string
	extraEnv []string

	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	stdin  io.WriteCloser

	events chan core.Event
	done   chan struct{}
	alive  atomic.Bool

	sessionID atomic.Value // stores string
	nextID    atomic.Uint64

	progressMu sync.Mutex
	// progressSeen deduplicates progress notifications per flow turn. A Heron
	// session runs several FlowTurns: the user's turn, then one turn per
	// mailbox wake-up (a dispatched Team reporting back, the entry Team
	// summarising it). Keying by turn instead of pinning one turn ID is what
	// lets an unattended turn's progress through — a single pinned ID silently
	// dropped everything after the first turn ended.
	//
	// Entries are bounded by pruneProgressSeen, which forgets turns a new turn
	// has superseded (see the turnSequence counter).
	progressSeen map[string]int64
	// progressOrder records the arrival order of each turn, so the turn the
	// caller is waiting on can be told apart from later mailbox turns without
	// relying on turn IDs, which carry no ordering.
	progressOrder    map[string]uint64
	turnSequence     uint64
	progressStatus   string
	pendingApprovals []heronPendingApproval
	// deliveredIDs remembers mailbox results already relayed to the user, so a
	// turn that reports the same delivery twice (completed + status frame)
	// does not produce two messages.
	deliveredIDs map[string]bool
	// sentBodies remembers the message bodies already sent within each FlowTurn.
	// A single turn carries both the mailbox result and the coordinating team's
	// reply, and the coordinator routinely repeats that result verbatim (s15:
	// 941 identical characters delivered twice). Dedup is per turn so a later
	// turn repeating the same wording still reaches the user.
	sentBodies map[string]map[string]bool

	writeMu    sync.Mutex
	turnMu     sync.Mutex
	closeOnce  sync.Once
	eventsOnce sync.Once
}

type heronJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type heronJSONRPCResponse struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      json.RawMessage    `json:"id,omitempty"`
	Result  json.RawMessage    `json:"result,omitempty"`
	Error   *heronJSONRPCError `json:"error,omitempty"`
}

type heronJSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type heronTurnResult struct {
	SessionID        string                 `json:"session_id"`
	FlowTurnID       string                 `json:"flow_turn_id"`
	Status           string                 `json:"status"`
	Reply            string                 `json:"reply"`
	Error            string                 `json:"error"`
	PendingApprovals []heronPendingApproval `json:"pending_approvals"`
	Usage            heronUsage             `json:"usage"`
}

type heronUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type heronPendingApproval struct {
	RequestID  string `json:"request_id"`
	CallID     string `json:"call_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type heronProgress struct {
	SessionID        string                 `json:"session_id"`
	FlowTurnID       string                 `json:"flow_turn_id"`
	Seq              int64                  `json:"seq"`
	EventID          string                 `json:"event_id"`
	Type             string                 `json:"type"`
	TeamID           string                 `json:"team_id"`
	CallID           string                 `json:"call_id"`
	CallTurnID       string                 `json:"call_turn_id"`
	CallType         string                 `json:"call_type"`
	Attempt          int                    `json:"attempt"`
	Round            int                    `json:"round"`
	Model            string                 `json:"model"`
	FinishReason     string                 `json:"finish_reason"`
	Content          json.RawMessage        `json:"content"`
	Usage            heronUsage             `json:"usage"`
	ToolUseID        string                 `json:"tool_use_id"`
	Name             string                 `json:"name"`
	Input            map[string]any         `json:"input"`
	IsError          bool                   `json:"is_error"`
	Status           string                 `json:"status"`
	ExitCode         *int                   `json:"exit_code"`
	PendingApprovals []heronPendingApproval `json:"pending_approvals"`
	RecordName       string                 `json:"record_name"`
	Revision         int                    `json:"revision"`
	ToolCalls        int                    `json:"tool_calls"`
	// Reply is the turn's final text, carried on flow_turn_completed for turns
	// the caller is not waiting on (mailbox wake-ups).
	Reply string `json:"reply"`
	// Origin distinguishes a user turn ("user") from a turn Heron started on
	// its own ("mailbox", "resume"). Empty on engines older than the field.
	Origin string `json:"origin"`
	// Intermediate marks a background turn whose reply is an internal step
	// rather than an answer for the user (a Team handing work back to its
	// activator). Such replies are never relayed.
	Intermediate bool `json:"intermediate"`
	// Deliveries are mailbox results the engine wants the user to receive as
	// their own messages, attributed to the Team that produced them. The
	// engine decides this from state (a delivered result), not from the
	// wording of the turn's reply.
	Deliveries []heronDelivery `json:"deliveries"`
}

type heronDelivery struct {
	Text      string `json:"text"`
	TeamID    string `json:"team_id"`
	MessageID string `json:"message_id"`
}

type heronContentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

func newHeronSession(
	ctx context.Context,
	command string,
	args []string,
	workDir string,
	resumeID string,
	extraEnv []string,
) (*heronSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(sessionCtx, command, args...)
	cmd.Dir = workDir
	core.PrepareCmdForKill(cmd)
	if len(extraEnv) > 0 {
		cmd.Env = core.MergeEnv(os.Environ(), extraEnv)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("heron: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("heron: stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("heron: start: %w", err)
	}

	session := &heronSession{
		workDir:  workDir,
		command:  command,
		args:     args,
		extraEnv: extraEnv,
		ctx:      sessionCtx,
		cancel:   cancel,
		cmd:      cmd,
		stdin:    stdin,
		events:   make(chan core.Event, 128),
		done:     make(chan struct{}),
	}
	if resumeID != "" && resumeID != core.ContinueSession {
		session.sessionID.Store(resumeID)
	}
	session.alive.Store(true)
	go session.readLoop(stdout, &stderr)
	return session, nil
}

func (s *heronSession) Send(prompt string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return errors.New("heron: session is closed")
	}
	if len(images) > 0 {
		slog.Warn("heron: images are not supported by the V1 JSON-RPC adapter; ignoring", "count", len(images))
	}
	if len(files) > 0 {
		paths := core.SaveFilesToDisk(s.workDir, files)
		prompt = core.AppendFileRefs(prompt, paths)
	}
	if strings.TrimSpace(prompt) == "" {
		return errors.New("heron: prompt is empty")
	}

	// Engine serializes turns for one core Session. Keep an adapter-side guard
	// as well so two callers cannot overlap Heron FlowTurns accidentally.
	s.turnMu.Lock()
	defer s.turnMu.Unlock()

	// A new user turn supersedes every turn seen so far: mailbox wake-ups from
	// the previous turn are done by the time it is over. Forgetting them keeps
	// the dedup map sized by one conversation turn rather than by session age.
	s.progressMu.Lock()
	s.progressSeen = make(map[string]int64)
	s.progressOrder = make(map[string]uint64)
	s.turnSequence = 0
	s.progressStatus = ""
	s.pendingApprovals = nil
	s.deliveredIDs = nil
	s.sentBodies = make(map[string]map[string]bool)
	s.progressMu.Unlock()

	id := s.nextID.Add(1)
	request := heronJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(fmt.Sprintf("%d", id)),
		Method:  "turn",
		Params: mustJSON(map[string]any{
			"session_id": s.CurrentSessionID(),
			"input":      prompt,
			// Keep the engine's subscription open after this turn returns.
			// A dispatch turn only routes the request ("已转给执行侧"), and
			// the Teams it hands work to answer in later FlowTurns; without
			// the subscription those events never reach this process, so the
			// final answer has nowhere to go. Engines older than 0.0.20 ignore
			// the field, which keeps this safe across versions.
			"follow_async": true,
		}),
	}
	if err := s.writeJSON(request); err != nil {
		return err
	}
	return nil
}

func (s *heronSession) readLoop(stdout io.ReadCloser, stderr *bytes.Buffer) {
	defer close(s.done)
	defer s.markDead()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := s.handleLine([]byte(line)); err != nil {
			s.emit(core.Event{Type: core.EventError, Error: err})
		}
	}

	scanErr := scanner.Err()
	if s.ctx.Err() != nil {
		_ = s.cmd.Wait()
		return
	}
	waitErr := s.cmd.Wait()
	if scanErr != nil {
		s.emit(core.Event{Type: core.EventError, Error: fmt.Errorf("heron: read stdout: %w", scanErr)})
		return
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		s.emit(core.Event{Type: core.EventError, Error: fmt.Errorf("heron: process exited: %s", message)})
		return
	}
	s.emit(core.Event{Type: core.EventError, Error: errors.New("heron: process exited before returning a response")})
}

func (s *heronSession) handleLine(line []byte) error {
	var message struct {
		JSONRPC string             `json:"jsonrpc"`
		ID      json.RawMessage    `json:"id,omitempty"`
		Method  string             `json:"method,omitempty"`
		Params  json.RawMessage    `json:"params,omitempty"`
		Result  json.RawMessage    `json:"result,omitempty"`
		Error   *heronJSONRPCError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &message); err != nil {
		return fmt.Errorf("heron: invalid JSON-RPC output: %w", err)
	}
	if message.JSONRPC != "2.0" {
		return errors.New("heron: JSON-RPC output has invalid jsonrpc version")
	}
	if message.Method != "" {
		return s.handleNotification(message.Method, message.Params)
	}
	return s.handleResponse(heronJSONRPCResponse{
		JSONRPC: message.JSONRPC,
		ID:      message.ID,
		Result:  message.Result,
		Error:   message.Error,
	})
}

func (s *heronSession) handleResponse(response heronJSONRPCResponse) error {
	if len(response.ID) == 0 || string(response.ID) == "null" {
		return errors.New("heron: JSON-RPC response has no id")
	}
	if response.Error != nil {
		if response.Error.Message == "" {
			response.Error.Message = "Heron request failed"
		}
		return s.emit(core.Event{
			Type:      core.EventError,
			Error:     fmt.Errorf("heron: JSON-RPC %d: %s", response.Error.Code, response.Error.Message),
			SessionID: s.sessionIDFromError(response.Error.Data),
		})
	}

	var result heronTurnResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return fmt.Errorf("heron: decode turn result: %w", err)
	}
	if result.SessionID != "" {
		s.sessionID.Store(result.SessionID)
	}
	s.progressMu.Lock()
	if result.Status == "" {
		result.Status = s.progressStatus
	}
	if len(result.PendingApprovals) == 0 {
		result.PendingApprovals = append([]heronPendingApproval(nil), s.pendingApprovals...)
	}
	s.progressMu.Unlock()
	if result.Error != "" {
		return s.emit(core.Event{
			Type:      core.EventError,
			Error:     errors.New(result.Error),
			SessionID: result.SessionID,
		})
	}
	if result.Status == "waiting_approval" || result.Status == "waiting_tool" {
		message := "等待审批"
		if result.Status == "waiting_approval" && len(result.PendingApprovals) > 0 && result.PendingApprovals[0].ToolName != "" {
			message = "等待审批：" + result.PendingApprovals[0].ToolName
		} else if result.Status == "waiting_tool" {
			message = "等待工具任务完成"
		}
		if err := s.emit(core.Event{
			Type:      core.EventThinking,
			Content:   message,
			SessionID: result.SessionID,
		}); err != nil {
			return err
		}
		return s.emit(core.Event{
			Type:      core.EventSuspended,
			Content:   message,
			SessionID: result.SessionID,
			Metadata: map[string]any{
				"status":            result.Status,
				"pending_approvals": result.PendingApprovals,
			},
		})
	}
	// Empty reply with no error (model/API returned nothing) must not surface
	// as a successful empty result.
	//
	// It must not surface as EventError either. Heron's dispatch turn
	// legitimately ends with a short routing reply and then keeps working in
	// the background; marking the normal end of a turn as an error makes the
	// engine flag the session for a resync, which stops the unsolicited reader
	// from ever starting — and with it every background result. A terminal
	// EventResult carrying a placeholder keeps the reader alive and still tells
	// the user the turn produced nothing.
	if strings.TrimSpace(result.Reply) == "" {
		slog.Warn("heronSession: turn returned empty reply", "session_id", result.SessionID)
		return s.emit(core.Event{
			Type:         core.EventResult,
			Content:      emptyReplyPlaceholder,
			SessionID:    result.SessionID,
			Done:         true,
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
			Metadata: map[string]any{
				"flow_turn_id": result.FlowTurnID,
				"status":       result.Status,
				"empty_reply":  true,
			},
		})
	}
	return s.emit(core.Event{
		Type:         core.EventResult,
		Content:      result.Reply,
		SessionID:    result.SessionID,
		Done:         true,
		InputTokens:  result.Usage.PromptTokens,
		OutputTokens: result.Usage.CompletionTokens,
		Metadata: map[string]any{
			"flow_turn_id": result.FlowTurnID,
			"status":       result.Status,
		},
	})
}

func (s *heronSession) handleNotification(method string, params json.RawMessage) error {
	if method != "progress" {
		slog.Debug("heron: ignored JSON-RPC notification", "method", method)
		return nil
	}

	var progress heronProgress
	if err := json.Unmarshal(params, &progress); err != nil {
		return fmt.Errorf("heron: decode progress notification: %w", err)
	}
	if progress.Type == "" {
		var legacy struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(params, &legacy); err != nil {
			return fmt.Errorf("heron: decode legacy progress notification: %w", err)
		}
		if legacy.Content == "" {
			return nil
		}
		return s.emit(core.Event{Type: core.EventThinking, Content: legacy.Content, SessionID: s.CurrentSessionID()})
	}
	if !s.acceptProgress(progress) {
		return nil
	}
	if progress.SessionID != "" {
		s.sessionID.Store(progress.SessionID)
	}

	sessionID := progress.SessionID
	if sessionID == "" {
		sessionID = s.CurrentSessionID()
	}
	// applyToUserTurn is set for a turn the caller is not waiting on: Heron
	// runs one FlowTurn per mailbox wake-up (a dispatched Team reporting back,
	// the entry Team summarising it), and a JSON-RPC "turn" response only ever
	// terminates the user's own turn. Progress of any other turn is therefore
	// relayed as an unattended agent turn — EventResult with no active caller —
	// which is exactly the "background completion" shape every other adapter
	// already produces.
	applyToUserTurn := s.progressIsUserTurn(progress.FlowTurnID)
	switch progress.Type {
	case "assistant":
		var blocks []heronContentBlock
		if len(progress.Content) == 0 || string(progress.Content) == "null" {
			return nil
		}
		if err := json.Unmarshal(progress.Content, &blocks); err != nil {
			return fmt.Errorf("heron: decode assistant content: %w", err)
		}
		for _, block := range blocks {
			if block.Type != "text" || block.Text == "" {
				continue
			}
			if !applyToUserTurn {
				continue
			}
			if err := s.emit(core.Event{Type: core.EventText, Content: block.Text, SessionID: sessionID}); err != nil {
				return err
			}
		}
	case "tool_use":
		return s.emit(core.Event{
			Type:         core.EventToolUse,
			ToolID:       progress.ToolUseID,
			ToolName:     progress.Name,
			ToolInput:    summarizeInput(progress.Name, progress.Input),
			ToolInputRaw: progress.Input,
			SessionID:    sessionID,
		})
	case "tool_result":
		var content string
		if len(progress.Content) > 0 && string(progress.Content) != "null" {
			if err := json.Unmarshal(progress.Content, &content); err != nil {
				return fmt.Errorf("heron: decode tool result content: %w", err)
			}
		}
		status := progress.Status
		if status == "" {
			if progress.IsError {
				status = "failed"
			} else {
				status = "completed"
			}
		}
		success := !progress.IsError
		return s.emit(core.Event{
			Type:         core.EventToolResult,
			ToolID:       progress.ToolUseID,
			ToolName:     progress.Name,
			ToolResult:   content,
			ToolStatus:   status,
			ToolExitCode: progress.ExitCode,
			ToolSuccess:  &success,
			SessionID:    sessionID,
		})
	case "flow_turn_completed":
		// A turn the caller is waiting on ends through its JSON-RPC response,
		// which carries the reply and the usage. Relaying the event as well
		// would duplicate that reply in the same bubble.
		if applyToUserTurn {
			return nil
		}
		return s.emitUnattendedTurn(progress, sessionID)
	case "flow_turn_started":
		// The caller's own turn is already running; a start notice for it adds
		// nothing. An unattended one is worth a thought line so the user can
		// tell a background turn apart from a stalled one.
		if applyToUserTurn || progress.Origin == "user" {
			return nil
		}
		return s.emit(core.Event{
			Type:      core.EventThinking,
			Content:   unattendedTurnNotice(progress),
			SessionID: sessionID,
		})
	case "team_turn_completed":
		if applyToUserTurn || progress.TeamID == "" {
			return nil
		}
		return s.emit(core.Event{
			Type:      core.EventThinking,
			Content:   fmt.Sprintf("↳ %s 完成", progress.TeamID),
			SessionID: sessionID,
		})
	case "status":
		s.progressMu.Lock()
		s.progressStatus = progress.Status
		s.pendingApprovals = append([]heronPendingApproval(nil), progress.PendingApprovals...)
		s.progressMu.Unlock()
		// A turn that ends waiting_input — every normal turn, including the
		// background ones — arrives as this synthetic status frame instead of
		// flow_turn_completed, and it carries the reply. Treating only
		// flow_turn_completed as terminal is why a background answer never
		// reaches the user even when the engine delivered it.
		if applyToUserTurn {
			return nil
		}
		return s.emitUnattendedTurn(progress, sessionID)
	case "call_completed":
		slog.Debug("heron: call completed",
			"call_id", progress.CallID,
			"call_type", progress.CallType,
			"status", progress.Status,
			"tool_calls", progress.ToolCalls,
			"usage", progress.Usage)
	case "record_published":
		slog.Debug("heron: record published",
			"record_name", progress.RecordName,
			"status", progress.Status,
			"revision", progress.Revision)
	}
	return nil
}

func (s *heronSession) acceptProgress(progress heronProgress) bool {
	if progress.SessionID == "" || progress.FlowTurnID == "" {
		return false
	}
	if progress.Type == "status" && progress.Seq == 0 {
		// Heron emits a synthetic status notification after stopping the event
		// pump; that frame has no event sequence or event ID.
	} else if progress.Seq <= 0 || progress.EventID == "" {
		return false
	}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.progressSeen == nil {
		s.progressSeen = make(map[string]int64)
	}
	if s.progressOrder == nil {
		s.progressOrder = make(map[string]uint64)
	}
	lastSeq, seen := s.progressSeen[progress.FlowTurnID]
	if !seen {
		// Record arrival order: the first turn of a Send cycle is the one the
		// JSON-RPC response terminates (see progressIsUserTurn).
		s.turnSequence++
		s.progressOrder[progress.FlowTurnID] = s.turnSequence
		s.progressSeen[progress.FlowTurnID] = 0
		lastSeq = 0
	}
	if progress.Seq > 0 {
		if progress.Seq <= lastSeq {
			return false
		}
		s.progressSeen[progress.FlowTurnID] = progress.Seq
	}
	return true
}

// progressIsUserTurn reports whether flowTurnID belongs to the turn the caller
// is waiting on. Heron answers a "turn" request with the reply of exactly one
// FlowTurn; every other turn of the same session (mailbox wake-ups) reaches the
// caller only as progress notifications.
//
// The user's turn is the first one seen in a Send cycle, which is the turn the
// response terminates. Later turns are unattended by construction: the reply
// that ends the request has already been produced or is being produced now.
func (s *heronSession) progressIsUserTurn(flowTurnID string) bool {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	order, seen := s.progressOrder[flowTurnID]
	// A turn with no recorded order has not been accepted yet; treat it as the
	// user's, the pre-existing behaviour for the very first turn.
	return !seen || order <= 1
}

// emitUnattendedTurn relays a background FlowTurn as an agent-initiated turn:
// its reply goes out as EventResult, which the engine already treats as an
// unsolicited completion (it appends history and sends to the platform without
// waiting for a caller).
// emitDeliveries relays mailbox results as their own agent-initiated messages.
// They are emitted before the turn's reply so the user sees the substance
// first, and they are attributed to the producing Team.
func (s *heronSession) emitDeliveries(progress heronProgress, sessionID string) error {
	for _, delivery := range progress.Deliveries {
		text := strings.TrimSpace(delivery.Text)
		if text == "" {
			continue
		}
		// Results are deduplicated by message id only. Two Teams answering with
		// the same wording are two separate results and must both arrive;
		// collapsing them by text would lose one.
		if delivery.MessageID != "" && s.deliveryAlreadySent(delivery.MessageID) {
			continue
		}
		metadata := map[string]any{
			"flow_turn_id": progress.FlowTurnID,
			"origin":       progress.Origin,
			"delivery":     true,
		}
		if delivery.TeamID != "" {
			metadata["team_id"] = delivery.TeamID
		}
		if err := s.emit(core.Event{
			Type:      core.EventResult,
			Content:   text,
			SessionID: sessionID,
			Done:      true,
			Metadata:  metadata,
		}); err != nil {
			return err
		}
		s.markDeliverySent(delivery.MessageID)
		s.markBodySent(progress.FlowTurnID, text)
	}
	return nil
}

func (s *heronSession) emitUnattendedTurn(progress heronProgress, sessionID string) error {
	if err := s.emitDeliveries(progress, sessionID); err != nil {
		return err
	}
	reply := strings.TrimSpace(progress.Reply)
	if reply == "" || progress.Intermediate {
		return nil
	}
	if s.bodyAlreadySent(progress.FlowTurnID, reply) {
		return nil
	}
	if err := s.emit(core.Event{
		Type:      core.EventResult,
		Content:   reply,
		SessionID: sessionID,
		Done:      true,
		Metadata: map[string]any{
			"flow_turn_id": progress.FlowTurnID,
			"status":       progress.Status,
			"origin":       progress.Origin,
		},
	}); err != nil {
		return err
	}
	s.markBodySent(progress.FlowTurnID, reply)
	return nil
}

func (s *heronSession) deliveryAlreadySent(messageID string) bool {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	return s.deliveredIDs[messageID]
}

func (s *heronSession) markDeliverySent(messageID string) {
	if messageID == "" {
		return
	}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.deliveredIDs == nil {
		s.deliveredIDs = make(map[string]bool)
	}
	s.deliveredIDs[messageID] = true
}

// bodyAlreadySent reports whether this exact body was already delivered within
// the same FlowTurn. A turn carries both the mailbox result and the
// coordinating reply, and the coordinator routinely repeats that result
// verbatim (s15: 941 identical characters delivered twice). Only the reply is
// judged by body, so two Teams wording the same conclusion are still both
// delivered.
func (s *heronSession) bodyAlreadySent(flowTurnID, text string) bool {
	key := bodyKey(text)
	if key == "" {
		return false
	}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	return s.sentBodies[flowTurnID][key]
}

// markBodySent records a body that was actually handed to the event channel.
// Recording after a successful emit (not before) keeps a failed send
// retryable: an adapter error must not swallow the message.
func (s *heronSession) markBodySent(flowTurnID, text string) {
	key := bodyKey(text)
	if key == "" {
		return
	}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.sentBodies == nil {
		s.sentBodies = make(map[string]map[string]bool)
	}
	seen := s.sentBodies[flowTurnID]
	if seen == nil {
		seen = make(map[string]bool)
		s.sentBodies[flowTurnID] = seen
	}
	seen[key] = true
}

// replyFooterSuffix matches the adapter's own presentation footer
// ("\n\n*[ctx: ~4%] · heron-ai*"), and contextSuffix the bare context marker.
// Both are added after the message leaves the engine, so they must not make an
// otherwise identical body look different when deciding whether it was already
// sent.
var (
	replyFooterSuffix = regexp.MustCompile(`(?s)\n\n\*[^*\n]*\*\s*$`)
	contextSuffix     = regexp.MustCompile(`\n?\[ctx: ~\d+%]`)
)

// bodyKey normalises a body for same-turn dedup. Normalisation only strips
// presentation the adapter itself appends; it never inspects meaning.
func bodyKey(text string) string {
	trimmed := strings.TrimSpace(text)
	trimmed = replyFooterSuffix.ReplaceAllString(trimmed, "")
	trimmed = contextSuffix.ReplaceAllString(trimmed, "")
	return strings.TrimSpace(trimmed)
}

// unattendedTurnNotice renders the one-line lead-in for a background turn, so
// the user can tell "a Team is still working" apart from "nothing is happening".
func unattendedTurnNotice(progress heronProgress) string {
	switch progress.Origin {
	case "mailbox":
		return "🔄 后台团队继续处理中…"
	default:
		return "🔄 会话继续处理中…"
	}
}

func summarizeInput(tool string, input map[string]any) string {
	if input == nil {
		return ""
	}
	switch tool {
	case "Read", "Edit", "Write":
		if path, ok := input["file_path"].(string); ok {
			return path
		}
		if path, ok := input["file"].(string); ok {
			return path
		}
	case "Bash":
		if command, ok := input["command"].(string); ok {
			return command
		}
	case "Grep", "Glob":
		if pattern, ok := input["pattern"].(string); ok {
			return pattern
		}
		if glob, ok := input["glob_pattern"].(string); ok {
			return glob
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func (s *heronSession) sessionIDFromError(raw json.RawMessage) string {
	var data struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &data)
	if data.SessionID != "" {
		s.sessionID.Store(data.SessionID)
		return data.SessionID
	}
	return s.CurrentSessionID()
}

func (s *heronSession) writeJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("heron: encode JSON-RPC request: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("heron: write stdin: %w", err)
	}
	return nil
}

func (s *heronSession) emit(event core.Event) error {
	if event.SessionID == "" {
		event.SessionID = s.CurrentSessionID()
	}
	select {
	case s.events <- event:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *heronSession) Events() <-chan core.Event {
	return s.events
}

func (s *heronSession) CurrentSessionID() string {
	value, _ := s.sessionID.Load().(string)
	return value
}

func (s *heronSession) Alive() bool {
	return s.alive.Load()
}

func (s *heronSession) RespondPermission(_ string, _ core.PermissionResult) error {
	return errors.New("heron: permission responses are not supported by the V1 JSON-RPC protocol")
}

func (s *heronSession) CancelTurn() {
	// V1 Heron JSON-RPC has no cancel method. Keep the process alive and let
	// the caller close the session when it needs a hard cancellation.
	slog.Debug("heron: CancelTurn is not supported by V1 JSON-RPC")
}

func (s *heronSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.alive.Store(false)
		_ = s.stdin.Close()

		select {
		case <-s.done:
		case <-time.After(8 * time.Second):
			s.cancel()
			closeErr = core.ForceKillProcessGroup(s.cmd)
			select {
			case <-s.done:
			case <-time.After(2 * time.Second):
			}
		}
		s.cancel()
		s.closeEvents()
	})
	return closeErr
}

func (s *heronSession) markDead() {
	s.alive.Store(false)
	s.cancel()
	s.closeEvents()
}

func (s *heronSession) closeEvents() {
	s.eventsOnce.Do(func() {
		close(s.events)
	})
}

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
