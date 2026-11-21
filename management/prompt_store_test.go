package management

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── helpers ──────────────────────────────────────────────────

func newPromptTestServer(t *testing.T) (*ManagementServer, *PromptStore) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := NewPromptStore(dataDir)
	if err != nil {
		t.Fatalf("NewPromptStore: %v", err)
	}
	m := NewManagementServer(0, "", nil)
	m.SetPromptStore(store)
	return m, store
}

// promptRequest drives the real routing layer so the tests cover the
// "prompts must work without an engine" branch in handleProjectRoutes.
func promptRequest(m *ManagementServer, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	m.handleProjectRoutes(w, r)
	return w
}

func promptEnvelope(t *testing.T, w *httptest.ResponseRecorder) (ok bool, data json.RawMessage, errMsg string) {
	t.Helper()
	var resp struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return resp.OK, resp.Data, resp.Error
}

func promptList(t *testing.T, m *ManagementServer, project string) []QuickPrompt {
	t.Helper()
	w := promptRequest(m, http.MethodGet, "/api/v1/projects/"+project+"/prompts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET prompts status = %d, body = %s", w.Code, w.Body.String())
	}
	_, data, _ := promptEnvelope(t, w)
	var payload struct {
		Prompts []QuickPrompt `json:"prompts"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode prompts: %v", err)
	}
	return payload.Prompts
}

func createPrompt(t *testing.T, m *ManagementServer, project, content string) QuickPrompt {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	w := promptRequest(m, http.MethodPost, "/api/v1/projects/"+project+"/prompts", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("POST prompt status = %d, body = %s", w.Code, w.Body.String())
	}
	_, data, _ := promptEnvelope(t, w)
	var p QuickPrompt
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("decode created prompt: %v", err)
	}
	return p
}

// ── HTTP behaviour ───────────────────────────────────────────

func TestPrompts_CRUDOverHTTP(t *testing.T) {
	m, _ := newPromptTestServer(t)

	created := createPrompt(t, m, "repo-a", "review my changes")
	if created.ID == "" || created.Project != "repo-a" || created.Content != "review my changes" {
		t.Fatalf("created prompt = %+v", created)
	}

	list := promptList(t, m, "repo-a")
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list after create = %+v", list)
	}

	body, _ := json.Marshal(map[string]string{"content": "review my changes carefully"})
	w := promptRequest(m, http.MethodPatch,
		"/api/v1/projects/repo-a/prompts/"+created.ID, string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", w.Code, w.Body.String())
	}
	list = promptList(t, m, "repo-a")
	if len(list) != 1 || list[0].Content != "review my changes carefully" {
		t.Fatalf("list after update = %+v", list)
	}
	if !list[0].UpdatedAt.After(list[0].CreatedAt) && !list[0].UpdatedAt.Equal(list[0].CreatedAt) {
		t.Fatalf("updated_at went backwards: %+v", list[0])
	}

	w = promptRequest(m, http.MethodDelete,
		"/api/v1/projects/repo-a/prompts/"+created.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := promptList(t, m, "repo-a"); len(got) != 0 {
		t.Fatalf("list after delete = %+v", got)
	}
}

func TestPrompts_ScopedToProject(t *testing.T) {
	m, _ := newPromptTestServer(t)

	a := createPrompt(t, m, "repo-a", "prompt of a")
	b := createPrompt(t, m, "repo-b", "prompt of b")

	listA := promptList(t, m, "repo-a")
	if len(listA) != 1 || listA[0].ID != a.ID {
		t.Fatalf("project a list = %+v", listA)
	}
	listB := promptList(t, m, "repo-b")
	if len(listB) != 1 || listB[0].ID != b.ID {
		t.Fatalf("project b list = %+v", listB)
	}

	// An ID from another project must not be reachable, otherwise the project
	// boundary is cosmetic: the caller could edit a prompt it cannot list.
	body, _ := json.Marshal(map[string]string{"content": "hijacked"})
	w := promptRequest(m, http.MethodPatch, "/api/v1/projects/repo-b/prompts/"+a.ID, string(body))
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-project PATCH status = %d, want 404", w.Code)
	}
	w = promptRequest(m, http.MethodDelete, "/api/v1/projects/repo-b/prompts/"+a.ID, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-project DELETE status = %d, want 404", w.Code)
	}
	if got := promptList(t, m, "repo-a"); len(got) != 1 || got[0].Content != "prompt of a" {
		t.Fatalf("project a prompt was modified: %+v", got)
	}
}

func TestPrompts_OrderByCreation(t *testing.T) {
	m, _ := newPromptTestServer(t)

	first := createPrompt(t, m, "repo-a", "first")
	second := createPrompt(t, m, "repo-a", "second")
	third := createPrompt(t, m, "repo-a", "third")

	got := promptList(t, m, "repo-a")
	want := []string{first.ID, second.ID, third.ID}
	if len(got) != len(want) {
		t.Fatalf("list length = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %s, want %s", i, got[i].ID, id)
		}
	}
}

func TestPrompts_RejectsEmptyAndOversized(t *testing.T) {
	m, _ := newPromptTestServer(t)

	for _, content := range []string{"", "   ", "\n\t "} {
		body, _ := json.Marshal(map[string]string{"content": content})
		w := promptRequest(m, http.MethodPost, "/api/v1/projects/repo-a/prompts", string(body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("POST %q status = %d, want 400", content, w.Code)
		}
	}

	tooLong := strings.Repeat("a", QuickPromptMaxRunes+1)
	body, _ := json.Marshal(map[string]string{"content": tooLong})
	w := promptRequest(m, http.MethodPost, "/api/v1/projects/repo-a/prompts", string(body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST oversized status = %d, want 400", w.Code)
	}

	// The limit counts characters, not bytes: 2000 multi-byte runes fit.
	atLimit := strings.Repeat("改", QuickPromptMaxRunes)
	body, _ = json.Marshal(map[string]string{"content": atLimit})
	w = promptRequest(m, http.MethodPost, "/api/v1/projects/repo-a/prompts", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("POST at-limit status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
}

func TestPrompts_RejectsOverCountLimit(t *testing.T) {
	m, store := newPromptTestServer(t)

	for i := 0; i < QuickPromptMaxPerProject; i++ {
		if _, err := store.Create("repo-a", fmt.Sprintf("prompt %d", i)); err != nil {
			t.Fatalf("seed prompt %d: %v", i, err)
		}
	}

	body, _ := json.Marshal(map[string]string{"content": "one too many"})
	w := promptRequest(m, http.MethodPost, "/api/v1/projects/repo-a/prompts", string(body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST over count limit status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "at most") {
		t.Fatalf("error message = %s", w.Body.String())
	}

	// The cap is per project, not global.
	if _, err := store.Create("repo-b", "still allowed"); err != nil {
		t.Fatalf("second project rejected: %v", err)
	}
}

func TestPrompts_UnknownIDAndMethods(t *testing.T) {
	m, _ := newPromptTestServer(t)

	body, _ := json.Marshal(map[string]string{"content": "x"})
	if w := promptRequest(m, http.MethodPatch, "/api/v1/projects/repo-a/prompts/qp_missing", string(body)); w.Code != http.StatusNotFound {
		t.Fatalf("PATCH unknown id status = %d, want 404", w.Code)
	}
	if w := promptRequest(m, http.MethodDelete, "/api/v1/projects/repo-a/prompts/qp_missing", ""); w.Code != http.StatusNotFound {
		t.Fatalf("DELETE unknown id status = %d, want 404", w.Code)
	}
	if w := promptRequest(m, http.MethodPut, "/api/v1/projects/repo-a/prompts", "{}"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT collection status = %d, want 405", w.Code)
	}
	if w := promptRequest(m, http.MethodGet, "/api/v1/projects/repo-a/prompts/qp_x", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET item status = %d, want 405", w.Code)
	}
}

func TestPrompts_DisabledWithoutStore(t *testing.T) {
	m := NewManagementServer(0, "", nil) // no store injected

	if w := promptRequest(m, http.MethodGet, "/api/v1/projects/repo-a/prompts", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET without store status = %d, want 503", w.Code)
	}
	body, _ := json.Marshal(map[string]string{"content": "x"})
	if w := promptRequest(m, http.MethodPost, "/api/v1/projects/repo-a/prompts", string(body)); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST without store status = %d, want 503", w.Code)
	}
}

// ── store behaviour ─────────────────────────────────────────

func TestPrompts_PersistAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewPromptStore(dataDir)
	if err != nil {
		t.Fatalf("NewPromptStore: %v", err)
	}
	created, err := store.Create("repo-a", "survives a restart")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	reopened, err := NewPromptStore(dataDir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	got := reopened.List("repo-a")
	if len(got) != 1 || got[0].ID != created.ID || got[0].Content != "survives a restart" {
		t.Fatalf("after reopen = %+v", got)
	}
}

func TestPrompts_CorruptStoreIsBackedUp(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "prompts.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt store: %v", err)
	}

	store, err := NewPromptStore(dataDir)
	if err != nil {
		t.Fatalf("NewPromptStore on corrupt file: %v", err)
	}
	if got := store.List("repo-a"); len(got) != 0 {
		t.Fatalf("corrupt store should load empty, got %+v", got)
	}

	backups, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d, want 1 (glob %s)", len(backups), path+".corrupt-*")
	}
	data, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != "{not json" {
		t.Fatalf("backup content = %q", string(data))
	}

	// A save after the corrupt load must not resurrect or overwrite the old
	// bytes: they live in the backup only.
	if _, err := store.Create("repo-a", "after corruption"); err != nil {
		t.Fatalf("Create after corrupt load: %v", err)
	}
	reopened, err := NewPromptStore(dataDir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.List("repo-a"); len(got) != 1 {
		t.Fatalf("after recovery = %+v", got)
	}
}

func TestPrompts_ListReturnsCopy(t *testing.T) {
	m, store := newPromptTestServer(t)
	if _, err := store.Create("repo-a", "original"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := store.List("repo-a")
	got[0].Content = "mutated by caller"

	if again := store.List("repo-a"); again[0].Content != "original" {
		t.Fatalf("store was mutated through List result: %+v", again)
	}
	_ = m
}

func TestPrompts_UpdateDoesNotRecreateDeletedPrompt(t *testing.T) {
	_, store := newPromptTestServer(t)
	created, err := store.Create("repo-a", "temporary")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete("repo-a", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Update("repo-a", created.ID, "edited after delete"); err == nil {
		t.Fatal("Update on a deleted prompt should fail")
	}
	if got := store.List("repo-a"); len(got) != 0 {
		t.Fatalf("deleted prompt came back: %+v", got)
	}
}

// A store file that parses but contains unusable records must not be treated as
// corrupt (that would back the whole file up and start empty, losing the good
// entries). The bad records are skipped and the rest load normally.
func TestPrompts_PartialRecordsAreSkipped(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "prompts.json")
	payload := `[{"id":"qp_ok","project":"repo-a","content":"keep me"},` +
		`{"id":"","project":"repo-a","content":"missing id"},` +
		`{"id":"qp_noproject","project":"","content":"missing project"},` +
		`{"id":"qp_empty","project":"repo-a","content":"   "}]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write store: %v", err)
	}

	store, err := NewPromptStore(dataDir)
	if err != nil {
		t.Fatalf("NewPromptStore: %v", err)
	}

	got := store.List("repo-a")
	if len(got) != 1 || got[0].ID != "qp_ok" {
		t.Fatalf("loaded = %+v, want only the usable record", got)
	}
	// The file still parsed, so it must NOT be moved aside as corrupt.
	if backups, _ := filepath.Glob(path + ".corrupt-*"); len(backups) != 0 {
		t.Fatalf("unexpected corrupt backup(s): %v", backups)
	}
}
