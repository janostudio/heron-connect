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
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// httpSession talks to a running `heron-ai --serve`.
//
// It implements the same core.AgentSession as the stdio adapter, so the engine
// above it does not know which one it holds. What changes is the shape of the
// conversation: the engine is one process serving every session, turns arrive
// over HTTP, and the session's event stream stays open so a turn the model
// started on its own — a scheduled wake-up — reaches the user without a request
// carrying it.
type httpSession struct {
	baseURL string
	token   string
	client  *http.Client
	events  chan core.Event
	closed  chan struct{}
	// streamCancel stops the event-stream goroutine; nil until it starts.
	streamCancel context.CancelFunc

	closeOnce  sync.Once
	streamOnce sync.Once

	mu        sync.RWMutex
	sessionID string
	// lastSeq is the highest event id read off the stream. A reconnect sends
	// it as Last-Event-ID so the engine resumes instead of replaying.
	lastSeq int64

	// seenTurns de-duplicates a turn that reaches us twice: once as the POST
	// response and once on the event stream. Both are the same answer.
	seenMu    sync.Mutex
	seenTurns map[string]bool
}

func newHTTPSession(baseURL, token, sessionID string) *httpSession {
	return &httpSession{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		client:    &http.Client{Timeout: 0}, // turns can take minutes; the caller cancels
		events:    make(chan core.Event, 64),
		closed:    make(chan struct{}),
		sessionID: sessionID,
		seenTurns: make(map[string]bool),
	}
}

func (s *httpSession) Send(prompt string, _ []core.ImageAttachment, _ []core.FileAttachment) error {
	if prompt == "" {
		return errors.New("heron: empty prompt")
	}
	select {
	case <-s.closed:
		return errors.New("heron: session is closed")
	default:
	}
	// The turn runs in the background: the engine above expects Send to return
	// and the answer to arrive as an event, the same contract as stdio.
	go s.runTurn(prompt)
	return nil
}

func (s *httpSession) runTurn(prompt string) {
	result, err := s.postTurn(prompt)
	if err != nil {
		s.emit(core.Event{Type: core.EventError, Error: err, Done: true})
		return
	}
	if result.Session.ID != "" {
		s.setSessionID(result.Session.ID)
		s.startEventStream()
	}
	if result.Error != "" {
		s.emit(core.Event{Type: core.EventError, Content: result.Error, Done: true})
		return
	}
	s.markTurnSeen(result.Turn.ID)
	text := result.FinalReply
	if strings.TrimSpace(text) == "" {
		text = result.Reply
	}
	s.emit(core.Event{
		Type:      core.EventResult,
		Content:   text,
		SessionID: result.Session.ID,
		Done:      true,
	})
}

// postTurn runs one turn. A session with no id yet creates one, which is how
// the first message of a conversation works in serve mode.
// serveTurnResult mirrors the HTTP response of /api/run and
// /api/sessions/turn. The outer keys are PascalCase and the inner ones are
// snake_case — that is the engine's existing wire shape, not a choice made
// here, so it is decoded separately from the JSON-RPC result type.
type serveTurnResult struct {
	Session struct {
		ID string `json:"id"`
	} `json:"Session"`
	Turn struct {
		ID           string `json:"id"`
		Origin       string `json:"origin"`
		Intermediate bool   `json:"intermediate"`
	} `json:"Turn"`
	Reply      string `json:"Reply"`
	FinalReply string `json:"FinalReply"`
	Error      string `json:"Error"`
}

func (s *httpSession) postTurn(prompt string) (*serveTurnResult, error) {
	body, err := json.Marshal(map[string]any{"input": prompt})
	if err != nil {
		return nil, err
	}
	path := "/api/run"
	if sessionID := s.CurrentSessionID(); sessionID != "" {
		path = "/api/sessions/turn?session_id=" + sessionID
	}
	var result serveTurnResult
	if err := s.doJSON(http.MethodPost, path, bytes.NewReader(body), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// startEventStream subscribes once, after the session id is known. The stream
// carries the turns this session starts without a request.
func (s *httpSession) startEventStream() {
	s.streamOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.streamCancel = cancel
		go s.consumeEvents(ctx)
	})
}

func (s *httpSession) consumeEvents(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-s.closed:
			return
		case <-ctx.Done():
			return
		default:
		}
		if err := s.pumpOnce(ctx); err != nil {
			if ctx.Err() != nil || s.isClosed() {
				return
			}
			slog.Warn("heron: event stream ended, reconnecting", "error", err, "retry_in", backoff)
			select {
			case <-time.After(backoff):
			case <-s.closed:
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

// pumpOnce reads the SSE stream until it ends. The engine keeps the connection
// open with follow=1, so a normal end means the server went away.
func (s *httpSession) pumpOnce(ctx context.Context) error {
	sessionID := s.CurrentSessionID()
	if sessionID == "" {
		return errors.New("no session to subscribe to")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.baseURL+"/api/stream?session_id="+sessionID+"&follow=1", nil)
	if err != nil {
		return err
	}
	// Resume where the last connection stopped. Without this a reconnect
	// replays the session from seq 1, and every wake-up turn already delivered
	// would be delivered again.
	if lastSeq := s.lastEventSeq(); lastSeq > 0 {
		request.Header.Set("Last-Event-ID", strconv.FormatInt(lastSeq, 10))
	}
	s.authorize(request)
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("event stream returned %s", response.Status)
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var data strings.Builder
	var eventID int64
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id:"):
			if parsed, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64); err == nil {
				eventID = parsed
			}
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteString("\n")
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "":
			if data.Len() > 0 {
				s.handleStreamEvent([]byte(data.String()))
				data.Reset()
			}
			if eventID > 0 {
				s.setLastEventSeq(eventID)
			}
		}
	}
	return scanner.Err()
}

