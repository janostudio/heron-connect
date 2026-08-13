package engine_matrix

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

// This file exercises the restart-persistence scenarios for /list and
// /switch that were previously broken for ACP-like backends:
//
//   - ACP servers (e.g. CodeBuddy) do not implement session/list, so
//     agent.ListSessions returns empty and the engine must fall back to
//     sessionsFromSessionManager.
//   - After a process restart the in-memory localSessions map is gone,
//     so the fallback is the ONLY source of truth for /list and /switch.
//
// We simulate this with a matrixAgent whose ListSessions always returns
// empty, and two engine instances sharing the same sessionStorePath.

// acpLikeAgent behaves like an ACP backend that does not support
// session/list: ListSessions returns empty, forcing the fallback path.
// StartSession records the actual session id assigned (for asserting
// resume attempts). When sessionID is non-empty, the agent "resumes"
// that session; otherwise it allocates a fresh id.
type acpLikeAgent struct {
	mu          sync.Mutex
	name        string
	startIDs    []string // actual session ids assigned by StartSession
	nextSession *acpLikeSession
}

func newACPLikeAgent(name string) *acpLikeAgent {
	return &acpLikeAgent{name: name}
}

func (a *acpLikeAgent) Name() string { return a.name }

func (a *acpLikeAgent) StartSession(_ context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := sessionID
	if id == "" {
		id = fmt.Sprintf("fresh-%d", len(a.startIDs)+1)
	}
	a.startIDs = append(a.startIDs, id)
	s := &acpLikeSession{id: id, events: make(chan core.Event, 16)}
	a.nextSession = s
	return s, nil
}

func (a *acpLikeAgent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil // simulates ACP without session/list support
}

func (a *acpLikeAgent) Stop() error { return nil }

func (a *acpLikeAgent) lastStartID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.startIDs) == 0 {
		return ""
	}
	return a.startIDs[len(a.startIDs)-1]
}

// acpLikeSession is a minimal AgentSession that emits one EventResult per Send.
type acpLikeSession struct {
	mu     sync.Mutex
	id     string
	events chan core.Event
	count  int
}

func (s *acpLikeSession) Send(_ string, _ []core.ImageAttachment, _ []core.FileAttachment) error {
	s.mu.Lock()
	s.count++
	n := s.count
	s.mu.Unlock()
	s.events <- core.Event{Type: core.EventResult, Content: "acp reply " + s.id + " #" + fmt.Sprintf("%d", n), Done: true}
	return nil
}
func (s *acpLikeSession) Events() <-chan core.Event                             { return s.events }
func (s *acpLikeSession) RespondPermission(string, core.PermissionResult) error { return nil }
func (s *acpLikeSession) CurrentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}
func (s *acpLikeSession) Alive() bool { return true }
func (s *acpLikeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.events:
	default:
		close(s.events)
	}
	return nil
}
func (s *acpLikeSession) CancelTurn() {}

// TestRestart_ListShowsSessionsFromFallback verifies that after a restart,
// /list surfaces sessions persisted by the previous engine instance even
// when the agent backend reports no sessions (ACP without session/list).
//
// Note: /new destroys the old session (clears AgentSessionID + History),
// so to have multiple survivable sessions we create them across different
// users (each user gets its own session on first message).
func TestRestart_ListShowsSessionsFromFallback(t *testing.T) {
	storePath := t.TempDir() + "/sessions.json"

	// Phase 1: two users each start a session.
	agent1 := newACPLikeAgent("acp")
	p1 := &matrixPlatform{}
	e1 := core.NewEngine("restart-test", agent1, []core.Platform{p1}, storePath, core.LangEnglish)

	mkMsgA := func(c string) *core.Message {
		return &core.Message{SessionKey: "matrix:restart:user-a", Platform: "matrix", UserID: "user-a", UserName: "A", Content: c, ReplyCtx: "rc"}
	}
	mkMsgB := func(c string) *core.Message {
		return &core.Message{SessionKey: "matrix:restart:user-b", Platform: "matrix", UserID: "user-b", UserName: "B", Content: c, ReplyCtx: "rc"}
	}

	e1.ReceiveMessage(p1, mkMsgA("alice的第一条消息"))
	p1.waitTextContaining(t, "acp reply")
	p1.clear()

	e1.ReceiveMessage(p1, mkMsgB("bob的独立对话"))
	p1.waitTextContaining(t, "acp reply")
	p1.clear()

	e1.Stop()

	// Phase 2: new engine, same storePath, fresh agent (no memory).
	agent2 := newACPLikeAgent("acp")
	p2 := &matrixPlatform{}
	e2 := core.NewEngine("restart-test", agent2, []core.Platform{p2}, storePath, core.LangEnglish)
	defer e2.Stop()

	// User A does /list — sees only their own session (isolation + fallback).
	e2.ReceiveMessage(p2, mkMsgA("/list"))
	listOut := p2.waitTextContaining(t, "msgs")
	p2.clear()

	if strings.Contains(strings.ToLower(listOut), "no session") {
		t.Fatalf("[restart] /list after restart says no sessions:\n%s", listOut)
	}
	if strings.Count(listOut, "msgs") != 1 {
		t.Fatalf("[restart] /list should show exactly 1 session for user A, got:\n%s", listOut)
	}
	if !strings.Contains(listOut, "alice的第一条消息") {
		t.Errorf("[restart] /list should show last user message as summary:\n%s", listOut)
	}
	t.Logf("[restart] /list after restart OK:\n%s", listOut)
}

