package core

// stats_engine_test.go — integration test for the turn-complete recording
// hook: a real engine turn through processInteractiveEvents must persist a
// TurnRecord via the engine's statsRecorder.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTurnCompleteRecordsMetrics(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewTurnRecorder(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetStatsRecorder(rec)

	sessionKey := "test:user1:chat1"
	session := e.sessions.GetOrCreateActive(sessionKey)
	agentSession := newControllableSession("cli-session-1")
	state := &interactiveState{
		agentSession: agentSession,
		platform:     p,
		replyCtx:     "ctx-1",
	}
	e.interactiveStates[sessionKey] = state

	// Turn identity captured at turn start (mirrors engine.go flow).
	state.mu.Lock()
	state.turnUserID = "user1"
	state.turnUserName = "张三"
	state.mu.Unlock()

	agentSession.events <- Event{Type: EventResult, Content: "done", InputTokens: 10000, OutputTokens: 2000, Done: true}
	e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m1", time.Now(), nil, nil, "ctx-1")

	files := MetricsFilesBetween(rec.Dir(), time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	recs := ReadTurnRecords(files, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Kind != RecordKindTurn || r.Project != "test" || r.SessionID != session.ID {
		t.Errorf("record identity: %+v", r)
	}
	if r.InputTokens != 10000 || r.OutputTokens != 2000 || r.TokensEstimated {
		t.Errorf("token fields: %+v", r)
	}
	if r.UserID != "user1" || r.UserName != "张三" {
		t.Errorf("user fields: %+v", r)
	}
	if r.Trigger != "user" {
		t.Errorf("trigger = %q, want user", r.Trigger)
	}
}

func TestTurnCompleteRecordsMetricsOnAgentError(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewTurnRecorder(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetStatsRecorder(rec)

	sessionKey := "test:user1:chat1"
	session := e.sessions.GetOrCreateActive(sessionKey)
	agentSession := newControllableSession("cli-session-2")
	state := &interactiveState{
		agentSession: agentSession,
		platform:     p,
		replyCtx:     "ctx-1",
	}
	e.interactiveStates[sessionKey] = state

	agentSession.events <- Event{Type: EventError, Error: errors.New("boom"), Done: true}
	e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m1", time.Now(), nil, nil, "ctx-1")

	files := MetricsFilesBetween(rec.Dir(), time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	recs := ReadTurnRecords(files, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1 (error turns count): %+v", len(recs), recs)
	}
	if recs[0].Error == "" {
		t.Errorf("error field empty: %+v", recs[0])
	}
}

func TestTurnRecorderNilOnEngine_NoPanic(t *testing.T) {
	// Engine without recorder: turn path must not record or panic.
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	sessionKey := "test:user1"
	session := e.sessions.GetOrCreateActive(sessionKey)
	agentSession := newControllableSession("cli-3")
	state := &interactiveState{agentSession: agentSession, platform: p, replyCtx: "ctx"}
	e.interactiveStates[sessionKey] = state

	agentSession.events <- Event{Type: EventResult, Content: "ok", InputTokens: 100, Done: true}
	e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m1", time.Now(), nil, nil, "ctx")
}

func TestSessionCreatedRecordedOnSpawn(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewTurnRecorder(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()

	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	e.SetStatsRecorder(rec)

	sessionKey := "feishu:ou_user1:oc_chat1"
	e.sessions.GetOrCreateActive(sessionKey)
	// getOrCreateInteractiveStateWith spawns via agent.StartSession.
	e.getOrCreateInteractiveStateWith(sessionKey, p, "ctx", e.sessions.GetOrCreateActive(sessionKey), e.sessions, nil, "")

	files := MetricsFilesBetween(rec.Dir(), time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	recs := ReadTurnRecords(files, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	found := false
	for _, r := range recs {
		if r.Kind == RecordKindSessionCreated && r.Platform == "feishu" && r.UserID == "ou_user1" {
			found = true
		}
	}
	if !found {
		t.Errorf("session_created record not found: %+v", recs)
	}
	// Sanity: metrics file exists on disk.
	if _, err := os.Stat(filepath.Join(rec.Dir(), "turns-"+time.Now().Format("2006-01-02")+".jsonl")); err != nil {
		t.Errorf("metrics file missing: %v", err)
	}
}
