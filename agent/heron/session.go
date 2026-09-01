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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/janostudio/heron-connect/core"
)

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
	SessionID  string `json:"session_id"`
	FlowTurnID string `json:"flow_turn_id"`
	Status     string `json:"status"`
	Reply      string `json:"reply"`
	Error      string `json:"error"`
	Usage      struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
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

	id := s.nextID.Add(1)
	request := heronJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(fmt.Sprintf("%d", id)),
		Method:  "turn",
		Params: mustJSON(map[string]any{
			"session_id": s.CurrentSessionID(),
			"input":      prompt,
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
	if result.Error != "" {
		return s.emit(core.Event{
			Type:      core.EventError,
			Error:     errors.New(result.Error),
			SessionID: result.SessionID,
		})
	}
	// Empty reply with no error (model/API returned nothing) must not surface
	// as a successful empty result.
	if strings.TrimSpace(result.Reply) == "" {
		slog.Warn("heronSession: turn returned empty reply", "session_id", result.SessionID)
		return s.emit(core.Event{
			Type:      core.EventError,
			Error:     errors.New("model returned an empty result"),
			SessionID: result.SessionID,
			Done:      true,
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
	switch method {
	case "progress":
		var progress struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(params, &progress); err != nil {
			return fmt.Errorf("heron: decode progress notification: %w", err)
		}
		if progress.Content != "" {
			return s.emit(core.Event{Type: core.EventThinking, Content: progress.Content, SessionID: s.CurrentSessionID()})
		}
		return nil
	default:
		slog.Debug("heron: ignored JSON-RPC notification", "method", method)
		return nil
	}
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
