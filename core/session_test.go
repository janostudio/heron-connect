package core

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSessionManager_GetOrCreateActive(t *testing.T) {
	sm := NewSessionManager("")
	s1 := sm.GetOrCreateActive("user1")
	if s1 == nil {
		t.Fatal("expected non-nil session")
	}
	s2 := sm.GetOrCreateActive("user1")
	if s1.ID != s2.ID {
		t.Error("same user should get same active session")
	}

	s3 := sm.GetOrCreateActive("user2")
	if s3.ID == s1.ID {
		t.Error("different user should get different session")
	}
}

func TestSessionManager_NewSession(t *testing.T) {
	sm := NewSessionManager("")
	s1 := sm.NewSession("user1", "chat-a")
	s2 := sm.NewSession("user1", "chat-b")

	if s1.ID == s2.ID {
		t.Error("new sessions should have different IDs")
	}
	if s1.Name != "chat-a" || s2.Name != "chat-b" {
		t.Error("session names should match")
	}

	active := sm.GetOrCreateActive("user1")
	if active.ID != s2.ID {
		t.Error("latest session should be active")
	}
}

func TestSessionManager_NewSideSession(t *testing.T) {
	sm := NewSessionManager("")
	main := sm.GetOrCreateActive("user1")
	side := sm.NewSideSession("user1", "cron-job")

	if side.ID == main.ID {
		t.Fatal("side session should be a new record")
	}
	if sm.ActiveSessionID("user1") != main.ID {
		t.Errorf("active session should stay main %q, got %q", main.ID, sm.ActiveSessionID("user1"))
	}
	list := sm.ListSessions("user1")
	if len(list) != 2 {
		t.Fatalf("want 2 sessions for user1, got %d", len(list))
	}
}

func TestSessionManager_SwitchSession(t *testing.T) {
	sm := NewSessionManager("")
	s1 := sm.NewSession("user1", "first")
	s2 := sm.NewSession("user1", "second")

	if sm.ActiveSessionID("user1") != s2.ID {
		t.Error("active should be s2")
	}

	switched, err := sm.SwitchSession("user1", s1.ID)
	if err != nil {
		t.Fatalf("SwitchSession: %v", err)
	}
	if switched.ID != s1.ID {
		t.Error("should have switched to s1")
	}
	if sm.ActiveSessionID("user1") != s1.ID {
		t.Error("active should now be s1")
	}
}

func TestSessionManager_SwitchByName(t *testing.T) {
	sm := NewSessionManager("")
	sm.NewSession("user1", "alpha")
	sm.NewSession("user1", "beta")

	switched, err := sm.SwitchSession("user1", "alpha")
	if err != nil {
		t.Fatalf("SwitchSession by name: %v", err)
	}
	if switched.Name != "alpha" {
		t.Error("should have switched to alpha")
	}
}

func TestSessionManager_SwitchNotFound(t *testing.T) {
	sm := NewSessionManager("")
	sm.NewSession("user1", "only")

	_, err := sm.SwitchSession("user1", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestSessionManager_ListSessions(t *testing.T) {
	sm := NewSessionManager("")
	sm.NewSession("user1", "a")
	sm.NewSession("user1", "b")
	sm.NewSession("user2", "c")

	list := sm.ListSessions("user1")
	if len(list) != 2 {
		t.Errorf("user1 should have 2 sessions, got %d", len(list))
	}

	list2 := sm.ListSessions("user2")
	if len(list2) != 1 {
		t.Errorf("user2 should have 1 session, got %d", len(list2))
	}
}

func TestSessionManager_SessionNames(t *testing.T) {
	sm := NewSessionManager("")
	sm.SetSessionName("agent-123", "my-chat")

	if got := sm.GetSessionName("agent-123"); got != "my-chat" {
		t.Errorf("got %q, want my-chat", got)
	}

	sm.SetSessionName("agent-123", "")
	if got := sm.GetSessionName("agent-123"); got != "" {
		t.Errorf("got %q, want empty after clear", got)
	}
}

func TestSessionManager_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	sm1 := NewSessionManager(path)
	sm1.NewSession("user1", "persisted")
	sm1.SetSessionName("agent-x", "custom-name")

	sm2 := NewSessionManager(path)
	list := sm2.ListSessions("user1")
	if len(list) != 1 {
		t.Fatalf("expected 1 session after reload, got %d", len(list))
	}
	if list[0].Name != "persisted" {
		t.Errorf("session name = %q, want persisted", list[0].Name)
	}
	if got := sm2.GetSessionName("agent-x"); got != "custom-name" {
		t.Errorf("session name after reload = %q, want custom-name", got)
	}
}

func TestSessionManager_SetSessionMeta_RenameAndPin(t *testing.T) {
	sm := NewSessionManager("")
	s := sm.NewSession("user1", "original")
	if s == nil || s.ID == "" {
		t.Fatal("expected a session with id")
	}

	// Rename: name updates and auto flag is cleared.
	name := "my-renamed"
	pinned := true
	ok, err := sm.SetSessionMeta(s.ID, &name, &pinned)
	if err != nil || !ok {
		t.Fatalf("SetSessionMeta() ok=%v err=%v", ok, err)
	}
	got := sm.FindByID(s.ID)
	if got.GetName() != "my-renamed" {
		t.Fatalf("name = %q, want my-renamed", got.GetName())
	}
	if got.GetNameAuto() {
		t.Fatal("expected name_auto to be cleared after manual rename")
	}
	if !got.IsPinned() {
		t.Fatal("expected session to be pinned")
	}

	// Unpin.
	f := false
	ok, _ = sm.SetSessionMeta(s.ID, nil, &f)
	if !ok {
		t.Fatal("expected unpin to succeed")
	}
	if sm.FindByID(s.ID).IsPinned() {
		t.Fatal("expected session to be unpinned")
	}
}

func TestSessionManager_SetSessionMeta_NotFound(t *testing.T) {
	sm := NewSessionManager("")
	ok, _ := sm.SetSessionMeta("nope", nil, nil)
	if ok {
		t.Fatal("expected false for unknown session")
	}
}

func TestSessionManager_SetSessionMeta_PersistsPinned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	sm1 := NewSessionManager(path)
	s := sm1.NewSession("user1", "x")
	pinned := true
	if _, err := sm1.SetSessionMeta(s.ID, nil, &pinned); err != nil {
		t.Fatal(err)
	}

	sm2 := NewSessionManager(path)
	list := sm2.ListSessions("user1")
	if len(list) != 1 {
		t.Fatalf("expected 1 session after reload, got %d", len(list))
	}
	if !list[0].IsPinned() {
		t.Fatal("expected pinned to persist after reload")
	}
}