// TestRestart_SwitchResumesViaLoad verifies that after a restart, /switch
// to a persisted session triggers StartSession with the stored agent
// session id (i.e. the session/load resume path).
//
// We use a single user with one session. After restart, /list shows it
// via fallback, and /switch 1 selects it. The next message triggers
// StartSession with the stored agent session id (resume).
func TestRestart_SwitchResumesViaLoad(t *testing.T) {
	storePath := t.TempDir() + "/sessions.json"

	// Phase 1: create one session, capture its agent session id.
	agent1 := newACPLikeAgent("acp")
	p1 := &matrixPlatform{}
	e1 := core.NewEngine("restart-test", agent1, []core.Platform{p1}, storePath, core.LangEnglish)
	sk := "matrix:restart:user-a"
	mkMsg := func(content string) *core.Message {
		return &core.Message{SessionKey: sk, Platform: "matrix", UserID: "user-a", UserName: "A", Content: content, ReplyCtx: "rc"}
	}

	e1.ReceiveMessage(p1, mkMsg("first message in session one"))
	p1.waitTextContaining(t, "acp reply")
	p1.clear()
	firstStartID := agent1.lastStartID()
	if firstStartID == "" {
		t.Fatal("[restart] expected a non-empty session id from phase 1")
	}
	t.Logf("[restart] phase 1 agent session id: %s", firstStartID)

	e1.Stop()

	// Phase 2: restart.
	agent2 := newACPLikeAgent("acp")
	p2 := &matrixPlatform{}
	e2 := core.NewEngine("restart-test", agent2, []core.Platform{p2}, storePath, core.LangEnglish)
	defer e2.Stop()

	// /list to confirm the session is visible via fallback.
	e2.ReceiveMessage(p2, mkMsg("/list"))
	listOut := p2.waitTextContaining(t, "msgs")
	p2.clear()
	if strings.Count(listOut, "msgs") != 1 {
		t.Fatalf("[restart] /list should show exactly 1 session:\n%s", listOut)
	}

	// /switch 1 — should resolve via fallback to the persisted session.
	e2.ReceiveMessage(p2, mkMsg("/switch 1"))
	switchOut := p2.waitTextContaining(t, "switch")
	p2.clear()
	t.Logf("[restart] /switch 1 output: %s", switchOut)
	if strings.Contains(strings.ToLower(switchOut), "no session matching") {
		t.Fatalf("[restart] /switch 1 failed after restart (fallback not working):\n%s", switchOut)
	}

	// Verify the agent received the stored session id for resume. The engine
	// calls StartSession lazily on the next message (not during /switch
	// itself), so send a message to trigger it.
	agent2.mu.Lock()
	agent2.startIDs = nil // reset to isolate the resume call
	agent2.mu.Unlock()
	e2.ReceiveMessage(p2, mkMsg("resume test message"))
	p2.waitTextContaining(t, "acp reply")
	p2.clear()

	gotResume := agent2.lastStartID()
	if gotResume == "" {
		t.Fatal("[restart] StartSession was not called after /switch + message")
	}
	if gotResume != firstStartID {
		t.Errorf("[restart] StartSession resume id = %q, want %q (the persisted session id)", gotResume, firstStartID)
	}
}

