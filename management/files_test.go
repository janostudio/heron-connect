package management

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

func newFileMgmtServer(t *testing.T, token string, projectWorkDir map[string]string) *httptest.Server {
	t.Helper()
	mgmt := NewManagementServer(0, token, nil)
	for name, wd := range projectWorkDir {
		mgmt.RegisterEngine(name, core.NewEngine(name, &stubAgent{}, nil, "", core.LangEnglish))
		mgmt.RegisterProjectWorkDir(name, wd)
	}
	mux := http.NewServeMux()
	ts := httptest.NewServer(mgmt.buildHandler(mux))
	t.Cleanup(ts.Close)
	return ts
}

func doGet(t *testing.T, url, token string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func TestMgmt_FileServesInline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/readme.md?token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if string(body) != "# hello\n" {
		t.Fatalf("unexpected body %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" && ct != "text/markdown" {
		t.Fatalf("unexpected content-type %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "inline" {
		t.Fatalf("expected inline disposition, got %q", cd)
	}
}

func TestMgmt_FileDownloadDisposition(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	resp, _ := doGet(t, ts.URL+"/api/v1/files/proj/report.pdf?download=1&token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "attachment; filename=\"report.pdf\"" {
		t.Fatalf("expected attachment disposition, got %q", cd)
	}
}

func TestMgmt_FileAuthRequired(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	// No token -> 401
	resp, _ := doGet(t, ts.URL+"/api/v1/files/proj/a.txt", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
	// Wrong token -> 401
	resp, _ = doGet(t, ts.URL+"/api/v1/files/proj/a.txt", "wrong")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
	}
	// Correct token via Bearer -> 200
	resp, _ = doGet(t, ts.URL+"/api/v1/files/proj/a.txt", "tok")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d", resp.StatusCode)
	}
}

func TestMgmt_FilePathTraversalBlocked(t *testing.T) {
	// Secret file outside the work dir root.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "workdir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	// Traversal via ../ should be blocked (either 400 or 403), never 200 with content.
	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/../secret.txt?token=tok", "")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected traversal to be blocked, got 200 body=%q", body)
	}
}

func TestMgmt_FileUnknownProject(t *testing.T) {
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": t.TempDir()})
	resp, _ := doGet(t, ts.URL+"/api/v1/files/nope/x.txt?token=tok", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown project, got %d", resp.StatusCode)
	}
}

func TestMgmt_FileDirListing(t *testing.T) {
	dir := t.TempDir()
	mustMkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(p string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir("docs")
	mustMkdir("src")
	mustWrite("src/app.ts")
	mustWrite("z.txt")
	mustWrite("a.md")
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/?token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for root listing, got %d: %s", resp.StatusCode, body)
	}
	var wrapped struct {
		OK   bool `json:"ok"`
		Data struct {
			Path    string `json:"path"`
			Entries []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		t.Fatalf("decode listing: %v (%s)", err, body)
	}
	if !wrapped.OK || wrapped.Data.Path != "" {
		t.Fatalf("expected ok + root path, got ok=%v path=%q", wrapped.OK, wrapped.Data.Path)
	}
	// dirs first (docs, src), then files (a.md, z.txt) — alphabetical within group.
	var names, types []string
	for _, e := range wrapped.Data.Entries {
		names = append(names, e.Name)
		types = append(types, e.Type)
	}
	wantNames := []string{"docs", "src", "a.md", "z.txt"}
	wantTypes := []string{"dir", "dir", "file", "file"}
	for i := range wantNames {
		if i >= len(names) || names[i] != wantNames[i] || types[i] != wantTypes[i] {
			t.Fatalf("listing mismatch:\n got names=%v types=%v\nwant names=%v types=%v", names, types, wantNames, wantTypes)
		}
	}
}

func TestMgmt_FileDirListingSubdir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "components"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/src?token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var wrapped struct {
		Data struct {
			Path    string `json:"path"`
			Entries []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if wrapped.Data.Path != "src" {
		t.Fatalf("expected path 'src', got %q", wrapped.Data.Path)
	}
	if len(wrapped.Data.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(wrapped.Data.Entries))
	}
	if wrapped.Data.Entries[0].Name != "components" || wrapped.Data.Entries[0].Type != "dir" {
		t.Fatalf("expected dir 'components' first, got %+v", wrapped.Data.Entries[0])
	}
	if wrapped.Data.Entries[1].Name != "main.ts" || wrapped.Data.Entries[1].Type != "file" {
		t.Fatalf("expected file 'main.ts' second, got %+v", wrapped.Data.Entries[1])
	}
}

func TestMgmt_FileDirListingEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})
	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/empty?token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var wrapped struct {
		Data struct {
			Entries []struct{ Name string } `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(wrapped.Data.Entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(wrapped.Data.Entries))
	}
}