func TestSessionManager_GetOrCreateActive_Persists(t *testing.T) {	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	sm1 := NewSessionManager(path)
	s := sm1.GetOrCreateActive("user1")
	if s == nil {
		t.Fatal("expected non-nil session")
	}

	// Reload from disk — session should survive
	sm2 := NewSessionManager(path)
	list := sm2.ListSessions("user1")
	if len(list) != 1 {
		t.Fatalf("expected 1 session after reload, got %d", len(list))
	}
	if list[0].ID != s.ID {
		t.Errorf("reloaded session ID = %q, want %q", list[0].ID, s.ID)
	}
}

func TestSession_TryLockUnlock(t *testing.T) {
	s := &Session{}
	if !s.TryLock() {
		t.Error("first TryLock should succeed")
	}
	if s.TryLock() {
		t.Error("second TryLock should fail")
	}
	s.Unlock()
	if !s.TryLock() {
		t.Error("TryLock after Unlock should succeed")
	}
}

func TestSession_Busy(t *testing.T) {
	s := &Session{}
	if s.Busy() {
		t.Error("fresh session should not be busy")
	}
	if !s.TryLock() {
		t.Fatal("TryLock should succeed")
	}
	if !s.Busy() {
		t.Error("session should be busy after TryLock")
	}
	s.Unlock()
	if s.Busy() {
		t.Error("session should not be busy after Unlock")
	}
}

func TestSession_History(t *testing.T) {
	s := &Session{}
	s.AddHistory("user", "hello")
	s.AddHistory("assistant", "hi there")
	s.AddHistory("user", "bye")

	all := s.GetHistory(0)
	if len(all) != 3 {
		t.Errorf("expected 3 entries, got %d", len(all))
	}

	last2 := s.GetHistory(2)
	if len(last2) != 2 {
		t.Errorf("expected 2 entries, got %d", len(last2))
	}
	if last2[0].Content != "hi there" {
		t.Errorf("expected 'hi there', got %q", last2[0].Content)
	}

	s.ClearHistory()
	if h := s.GetHistory(0); len(h) != 0 {
		t.Errorf("expected empty history after clear, got %d", len(h))
	}
}

func TestSession_AddHistoryWithAttachments(t *testing.T) {
	s := &Session{}
	atts := []HistoryAttachment{
		{Kind: "image", Name: "a.png", MimeType: "image/png", Path: ".heron-connect/history-attachments/s1/a.png", Size: 4},
	}
	s.AddHistoryWithAttachments("user", "look", atts)
	// Plain AddHistory still writes no attachments.
	s.AddHistory("assistant", "ok")

	all := s.GetHistory(0)
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	if len(all[0].Attachments) != 1 || all[0].Attachments[0].Name != "a.png" {
		t.Errorf("expected attachment metadata on first entry, got %+v", all[0].Attachments)
	}
	if len(all[1].Attachments) != 0 {
		t.Errorf("expected no attachments on plain entry, got %+v", all[1].Attachments)
	}
}

func TestSession_ConcurrentHistory(t *testing.T) {
	s := &Session{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.AddHistory("user", "msg")
		}()
	}
	wg.Wait()
	if h := s.GetHistory(0); len(h) != 50 {
		t.Errorf("expected 50 entries, got %d", len(h))
	}
}

func TestSession_GetAgentSessionID(t *testing.T) {
	s := &Session{}
	if got := s.GetAgentSessionID(); got != "" {
		t.Errorf("initial GetAgentSessionID = %q, want empty", got)
	}
	s.SetAgentSessionID("sess-1", "test")
	if got := s.GetAgentSessionID(); got != "sess-1" {
		t.Errorf("GetAgentSessionID = %q, want %q", got, "sess-1")
	}
}

func TestSession_SetAgentSessionID_RejectsContinueSentinel(t *testing.T) {
	s := &Session{}
	s.SetAgentSessionID("real", "ag")
	s.SetAgentSessionID(ContinueSession, "ag")
	if got := s.GetAgentSessionID(); got != "real" {
		t.Fatalf("ContinueSession must not clobber stored id, got %q", got)
	}
	s.SetAgentSessionID("", "")
	if got := s.GetAgentSessionID(); got != "" {
		t.Fatalf("expected clear, got %q", got)
	}
}

// DetachAgentSession must clear the live agent session ID (so it can't be
// resumed) but PRESERVE display/reference data: agent type, history, name, and
// past agent session IDs. Regression test for the empty-shell bug where /new
// wiped history and agent_type, making past conversations unrecoverable.
func TestSession_DetachAgentSession_PreservesHistoryAndType(t *testing.T) {
	s := &Session{Name: "my chat"}
	s.SetAgentSessionID("thread-1", "codex")
	s.AddHistory("user", "hello")
	s.AddHistory("assistant", "hi there")

	s.DetachAgentSession()

	if s.GetAgentSessionID() != "" {
		t.Fatalf("DetachAgentSession should clear AgentSessionID, got %q", s.GetAgentSessionID())
	}
	if s.GetAgentName() != "codex" {
		t.Fatalf("DetachAgentSession should preserve AgentType, got %q", s.GetAgentName())
	}
	if s.Name != "my chat" {
		t.Fatalf("DetachAgentSession should preserve Name, got %q", s.Name)
	}
	if len(s.GetHistory(0)) != 2 {
		t.Fatalf("DetachAgentSession should preserve History, got %d entries", len(s.GetHistory(0)))
	}
	if len(s.PastAgentSessionIDs) != 1 || s.PastAgentSessionIDs[0] != "thread-1" {
		t.Fatalf("DetachAgentSession should record past id, got %v", s.PastAgentSessionIDs)
	}

	// Detaching again must not duplicate or lose the past id.
	s.DetachAgentSession()
	if len(s.PastAgentSessionIDs) != 1 {
		t.Fatalf("repeated DetachAgentSession should not duplicate past id, got %v", s.PastAgentSessionIDs)
	}
}

func TestSession_CompareAndSet_ReplacesContinueSentinel(t *testing.T) {
	s := &Session{}
	s.mu.Lock()
	s.AgentSessionID = ContinueSession
	s.mu.Unlock()
	if !s.CompareAndSetAgentSessionID("uuid-1", "pi") {
		t.Fatal("expected CompareAndSet to replace erroneous ContinueSession slot")
	}
	if s.GetAgentSessionID() != "uuid-1" {
		t.Fatalf("GetAgentSessionID = %q, want uuid-1", s.GetAgentSessionID())
	}
	if s.CompareAndSetAgentSessionID("uuid-2", "pi") {
		t.Fatal("expected second CompareAndSet to fail when real id already set")
	}
}