// TestRestart_UserIsolation verifies that after a restart, user A cannot
// see or switch to user B's sessions via the fallback path.
func TestRestart_UserIsolation(t *testing.T) {
	storePath := t.TempDir() + "/sessions.json"

	// Phase 1: user A and user B each create a session.
	agent1 := newACPLikeAgent("acp")
	p1 := &matrixPlatform{}
	e1 := core.NewEngine("iso-test", agent1, []core.Platform{p1}, storePath, core.LangEnglish)

	skA := "matrix:iso:user-a"
	skB := "matrix:iso:user-b"
	mkMsgA := func(c string) *core.Message {
		return &core.Message{SessionKey: skA, Platform: "matrix", UserID: "user-a", UserName: "A", Content: c, ReplyCtx: "rc"}
	}
	mkMsgB := func(c string) *core.Message {
		return &core.Message{SessionKey: skB, Platform: "matrix", UserID: "user-b", UserName: "B", Content: c, ReplyCtx: "rc"}
	}

	e1.ReceiveMessage(p1, mkMsgA("alice的秘密会话"))
	p1.waitTextContaining(t, "acp reply")
	p1.clear()

	e1.ReceiveMessage(p1, mkMsgB("bob的独立会话"))
	p1.waitTextContaining(t, "acp reply")
	p1.clear()

	e1.Stop()

	// Phase 2: restart, user A does /list.
	agent2 := newACPLikeAgent("acp")
	p2 := &matrixPlatform{}
	e2 := core.NewEngine("iso-test", agent2, []core.Platform{p2}, storePath, core.LangEnglish)
	defer e2.Stop()

	e2.ReceiveMessage(p2, mkMsgA("/list"))
	listA := p2.waitTextContaining(t, "msgs")
	p2.clear()
	t.Logf("[iso] user A /list:\n%s", listA)

	// User A should see only their own session.
	if strings.Contains(listA, "bob的独立会话") {
		t.Errorf("[iso] user A should NOT see user B's session summary:\n%s", listA)
	}
	if !strings.Contains(listA, "alice的秘密会话") {
		t.Errorf("[iso] user A should see their own session summary:\n%s", listA)
	}
	countA := strings.Count(listA, "msgs")
	if countA != 1 {
		t.Errorf("[iso] user A should see exactly 1 session, got %d:\n%s", countA, listA)
	}

	// User A attempts /switch to a number that would only match if B's
	// sessions were visible. With only 1 session, /switch 2 must fail.
	e2.ReceiveMessage(p2, mkMsgA("/switch 2"))
	switchOut := p2.waitTextContaining(t, "match")
	p2.clear()
	if !strings.Contains(strings.ToLower(switchOut), "no session matching") {
		t.Errorf("[iso] /switch 2 should be no-match for user A (only 1 session visible):\n%s", switchOut)
	}
}