// handleStreamEvent projects one session event onto the events the engine
// already understands.
//
// Only a turn that ended is interesting: it carries reply, origin, team and
// intermediate, which is exactly what heronProgress carries on the stdio path.
// A wake-up turn is the case this whole path exists for — no request carries
// it, so without the stream it would be visible only in the event log.
func (s *httpSession) handleStreamEvent(raw []byte) {
	// A session event on the wire is the engine's EventHeader flattened at the
	// top level plus a nested "payload": the turn id and session id live in
	// the header, while reply / origin / intermediate / deliveries live in the
	// payload. Decoding only the payload leaves the turn id empty, which is
	// what the de-duplication and the log line key off.
	var event struct {
		Seq           int64           `json:"seq"`
		Type          string          `json:"type"`
		FlowSessionID string          `json:"flow_session_id"`
		FlowTurnID    string          `json:"flow_turn_id"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return
	}
	switch event.Type {
	case "flow_turn.waiting_input", "flow_turn.completed":
	default:
		return
	}

	var progress heronProgress
	if err := json.Unmarshal(event.Payload, &progress); err != nil {
		return
	}
	progress.Seq = event.Seq
	progress.FlowTurnID = event.FlowTurnID
	progress.SessionID = event.FlowSessionID
	// A wake-up turn is not an answer to anything the user said, and an
	// intermediate one is still waiting on work it dispatched. Neither is a
	// message for the chat.
	if progress.Origin != "wakeup" || progress.Intermediate {
		return
	}
	if s.turnSeen(progress.FlowTurnID) {
		return
	}
	s.markTurnSeen(progress.FlowTurnID)

	text := strings.TrimSpace(progress.Reply)
	if text == "" {
		for _, delivery := range progress.Deliveries {
			if content := strings.TrimSpace(delivery.Text); content != "" {
				text = content
				break
			}
		}
	}
	if text == "" {
		return
	}
	slog.Info("heron: delivering scheduled wake-up", "flow_turn_id", progress.FlowTurnID)
	s.emit(core.Event{
		Type:      core.EventResult,
		Content:   text,
		SessionID: s.CurrentSessionID(),
		Done:      true,
	})
}

func (s *httpSession) authorize(request *http.Request) {
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
}

func (s *httpSession) doJSON(method, path string, body io.Reader, target any) error {
	request, err := http.NewRequest(method, s.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	s.authorize(request)
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("heron serve is not reachable at %s: %w", s.baseURL, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("heron serve returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (s *httpSession) Events() <-chan core.Event { return s.events }

func (s *httpSession) CurrentSessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

func (s *httpSession) setSessionID(sessionID string) {
	s.mu.Lock()
	s.sessionID = sessionID
	s.mu.Unlock()
}

func (s *httpSession) lastEventSeq() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSeq
}

func (s *httpSession) setLastEventSeq(seq int64) {
	s.mu.Lock()
	if seq > s.lastSeq {
		s.lastSeq = seq
	}
	s.mu.Unlock()
}

// Alive reports whether the engine still answers. There is no child process to
// inspect: the session is alive exactly as long as the server it talks to is.
func (s *httpSession) Alive() bool {
	select {
	case <-s.closed:
		return false
	default:
	}
	request, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/health", nil)
	if err != nil {
		return false
	}
	s.authorize(request)
	response, err := s.client.Do(request)
	if err != nil {
		return false
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode == http.StatusOK
}

// CancelTurn asks the engine to cancel the running turn. The session stays
// usable: the conversation is a server-side object, not this connection.
func (s *httpSession) CancelTurn() {
	sessionID := s.CurrentSessionID()
	if sessionID == "" {
		return
	}
	if err := s.doJSON(http.MethodPost, "/api/cancel?session_id="+sessionID, nil, nil); err != nil {
		slog.Warn("heron: cancel turn failed", "error", err)
	}
}

// RespondPermission forwards an approval decision. Heron's HTTP API takes the
// decision as a body, not as a reply to a pending request id.
func (s *httpSession) RespondPermission(requestID string, result core.PermissionResult) error {
	body, err := json.Marshal(map[string]any{
		"request_id": requestID,
		"approved":   result.Behavior == "allow",
		"reason":     result.Message,
	})
	if err != nil {
		return err
	}
	sessionID := s.CurrentSessionID()
	path := "/api/approvals"
	if sessionID != "" {
		path += "?session_id=" + sessionID
	}
	return s.doJSON(http.MethodPost, path, bytes.NewReader(body), nil)
}

// Close stops the event stream and closes the event channel. It does not stop
// the engine: one server serves every session, and this connection is one
// client of it.
func (s *httpSession) Close() error {
	s.closeOnce.Do(func() {
		if s.streamCancel != nil {
			s.streamCancel()
		}
		close(s.closed)
		close(s.events)
	})
	return nil
}

func (s *httpSession) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *httpSession) emit(event core.Event) {
	select {
	case s.events <- event:
	case <-s.closed:
	}
}

func (s *httpSession) turnSeen(flowTurnID string) bool {
	if flowTurnID == "" {
		return false
	}
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	return s.seenTurns[flowTurnID]
}

func (s *httpSession) markTurnSeen(flowTurnID string) {
	if flowTurnID == "" {
		return
	}
	s.seenMu.Lock()
	s.seenTurns[flowTurnID] = true
	s.seenMu.Unlock()
}