func TestSession_SetAgentInfo_NormalizesContinueSentinel(t *testing.T) {
	s := &Session{}
	s.SetAgentInfo(ContinueSession, "pi", "n")
	if s.GetAgentSessionID() != "" {
		t.Fatalf("SetAgentInfo(ContinueSession) should store empty id, got %q", s.GetAgentSessionID())
	}
}

func TestSessionManager_Load_SanitizesContinueSentinel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	raw := `{
  "sessions": {
    "s1": {
      "id": "s1",
      "name": "default",
      "agent_session_id": "__continue__",
      "agent_type": "pi",
      "history": [],
      "created_at": "2020-01-01T00:00:00Z",
      "updated_at": "2020-01-01T00:00:00Z"
    }
  },
  "active_session": {"user1": "s1"},
  "user_sessions": {"user1": ["s1"]},
  "counter": 1
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSessionManager(path)
	s := sm.GetOrCreateActive("user1")
	if got := s.GetAgentSessionID(); got != "" {
		t.Fatalf("loaded session should clear ContinueSession, got %q", got)
	}
}

func TestSessionManager_Save_StripsContinueSentinel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	sm := NewSessionManager(path)
	sm.NewSession("u1", "x")
	s := sm.GetOrCreateActive("u1")
	s.mu.Lock()
	s.AgentSessionID = ContinueSession
	s.AgentType = "pi"
	s.mu.Unlock()
	sm.Save()
	sm2 := NewSessionManager(path)
	// Same user key should reload the same logical session without sentinel.
	s2 := sm2.GetOrCreateActive("u1")
	if got := s2.GetAgentSessionID(); got != "" {
		t.Fatalf("after save+reload want empty agent_session_id, got %q", got)
	}
}

func TestSession_GetName(t *testing.T) {
	s := &Session{Name: "test-session"}
	if got := s.GetName(); got != "test-session" {
		t.Errorf("GetName = %q, want %q", got, "test-session")
	}
}

func TestSessionManager_InvalidateForAgent(t *testing.T) {
	sm := NewSessionManager("")

	// Create sessions with different agent types
	s1 := sm.NewSession("user1", "sess1")
	s1.SetAgentSessionID("old-id-1", "opencode")

	s2 := sm.NewSession("user2", "sess2")
	s2.SetAgentSessionID("old-id-2", "claudecode")

	s3 := sm.NewSession("user3", "sess3")
	s3.SetAgentSessionID("old-id-3", "") // pre-migration, no agent type

	s4 := sm.NewSession("user4", "sess4") // no agent session ID at all

	sm.InvalidateForAgent("claudecode")

	// s1: opencode → should be invalidated
	if got := s1.GetAgentSessionID(); got != "" {
		t.Errorf("s1 (opencode) AgentSessionID = %q, want empty (should be invalidated)", got)
	}
	if s1.AgentType != "claudecode" {
		t.Errorf("s1 AgentType = %q, want %q after invalidation", s1.AgentType, "claudecode")
	}

	// s2: claudecode → should be untouched
	if got := s2.GetAgentSessionID(); got != "old-id-2" {
		t.Errorf("s2 (claudecode) AgentSessionID = %q, want %q (should be preserved)", got, "old-id-2")
	}
	if s2.AgentType != "claudecode" {
		t.Errorf("s2 AgentType = %q, want %q", s2.AgentType, "claudecode")
	}

	// s3: empty agent type → should be untouched (backward compat)
	if got := s3.GetAgentSessionID(); got != "old-id-3" {
		t.Errorf("s3 (empty type) AgentSessionID = %q, want %q (migration-safe)", got, "old-id-3")
	}
	if s3.AgentType != "" {
		t.Errorf("s3 AgentType = %q, want empty (pre-migration should be untouched)", s3.AgentType)
	}

	// s4: no agent session ID → should be untouched
	if got := s4.GetAgentSessionID(); got != "" {
		t.Errorf("s4 (no session ID) AgentSessionID = %q, want empty", got)
	}
}

func TestSessionManager_UserMeta(t *testing.T) {
	sm := NewSessionManager("")
	sm.GetOrCreateActive("feishu:oc_abc:ou_xyz")

	// Set UserName
	sm.UpdateUserMeta("feishu:oc_abc:ou_xyz", "Zhang San", "")
	meta := sm.GetUserMeta("feishu:oc_abc:ou_xyz")
	if meta == nil || meta.UserName != "Zhang San" {
		t.Errorf("expected UserName='Zhang San', got %+v", meta)
	}
	if meta.ChatName != "" {
		t.Errorf("expected empty ChatName, got %q", meta.ChatName)
	}

	// Merge: add ChatName without losing UserName
	sm.UpdateUserMeta("feishu:oc_abc:ou_xyz", "", "Test Group")
	meta = sm.GetUserMeta("feishu:oc_abc:ou_xyz")
	if meta.UserName != "Zhang San" || meta.ChatName != "Test Group" {
		t.Errorf("expected merge, got %+v", meta)
	}

	// No-op for empty values
	sm.UpdateUserMeta("feishu:oc_abc:ou_xyz", "", "")
	meta = sm.GetUserMeta("feishu:oc_abc:ou_xyz")
	if meta.UserName != "Zhang San" || meta.ChatName != "Test Group" {
		t.Errorf("expected no change, got %+v", meta)
	}

	// Unknown key returns nil
	if m := sm.GetUserMeta("nonexistent"); m != nil {
		t.Errorf("expected nil for unknown key, got %+v", m)
	}
}

func TestSessionManager_UserMetaPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	sm1 := NewSessionManager(path)
	sm1.NewSession("feishu:oc_abc:ou_xyz", "test")
	sm1.UpdateUserMeta("feishu:oc_abc:ou_xyz", "Zhang San", "Group Name")
	sm1.Save()

	sm2 := NewSessionManager(path)
	meta := sm2.GetUserMeta("feishu:oc_abc:ou_xyz")
	if meta == nil || meta.UserName != "Zhang San" || meta.ChatName != "Group Name" {
		t.Errorf("expected persisted meta, got %+v", meta)
	}
}

func TestSessionManager_DeleteByAgentSessionID(t *testing.T) {
	sm := NewSessionManager("")

	s1 := sm.NewSession("user1", "one")
	s1.SetAgentSessionID("agent-1", "codex")

	s2 := sm.NewSession("user2", "two")
	s2.SetAgentSessionID("agent-2", "codex")

	s3 := sm.NewSession("user3", "three")
	s3.SetAgentSessionID("agent-1", "codex")

	if removed := sm.DeleteByAgentSessionID("agent-1"); removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if got := sm.FindByID(s1.ID); got != nil {
		t.Fatalf("expected s1 removed, got %+v", got)
	}
	if got := sm.FindByID(s3.ID); got != nil {
		t.Fatalf("expected s3 removed, got %+v", got)
	}
	if got := sm.FindByID(s2.ID); got == nil {
		t.Fatal("expected s2 preserved")
	}
	if got := sm.ActiveSessionID("user1"); got != "" {
		t.Fatalf("user1 active session = %q, want empty", got)
	}
	if got := sm.ActiveSessionID("user3"); got != "" {
		t.Fatalf("user3 active session = %q, want empty", got)
	}
	if list := sm.ListSessions("user2"); len(list) != 1 || list[0].ID != s2.ID {
		t.Fatalf("user2 sessions = %+v, want only s2", list)
	}

	if removed := sm.DeleteByAgentSessionID("missing"); removed != 0 {
		t.Fatalf("removed missing = %d, want 0", removed)
	}
}

func TestSession_ConcurrentGetSet(t *testing.T) {
	s := &Session{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.SetAgentSessionID("id", "test")
		}()
		go func() {
			defer wg.Done()
			_ = s.GetAgentSessionID()
		}()
	}
	wg.Wait()
	if got := s.GetAgentSessionID(); got != "id" {
		t.Errorf("final GetAgentSessionID = %q, want %q", got, "id")
	}
}

func TestSessionManager_StorePath(t *testing.T) {
	sm := NewSessionManager("/var/data/sessions")
	if got := sm.StorePath(); got != "/var/data/sessions" {
		t.Errorf("StorePath() = %q, want %q", got, "/var/data/sessions")
	}

	sm2 := NewSessionManager("")
	if got := sm2.StorePath(); got != "" {
		t.Errorf("StorePath() empty = %q, want empty string", got)
	}
}

func TestKnownAgentSessionIDs(t *testing.T) {
	sm := NewSessionManager("")
	s1 := sm.NewSession("user1", "a")
	s1.SetAgentSessionID("uuid-aaa", "claude")
	s2 := sm.NewSession("user1", "b")
	s2.SetAgentSessionID("uuid-bbb", "claude")
	sm.NewSession("user1", "c") // no agent session id

	known := sm.KnownAgentSessionIDs()
	if len(known) != 2 {
		t.Fatalf("KnownAgentSessionIDs len = %d, want 2", len(known))
	}
	if _, ok := known["uuid-aaa"]; !ok {
		t.Fatal("expected uuid-aaa in known set")
	}
	if _, ok := known["uuid-bbb"]; !ok {
		t.Fatal("expected uuid-bbb in known set")
	}
}

func TestFilterOwnedSessions_FiltersUnknown(t *testing.T) {
	all := []AgentSessionInfo{
		{ID: "owned-1"},
		{ID: "external-1"},
		{ID: "owned-2"},
		{ID: "external-2"},
	}
	known := map[string]struct{}{
		"owned-1": {},
		"owned-2": {},
	}
	filtered := filterOwnedSessions(all, known)
	if len(filtered) != 2 {
		t.Fatalf("filterOwnedSessions len = %d, want 2", len(filtered))
	}
	if filtered[0].ID != "owned-1" || filtered[1].ID != "owned-2" {
		t.Fatalf("filtered = %v, want owned-1 and owned-2", filtered)
	}
}

func TestFilterOwnedSessions_EmptyKnownReturnsAll(t *testing.T) {
	all := []AgentSessionInfo{
		{ID: "session-1"},
		{ID: "session-2"},
	}
	filtered := filterOwnedSessions(all, map[string]struct{}{})
	if len(filtered) != 2 {
		t.Fatalf("filterOwnedSessions with empty known = %d, want 2", len(filtered))
	}
}

func TestSessionsFromSessionManager_returnsTrackedSessions(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	userKey := "user:alice"

	// Two sessions for the same user.
	s1 := sm.GetOrCreateActive(userKey)
	s1.SetAgentInfo("agent-sid-1", "acp", "First session")
	s2 := sm.NewSession(userKey, "second")
	s2.SetAgentInfo("agent-sid-2", "acp", "Second session")

	got := sessionsFromSessionManager(sm, "acp", userKey)
	if len(got) != 2 {
		t.Fatalf("sessionsFromSessionManager len = %d, want 2", len(got))
	}
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !ids["agent-sid-1"] || !ids["agent-sid-2"] {
		t.Fatalf("missing expected ids, got = %+v", got)
	}
}

func TestSessionsFromSessionManager_userIsolation(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	alice := "user:alice"
	bob := "user:bob"

	sa := sm.GetOrCreateActive(alice)
	sa.SetAgentInfo("agent-alice", "acp", "Alice session")

	sb := sm.GetOrCreateActive(bob)
	sb.SetAgentInfo("agent-bob", "acp", "Bob session")

	got := sessionsFromSessionManager(sm, "acp", alice)
	if len(got) != 1 || got[0].ID != "agent-alice" {
		t.Fatalf("alice should see only her session, got = %+v", got)
	}
	got = sessionsFromSessionManager(sm, "acp", bob)
	if len(got) != 1 || got[0].ID != "agent-bob" {
		t.Fatalf("bob should see only his session, got = %+v", got)
	}
}

func TestSessionsFromSessionManager_fillsSummaryAndCount(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	userKey := "user:alice"

	s := sm.GetOrCreateActive(userKey)
	s.SetAgentInfo("agent-sid", "acp", "test")
	s.AddHistory("assistant", "hello there")
	s.AddHistory("user", "帮我看看这个报错")
	s.AddHistory("assistant", "正在分析")

	got := sessionsFromSessionManager(sm, "acp", userKey)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", got[0].MessageCount)
	}
	want := "帮我看看这个报错"
	if got[0].Summary != want {
		t.Errorf("Summary = %q, want %q", got[0].Summary, want)
	}
}

func TestSessionsFromSessionManager_summaryTruncates(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	userKey := "user:alice"

	longMsg := strings.Repeat("一二三", 20) // 60 runes
	s := sm.GetOrCreateActive(userKey)
	s.SetAgentInfo("agent-sid", "acp", "test")
	s.AddHistory("user", longMsg)

	got := sessionsFromSessionManager(sm, "acp", userKey)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	r := []rune(got[0].Summary)
	// 30 runes + "…" = 31
	if len(r) != 31 {
		t.Errorf("Summary rune count = %d, want 31 (30 + ellipsis)", len(r))
	}
	if !strings.HasSuffix(got[0].Summary, "…") {
		t.Errorf("Summary should end with ellipsis, got %q", got[0].Summary)
	}
}

func TestSessionsFromSessionManager_filtersByAgentName(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	userKey := "user:alice"

	s1 := sm.GetOrCreateActive(userKey)
	s1.SetAgentInfo("agent-acp-1", "acp", "ACP session")

	s2 := sm.NewSession(userKey, "cc")
	s2.SetAgentInfo("agent-cc-1", "claudecode", "Claude session")

	got := sessionsFromSessionManager(sm, "acp", userKey)
	if len(got) != 1 || got[0].ID != "agent-acp-1" {
		t.Fatalf("sessionsFromSessionManager(acp) = %+v, want [agent-acp-1]", got)
	}
}

func TestSessionsFromSessionManager_ignoresEmptySessionID(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	userKey := "user:alice"

	// Session without agent session id
	s1 := sm.GetOrCreateActive(userKey)
	s1.SetAgentInfo("", "acp", "No agent id")

	// Session with continue sentinel
	s2 := sm.NewSession(userKey, "cont")
	s2.SetAgentInfo(ContinueSession, "acp", "Continue sentinel")

	got := sessionsFromSessionManager(sm, "acp", userKey)
	if len(got) != 0 {
		t.Fatalf("sessionsFromSessionManager = %+v, want empty", got)
	}
}

func TestSessionsFromSessionManager_emptyManager(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	got := sessionsFromSessionManager(sm, "acp", "user:nobody")
	if len(got) != 0 {
		t.Fatalf("sessionsFromSessionManager on empty = %d, want 0", len(got))
	}
}

func TestSwitchToAgentSession_PreservesOldSession(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	userKey := "user:alice"

	s1 := sm.GetOrCreateActive(userKey)
	s1.SetAgentInfo("agent-A", "claude", "session A")

	known := sm.KnownAgentSessionIDs()
	if _, ok := known["agent-A"]; !ok {
		t.Fatal("agent-A should be in KnownAgentSessionIDs before switch")
	}

	s2 := sm.SwitchToAgentSession(userKey, "agent-B", "claude", "session B")
	if s2.GetAgentSessionID() != "agent-B" {
		t.Fatalf("switched session AgentSessionID = %q, want agent-B", s2.GetAgentSessionID())
	}

	known = sm.KnownAgentSessionIDs()
	if _, ok := known["agent-A"]; !ok {
		t.Fatal("agent-A should still be in KnownAgentSessionIDs after switch")
	}
	if _, ok := known["agent-B"]; !ok {
		t.Fatal("agent-B should be in KnownAgentSessionIDs after switch")
	}
}

func TestSwitchToAgentSession_ReusesExisting(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir + "/sessions.json")
	userKey := "user:bob"

	s1 := sm.GetOrCreateActive(userKey)
	s1.SetAgentInfo("agent-A", "claude", "session A")

	sm.SwitchToAgentSession(userKey, "agent-B", "claude", "session B")

	s3 := sm.SwitchToAgentSession(userKey, "agent-A", "claude", "session A")
	if s3.ID != s1.ID {
		t.Fatalf("switching back to agent-A should reuse session %s, got %s", s1.ID, s3.ID)
	}
}

func TestPastAgentSessionIDs_ClearPreservesHistory(t *testing.T) {
	s := &Session{}
	s.SetAgentSessionID("thread-1", "codex")
	s.SetAgentSessionID("", "")

	if len(s.PastAgentSessionIDs) != 1 || s.PastAgentSessionIDs[0] != "thread-1" {
		t.Fatalf("PastAgentSessionIDs = %v, want [thread-1]", s.PastAgentSessionIDs)
	}
}

func TestPastAgentSessionIDs_ReplacePreservesHistory(t *testing.T) {
	s := &Session{}
	s.SetAgentSessionID("thread-1", "codex")
	s.SetAgentSessionID("thread-2", "codex")

	if len(s.PastAgentSessionIDs) != 1 || s.PastAgentSessionIDs[0] != "thread-1" {
		t.Fatalf("PastAgentSessionIDs = %v, want [thread-1]", s.PastAgentSessionIDs)
	}
	if s.AgentSessionID != "thread-2" {
		t.Fatalf("AgentSessionID = %q, want thread-2", s.AgentSessionID)
	}
}

func TestPastAgentSessionIDs_NoDuplicates(t *testing.T) {
	s := &Session{}
	s.SetAgentSessionID("thread-1", "codex")
	s.SetAgentSessionID("", "")
	s.SetAgentSessionID("thread-1", "codex")
	s.SetAgentSessionID("", "")

	if len(s.PastAgentSessionIDs) != 1 {
		t.Fatalf("PastAgentSessionIDs has duplicates: %v", s.PastAgentSessionIDs)
	}
}

func TestPastAgentSessionIDs_ContinueSentinelNotRecorded(t *testing.T) {
	s := &Session{}
	s.SetAgentSessionID(ContinueSession, "codex")
	s.SetAgentSessionID("real-id", "codex")
	s.SetAgentSessionID("", "")

	for _, past := range s.PastAgentSessionIDs {
		if past == ContinueSession {
			t.Fatal("ContinueSession sentinel should not be in PastAgentSessionIDs")
		}
	}
	if len(s.PastAgentSessionIDs) != 1 || s.PastAgentSessionIDs[0] != "real-id" {
		t.Fatalf("PastAgentSessionIDs = %v, want [real-id]", s.PastAgentSessionIDs)
	}
}

func TestSetAgentInfo_PreservesHistory(t *testing.T) {
	s := &Session{}
	s.SetAgentInfo("thread-1", "codex", "session 1")
	s.SetAgentInfo("thread-2", "codex", "session 2")

	if len(s.PastAgentSessionIDs) != 1 || s.PastAgentSessionIDs[0] != "thread-1" {
		t.Fatalf("SetAgentInfo PastAgentSessionIDs = %v, want [thread-1]", s.PastAgentSessionIDs)
	}
}

func TestKnownAgentSessionIDs_IncludesPast(t *testing.T) {
	sm := NewSessionManager("")
	s1 := sm.NewSession("user1", "a")
	s1.SetAgentSessionID("thread-aaa", "codex")
	s1.SetAgentSessionID("", "")

	s2 := sm.NewSession("user1", "b")
	s2.SetAgentSessionID("thread-bbb", "codex")

	known := sm.KnownAgentSessionIDs()
	if _, ok := known["thread-aaa"]; !ok {
		t.Fatal("expected thread-aaa (past ID) in known set")
	}
	if _, ok := known["thread-bbb"]; !ok {
		t.Fatal("expected thread-bbb (current ID) in known set")
	}
}

// TestKnownAgentSessionIDs_ReproducesNewCommandBug simulates the exact user
// reproduction steps: repeated /new commands progressively clear AgentSessionIDs.
// Before the PastAgentSessionIDs fix, only the latest session would remain visible.
func TestKnownAgentSessionIDs_ReproducesNewCommandBug(t *testing.T) {
	sm := NewSessionManager("")
	userKey := "user:test"

	agentSessions := []AgentSessionInfo{
		{ID: "codex-thread-1"},
		{ID: "codex-thread-2"},
		{ID: "codex-thread-3"},
	}

	s1 := sm.GetOrCreateActive(userKey)
	s1.SetAgentSessionID("codex-thread-1", "codex")

	s1.SetAgentSessionID("", "")
	s2 := sm.NewSession(userKey, "session 2")
	s2.SetAgentSessionID("codex-thread-2", "codex")

	s2.SetAgentSessionID("", "")
	s3 := sm.NewSession(userKey, "session 3")
	s3.SetAgentSessionID("codex-thread-3", "codex")

	known := sm.KnownAgentSessionIDs()
	filtered := filterOwnedSessions(agentSessions, known)

	if len(filtered) != 3 {
		t.Fatalf("filterOwnedSessions returned %d sessions, want 3 (all should be visible)\nknown IDs: %v",
			len(filtered), known)
	}
}

// TestKnownAgentSessionIDs_ResetAllSessionsBug simulates resetAllSessions
// clearing all IDs (management API provider switch). Past IDs should keep
// all sessions visible.
func TestKnownAgentSessionIDs_ResetAllSessionsBug(t *testing.T) {
	sm := NewSessionManager("")
	userKey := "user:test"

	s1 := sm.NewSession(userKey, "a")
	s1.SetAgentSessionID("thread-1", "codex")
	s2 := sm.NewSession(userKey, "b")
	s2.SetAgentSessionID("thread-2", "codex")
	s3 := sm.NewSession(userKey, "c")
	s3.SetAgentSessionID("thread-3", "codex")

	for _, s := range sm.AllSessions() {
		s.SetAgentSessionID("", "")
	}

	known := sm.KnownAgentSessionIDs()
	for _, id := range []string{"thread-1", "thread-2", "thread-3"} {
		if _, ok := known[id]; !ok {
			t.Fatalf("expected %s in known set after resetAllSessions, known = %v", id, known)
		}
	}

	agentSessions := []AgentSessionInfo{
		{ID: "thread-1"}, {ID: "thread-2"}, {ID: "thread-3"},
	}
	filtered := filterOwnedSessions(agentSessions, known)
	if len(filtered) != 3 {
		t.Fatalf("filterOwnedSessions returned %d, want 3", len(filtered))
	}
}

func TestPastAgentSessionIDs_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	sm1 := NewSessionManager(path)
	s := sm1.NewSession("user1", "test")
	s.SetAgentSessionID("thread-old", "codex")
	s.SetAgentSessionID("thread-new", "codex")
	sm1.Save()

	sm2 := NewSessionManager(path)
	known := sm2.KnownAgentSessionIDs()
	if _, ok := known["thread-old"]; !ok {
		t.Fatal("past ID thread-old not persisted/loaded")
	}
	if _, ok := known["thread-new"]; !ok {
		t.Fatal("current ID thread-new not persisted/loaded")
	}
}

// TestKnownAgentSessionIDs_LegacyDataDisablesFilter simulates loading a
// session file written by the old code (before PastAgentSessionIDs tracking).
// The filter must be disabled so sessions with lost IDs remain visible.
func TestKnownAgentSessionIDs_LegacyDataDisablesFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	legacyJSON := `{
		"sessions": {
			"s1": {"id":"s1","name":"old","agent_session_id":"","history":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
			"s2": {"id":"s2","name":"","agent_session_id":"","history":null,"created_at":"2026-01-02T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"},
			"s3": {"id":"s3","name":"active","agent_session_id":"thread-3","agent_type":"codex","history":null,"created_at":"2026-01-03T00:00:00Z","updated_at":"2026-01-03T00:00:00Z"}
		},
		"active_session": {"user1":"s3"},
		"user_sessions": {"user1":["s1","s2","s3"]},
		"counter": 3
	}`
	if err := os.WriteFile(path, []byte(legacyJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := NewSessionManager(path)
	known := sm.KnownAgentSessionIDs()

	if known != nil {
		t.Fatalf("legacy data should return nil known IDs to disable filter, got %v", known)
	}

	agentSessions := []AgentSessionInfo{
		{ID: "thread-1"}, {ID: "thread-2"}, {ID: "thread-3"},
	}
	filtered := filterOwnedSessions(agentSessions, known)
	if len(filtered) != 3 {
		t.Fatalf("filterOwnedSessions with legacy data returned %d, want 3 (all visible)", len(filtered))
	}
}

// TestKnownAgentSessionIDs_NewDataEnablesFilter verifies that data saved by
// the new code (with PastIDTracking=true) enables normal filtering.
func TestKnownAgentSessionIDs_NewDataEnablesFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	sm1 := NewSessionManager(path)
	s1 := sm1.NewSession("user1", "a")
	s1.SetAgentSessionID("thread-1", "codex")
	sm1.NewSession("user1", "b")
	sm1.Save()

	sm2 := NewSessionManager(path)
	known := sm2.KnownAgentSessionIDs()

	if known == nil {
		t.Fatal("new data should not return nil known IDs")
	}
	if _, ok := known["thread-1"]; !ok {
		t.Fatal("thread-1 should be in known set")
	}

	agentSessions := []AgentSessionInfo{
		{ID: "thread-1"}, {ID: "external-1"},
	}
	filtered := filterOwnedSessions(agentSessions, known)
	if len(filtered) != 1 || filtered[0].ID != "thread-1" {
		t.Fatalf("filterOwnedSessions should hide external session, got %v", filtered)
	}
}

// TestLegacyData_PartiallyMigratedData verifies that data saved by a prior code
// version with PastIDTracking=true but without LegacyData persistence is detected
// as legacy if untracked sessions exist (sessions that lost their IDs before
// PastAgentSessionIDs tracking was available).
func TestLegacyData_PartiallyMigratedData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	partialJSON := `{
		"sessions": {
			"s1": {"id":"s1","name":"default","agent_session_id":"","history":null,"created_at":"2026-03-26T22:25:56Z","updated_at":"2026-03-26T22:25:56Z"},
			"s2": {"id":"s2","name":"","agent_session_id":"","history":null,"created_at":"2026-04-18T09:02:57Z","updated_at":"2026-04-18T09:02:57Z"},
			"s3": {"id":"s3","name":"active","agent_session_id":"thread-active","agent_type":"codex","past_agent_session_ids":["thread-old"],"history":null,"created_at":"2026-04-20T21:50:14Z","updated_at":"2026-04-20T21:50:14Z"}
		},
		"active_session": {"user1":"s3"},
		"user_sessions":  {"user1":["s1","s2","s3"]},
		"counter": 3,
		"past_id_tracking": true
	}`
	if err := os.WriteFile(path, []byte(partialJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := NewSessionManager(path)
	known := sm.KnownAgentSessionIDs()

	if known != nil {
		t.Fatalf("partially migrated data should disable filter (return nil), got %v", known)
	}

	agentSessions := []AgentSessionInfo{
		{ID: "thread-active"}, {ID: "thread-old"}, {ID: "other-1"}, {ID: "other-2"},
	}
	filtered := filterOwnedSessions(agentSessions, known)
	if len(filtered) != 4 {
		t.Fatalf("all sessions should be visible with legacy data, got %d", len(filtered))
	}
}

// TestCmdNew_PreservesOldSessionHistoryAndType exercises the REAL cmdNew path
// (not the inlined old clearing pattern) and asserts the previously-active
// session keeps its history/agent_type/name after /new, only losing its live
// AgentSessionID. This is the regression test for the empty-shell Web bug:
// /new must not wipe the old conversation's display data.
func TestCmdNew_PreservesOldSessionHistoryAndType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	sm := NewSessionManager(path)

	e := &Engine{sessions: sm}
	e.i18n = NewI18n(LangEnglish)

	p := &stubPlatformEngine{n: "test"}
	userKey := "user:alice"
	msg := &Message{SessionKey: userKey, ReplyCtx: "ctx"}

	// Seed a live conversation on the active session.
	old := sm.GetOrCreateActive(userKey)
	old.SetAgentSessionID("thread-1", "codex")
	old.Name = "my chat"
	old.AddHistory("user", "hello")
	old.AddHistory("assistant", "hi there")
	old.AddHistory("user", "bye")
	sm.Save()

	e.cmdNew(p, msg, []string{"我的新会话"})

	// The OLD session must still carry its display/reference data.
	reloadedOld := sm.FindByID(old.ID)
	if reloadedOld == nil {
		t.Fatal("old session record disappeared after /new")
	}
	if reloadedOld.GetAgentSessionID() != "" {
		t.Fatalf("old session AgentSessionID = %q, want empty (detached)", reloadedOld.GetAgentSessionID())
	}
	if reloadedOld.GetAgentName() != "codex" {
		t.Fatalf("old session AgentType = %q, want codex (preserved)", reloadedOld.GetAgentName())
	}
	if reloadedOld.Name != "my chat" {
		t.Fatalf("old session Name = %q, want 'my chat' (preserved)", reloadedOld.Name)
	}
	if len(reloadedOld.GetHistory(0)) != 3 {
		t.Fatalf("old session History = %d entries, want 3 (preserved)", len(reloadedOld.GetHistory(0)))
	}
	if len(reloadedOld.PastAgentSessionIDs) != 1 || reloadedOld.PastAgentSessionIDs[0] != "thread-1" {
		t.Fatalf("old session PastAgentSessionIDs = %v, want [thread-1]", reloadedOld.PastAgentSessionIDs)
	}

	// A NEW active session must exist and be distinct from the old one.
	active := sm.GetOrCreateActive(userKey)
	if active.ID == old.ID {
		t.Fatal("/new should have created a new active session, not reused the old one")
	}
	if active.GetName() != "我的新会话" {
		t.Fatalf("new active session Name = %q, want '我的新会话'", active.GetName())
	}
	if active.GetAgentSessionID() != "" {
		t.Fatalf("new active session should start with empty AgentSessionID, got %q", active.GetAgentSessionID())
	}
}

// TestForceNewSession_PreservesOldSession exercises ForceNewSession (the
// programmatic /new used by the Web adapter) and asserts the same preservation
// guarantee as cmdNew.
func TestForceNewSession_PreservesOldSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	sm := NewSessionManager(path)

	e := &Engine{sessions: sm}
	e.i18n = NewI18n(LangEnglish)

	userKey := "web:admin:proj"

	old := sm.GetOrCreateActive(userKey)
	old.SetAgentSessionID("thread-web-1", "acp")
	old.Name = "previous web chat"
	old.AddHistory("user", "what is the build status?")
	old.AddHistory("assistant", "build is green")
	sm.Save()

	newSession := e.ForceNewSession(userKey, "fresh web chat")

	reloadedOld := sm.FindByID(old.ID)
	if reloadedOld == nil {
		t.Fatal("old session record disappeared after ForceNewSession")
	}
	if reloadedOld.GetAgentSessionID() != "" {
		t.Fatalf("old AgentSessionID = %q, want empty", reloadedOld.GetAgentSessionID())
	}
	if reloadedOld.GetAgentName() != "acp" {
		t.Fatalf("old AgentType = %q, want acp", reloadedOld.GetAgentName())
	}
	if reloadedOld.Name != "previous web chat" {
		t.Fatalf("old Name = %q, want 'previous web chat'", reloadedOld.Name)
	}
	if len(reloadedOld.GetHistory(0)) != 2 {
		t.Fatalf("old History = %d entries, want 2", len(reloadedOld.GetHistory(0)))
	}

	if newSession.ID == old.ID {
		t.Fatal("ForceNewSession should return a new session distinct from the old")
	}
	if newSession.GetName() != "fresh web chat" {
		t.Fatalf("new session Name = %q, want 'fresh web chat'", newSession.GetName())
	}
}

// TestCmdNew_WebPlatform_OnlyAddsConversation is the regression test for the
// "create session breaks the ongoing conversation" bug: on Web (bridge), /new
// must ONLY create a new conversation. The current conversation keeps its
// agent binding, stays the active session under its own key, and keeps its
// live interactive state (a running turn must not be killed).
func TestCmdNew_WebPlatform_OnlyAddsConversation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	sm := NewSessionManager(path)

	e := &Engine{sessions: sm}
	e.i18n = NewI18n(LangEnglish)

	p := &stubPlatformEngine{n: "bridge"}
	convKey := MintWebSessionKey("auto-bugfix")
	msg := &Message{SessionKey: convKey, Platform: "web", ReplyCtx: "ctx"}

	// Seed the ongoing conversation: agent binding, history and live state.
	old := sm.GetOrCreateActive(convKey)
	old.SetAgentSessionID("thread-1", "codebuddy")
	old.AddHistory("user", "ongoing question")
	sm.Save()
	e.interactiveStates = map[string]*interactiveState{convKey: {}}

	e.cmdNew(p, msg, nil)

	// The ongoing conversation is untouched: agent binding preserved (it must
	// stay resumable — this is what "原来的会话不能用了" regressed on)...
	reloaded := sm.FindByID(old.ID)
	if reloaded == nil {
		t.Fatal("current conversation disappeared after /new")
	}
	if got := reloaded.GetAgentSessionID(); got != "thread-1" {
		t.Fatalf("current conversation AgentSessionID = %q, want thread-1 (must stay resumable)", got)
	}
	if got := reloaded.GetAgentName(); got != "codebuddy" {
		t.Fatalf("current conversation AgentType = %q, want codebuddy", got)
	}
	if len(reloaded.GetHistory(0)) != 1 {
		t.Fatalf("current conversation History = %d entries, want 1", len(reloaded.GetHistory(0)))
	}
	// ...still the active session under its own key...
	if sm.ActiveSessionID(convKey) != old.ID {
		t.Fatalf("active session for current key = %q, want %q (must stay current)", sm.ActiveSessionID(convKey), old.ID)
	}
	// ...and its live interactive state survives (running turns are not killed).
	if _, ok := e.interactiveStates[convKey]; !ok {
		t.Fatal("interactive state for the current conversation was cleaned up; /new must not kill a running web conversation")
	}

	// The new conversation exists under a DIFFERENT freshly minted key.
	idToKey, _ := sm.SessionKeyMap()
	found := false
	for _, s := range sm.AllSessions() {
		key := idToKey[s.ID]
		if s.ID != old.ID && strings.HasPrefix(key, "bridge:web-admin:auto-bugfix:conv-") && key != convKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("/new on web should create a new conversation under a fresh minted key; got keys %v", idToKey)
	}
}

// TestForceNewSession_FreshKeyCreatesSingleSession: the Web management API
// create path passes a freshly minted conversation key. ForceNewSession must
// create exactly ONE session for it — the old GetOrCreateActive call
// materialized an empty stub session that lingered in the session list.
func TestForceNewSession_FreshKeyCreatesSingleSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	sm := NewSessionManager(path)

	e := &Engine{sessions: sm}
	e.i18n = NewI18n(LangEnglish)

	freshKey := MintWebSessionKey("auto-bugfix")
	created := e.ForceNewSession(freshKey, "")

	if created == nil {
		t.Fatal("ForceNewSession returned nil")
	}
	var underKey int
	idToKey, _ := sm.SessionKeyMap()
	for _, s := range sm.AllSessions() {
		if idToKey[s.ID] == freshKey {
			underKey++
		}
	}
	if underKey != 1 {
		t.Fatalf("fresh key should yield exactly 1 session, got %d (empty stub leak)", underKey)
	}
	if sm.ActiveSessionID(freshKey) != created.ID {
		t.Fatalf("active session for fresh key = %q, want the created %q", sm.ActiveSessionID(freshKey), created.ID)
	}
}

// TestLegacyData_ClearsAfterFirstNewCommand verifies the full migration
// lifecycle: legacy data → disable filter → /new populates PastAgentSessionIDs
// → filter re-enables on next cycle.
func TestLegacyData_ClearsAfterFirstNewCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	legacyJSON := `{
		"sessions": {
			"s1": {"id":"s1","name":"","agent_session_id":"thread-old","agent_type":"codex","history":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}
		},
		"active_session": {"user1":"s1"},
		"user_sessions": {"user1":["s1"]},
		"counter": 1
	}`
	if err := os.WriteFile(path, []byte(legacyJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := NewSessionManager(path)
	known := sm.KnownAgentSessionIDs()
	if known == nil {
		t.Log("legacy mode: filter disabled (only 1 session, OK)")
	}

	s1 := sm.GetOrCreateActive("user1")
	s1.SetAgentSessionID("", "")
	s2 := sm.NewSession("user1", "new")
	s2.SetAgentSessionID("thread-new", "codex")
	sm.Save()

	sm2 := NewSessionManager(path)
	known2 := sm2.KnownAgentSessionIDs()

	if known2 == nil {
		t.Fatal("after save with new code, known should not be nil")
	}
	if _, ok := known2["thread-old"]; !ok {
		t.Fatal("thread-old should be in known via PastAgentSessionIDs")
	}
	if _, ok := known2["thread-new"]; !ok {
		t.Fatal("thread-new should be in known as current ID")
	}
}

func TestMintWebSessionKey_ShapeAndUniqueness(t *testing.T) {
	a := MintWebSessionKey("auto-bugfix")
	b := MintWebSessionKey("auto-bugfix")
	c := MintWebSessionKey("other")

	wantPrefix := "bridge:web-admin:auto-bugfix:conv-"
	if !strings.HasPrefix(a, wantPrefix) {
		t.Errorf("key %q missing prefix %q", a, wantPrefix)
	}
	if a == b {
		t.Errorf("two MintWebSessionKey calls returned identical key %q", a)
	}
	if !strings.HasPrefix(c, "bridge:web-admin:other:conv-") {
		t.Errorf("project not reflected in key %q", c)
	}
	// The key must route to the bridge/web platform and project.
	if projectFromWebSessionKey(a) != "auto-bugfix" {
		t.Errorf("projectFromWebSessionKey(%q) = %q, want auto-bugfix", a, projectFromWebSessionKey(a))
	}
}

func TestDistinctWebConversationKeysYieldDistinctSessions(t *testing.T) {
	// The heart of the Web session-isolation fix: two conversations with
	// distinct keys must map to two distinct active agent sessions, never
	// collapse onto one.
	sm := NewSessionManager("")
	keyA := MintWebSessionKey("auto-bugfix")
	keyB := MintWebSessionKey("auto-bugfix")

	sA := sm.GetOrCreateActive(keyA)
	sB := sm.GetOrCreateActive(keyB)

	if sA == sB {
		t.Fatal("distinct conversation keys mapped to the same active session; sessions are not isolated")
	}
	if sm.ActiveSessionID(keyA) != sA.ID {
		t.Errorf("active for keyA = %q, want %q", sm.ActiveSessionID(keyA), sA.ID)
	}
	if sm.ActiveSessionID(keyB) != sB.ID {
		t.Errorf("active for keyB = %q, want %q", sm.ActiveSessionID(keyB), sB.ID)
	}
}