// TestRestart_EmptySessionNotShown verifies the edge case where a
// persisted session has no AgentSessionID (e.g. created via /new but the
// agent never started). Such sessions should NOT appear in /list fallback
// because they cannot be resumed (no agent session id to load).
//
// We use two users: user A sends a real message (session has AgentSessionID),
// user B only runs /new (session has no AgentSessionID). After restart,
// user A's /list should show only their own session, and user B's /list
// should show nothing (their session is empty).
func TestRestart_EmptySessionNotShown(t *testing.T) {
	storePath := t.TempDir() + "/sessions.json"

	agent1 := newACPLikeAgent("acp")
	p1 := &matrixPlatform{}
	e1 := core.NewEngine("empty-test", agent1, []core.Platform{p1}, storePath, core.LangEnglish)

	mkMsgA := func(c string) *core.Message {
		return &core.Message{SessionKey: "matrix:empty:user-a", Platform: "matrix", UserID: "user-a", UserName: "A", Content: c, ReplyCtx: "rc"}
	}
	mkMsgB := func(c string) *core.Message {
		return &core.Message{SessionKey: "matrix:empty:user-b", Platform: "matrix", UserID: "user-b", UserName: "B", Content: c, ReplyCtx: "rc"}
	}

	// User A: real session with a message.
	e1.ReceiveMessage(p1, mkMsgA("real message from alice"))
	p1.waitTextContaining(t, "acp reply")
	p1.clear()

	// User B: /new only, no message — session has no AgentSessionID.
	e1.ReceiveMessage(p1, mkMsgB("/new bobs-empty"))
	p1.waitTextContaining(t, "bobs-empty")
	p1.clear()

	e1.Stop()

	// Restart.
	agent2 := newACPLikeAgent("acp")
	p2 := &matrixPlatform{}
	e2 := core.NewEngine("empty-test", agent2, []core.Platform{p2}, storePath, core.LangEnglish)
	defer e2.Stop()

	// User A /list — sees their own real session only.
	e2.ReceiveMessage(p2, mkMsgA("/list"))
	listA := p2.waitTextContaining(t, "msgs")
	p2.clear()
	t.Logf("[empty] user A /list:\n%s", listA)
	if got := strings.Count(listA, "msgs"); got != 1 {
		t.Errorf("[empty] user A should see exactly 1 session, got %d:\n%s", got, listA)
	}
	if !strings.Contains(listA, "real message from alice") {
		t.Errorf("[empty] user A should see their session summary:\n%s", listA)
	}

	// User B /list — their empty session (no AgentSessionID) is filtered out.
	e2.ReceiveMessage(p2, mkMsgB("/list"))
	listB := p2.snapshot()
	p2.clear()
	t.Logf("[empty] user B /list:\n%s", listB)
	if strings.Contains(strings.ToLower(strings.Join(listB, " ")), "msgs") {
		t.Errorf("[empty] user B should see no sessions (empty session filtered):\n%s", listB)
	}
}

// TestRestart_MessageCountAndSummary verifies the P1 fill: after restart,
// /list shows the correct message count and the last user message as summary.
func TestRestart_MessageCountAndSummary(t *testing.T) {
	storePath := t.TempDir() + "/sessions.json"

	agent1 := newACPLikeAgent("acp")
	p1 := &matrixPlatform{}
	e1 := core.NewEngine("count-test", agent1, []core.Platform{p1}, storePath, core.LangEnglish)
	sk := "matrix:count:user-a"
	mkMsg := func(c string) *core.Message {
		return &core.Message{SessionKey: sk, Platform: "matrix", UserID: "user-a", UserName: "A", Content: c, ReplyCtx: "rc"}
	}

	// Send 2 user messages (each produces a user + assistant history entry).
	e1.ReceiveMessage(p1, mkMsg("第一条用户消息"))
	p1.waitTextContaining(t, "acp reply")
	p1.clear()
	e1.ReceiveMessage(p1, mkMsg("最后一条用户消息"))
	p1.waitTextContaining(t, "acp reply")
	p1.clear()

	e1.Stop()

	// Restart.
	agent2 := newACPLikeAgent("acp")
	p2 := &matrixPlatform{}
	e2 := core.NewEngine("count-test", agent2, []core.Platform{p2}, storePath, core.LangEnglish)
	defer e2.Stop()

	e2.ReceiveMessage(p2, mkMsg("/list"))
	listOut := p2.waitTextContaining(t, "msgs")
	p2.clear()
	t.Logf("[count] /list:\n%s", listOut)

	// Summary should be the LAST user message, not the first.
	if !strings.Contains(listOut, "最后一条用户消息") {
		t.Errorf("[count] /list summary should show last user message:\n%s", listOut)
	}
	if strings.Contains(listOut, "第一条用户消息") {
		t.Errorf("[count] /list summary should NOT show first user message:\n%s", listOut)
	}

	// MessageCount should be 4 (2 user + 2 assistant). The list formats it
	// as "**4** msgs" with markdown bold; strip "*" for a robust check.
	stripped := strings.ReplaceAll(listOut, "*", "")
	if !strings.Contains(stripped, "4 msgs") {
		t.Errorf("[count] /list should show 4 msgs (2 user + 2 assistant):\n%s", listOut)
	}
}
