package management

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janostudio/heron-connect/core"
	// Registers the REAL embedded web/dist via its init(). Imported here (not
	// in production code) so TestRealHTTPShareFlow exercises the actual
	// bundle; the web package depends only on core, so there is no cycle.
	_ "github.com/janostudio/heron-connect/web"
)

// ── test helpers ──────────────────────────────────────────────

// installFakeSharePage registers a minimal stand-in for the embedded
// share.html.
//
// The management package's tests never import the web package, so no embedded
// frontend is registered and core.GetWebAssets() returns nil. Without a page
// the viewer branch would silently fall through to raw bytes and every
// navigation test would pass for the wrong reason. Registering a fake page
// makes the branch under test actually execute.
func installFakeSharePage(t *testing.T) {
	t.Helper()
	// Restore whatever was registered before (the real embedded bundle in this
	// build) rather than nil — blanking it would break any later test that
	// needs the real assets.
	prev := core.GetWebAssets()
	fsys := fstest.MapFS{
		"share.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><body><div id="share-root"></div>` +
				`<script type="module" src="/assets/share.js"></script></body></html>`),
		},
	}
	core.RegisterWebAssets(fsys)
	t.Cleanup(func() { core.RegisterWebAssets(prev) })
}

// newShareMgmtServer builds a management server with both a work dir and a
// share store, wired exactly as main.go does it.
func newShareMgmtServer(t *testing.T, token string, projectWorkDir map[string]string) (*httptest.Server, *ShareStore) {
	t.Helper()
	installFakeSharePage(t)
	store, err := NewShareStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewShareStore: %v", err)
	}
	mgmt := NewManagementServer(0, token, nil)
	mgmt.SetShareStore(store)
	for name, wd := range projectWorkDir {
		mgmt.RegisterProjectWorkDir(name, wd)
	}
	mux := http.NewServeMux()
	ts := httptest.NewServer(mgmt.buildHandler(mux))
	t.Cleanup(ts.Close)
	return ts, store
}

func doReq(t *testing.T, method, url, token string, body []byte) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	return resp, got
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func createShareViaAPI(t *testing.T, ts *httptest.Server, token, project, path string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"project": project, "path": path})
	resp, raw := doReq(t, http.MethodPost, ts.URL+"/api/v1/share", token, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create share: expected 200, got %d: %s", resp.StatusCode, raw)
	}
	var env struct {
		Data struct {
			Token string `json:"token"`
			URL   string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode create response: %v (%s)", err, raw)
	}
	if env.Data.Token == "" {
		t.Fatalf("create share returned empty token: %s", raw)
	}
	return env.Data.Token
}

// ── the public read path ──────────────────────────────────────

// TestShare_PublicDownloadWithoutToken is the whole point of the feature: a
// recipient with only the link and no credentials gets the file.
func TestShare_PublicDownloadWithoutToken(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "# shared\n")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})

	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	// No Authorization header, no ?token= — the link alone must work.
	resp, body := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+token, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 without any auth, got %d: %s", resp.StatusCode, body)
	}
	if string(body) != "# shared\n" {
		t.Fatalf("unexpected body %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("unexpected content-type %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "inline" {
		t.Fatalf("expected inline disposition, got %q", cd)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
}

func TestShare_DownloadForcedAttachment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	resp, _ := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+token+"?download=1", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="notes.md"` {
		t.Fatalf("expected attachment disposition, got %q", cd)
	}
}

func TestShare_UnknownTokenIs404(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})

	resp, _ := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/deadbeef", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown token, got %d", resp.StatusCode)
	}
}

func TestShare_RevokedTokenIs404(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	// Works before revocation.
	resp, _ := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+token, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 before revoke, got %d", resp.StatusCode)
	}

	resp, body := doReq(t, http.MethodDelete, ts.URL+"/api/v1/share/"+token, "tok", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d: %s", resp.StatusCode, body)
	}

	resp, _ = doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+token, "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after revoke, got %d", resp.StatusCode)
	}
}

// TestShare_RevokeDoesNotAffectOtherShares guards against a revoke that wipes
// more than the one token it was given.
func TestShare_RevokeDoesNotAffectOtherShares(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "AAA")
	writeFile(t, dir, "b.md", "BBB")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})

	tokenA := createShareViaAPI(t, ts, "tok", "proj", "a.md")
	tokenB := createShareViaAPI(t, ts, "tok", "proj", "b.md")

	if _, body := doReq(t, http.MethodDelete, ts.URL+"/api/v1/share/"+tokenA, "tok", nil); len(body) == 0 {
		t.Fatal("expected a revoke response body")
	}

	if resp, _ := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+tokenA, "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked share should be 404, got %d", resp.StatusCode)
	}
	resp, body := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+tokenB, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unrelated share must survive revoke, got %d", resp.StatusCode)
	}
	if string(body) != "BBB" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestShare_DeletedFileIs404(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "gone.md", "x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "gone.md")

	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}

	resp, _ := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+token, "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after the file is deleted, got %d", resp.StatusCode)
	}
}

// ── auth boundary: the most important tests here ──────────────
//
// handleShareRoutes is registered WITHOUT m.wrap, so it must authenticate the
// management operations itself. If that guard is ever dropped, an anonymous
// caller could mint or enumerate share links.

func TestShare_ManagementOpsRequireAuth(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "a.md")

	body, _ := json.Marshal(map[string]string{"project": "proj", "path": "a.md"})

	tests := []struct {
		name, method, path string
		body               []byte
	}{
		{"create", http.MethodPost, "/api/v1/share", body},
		{"list all", http.MethodGet, "/api/v1/share", nil},
		{"list by project", http.MethodGet, "/api/v1/share?project=proj", nil},
		{"revoke", http.MethodDelete, "/api/v1/share/" + token, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := doReq(t, tc.method, ts.URL+tc.path, "", tc.body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s %s without token: expected 401, got %d: %s", tc.method, tc.path, resp.StatusCode, raw)
			}
		})
	}

	// And they still work with the token — the guard must not be a blanket deny.
	resp, raw := doReq(t, http.MethodGet, ts.URL+"/api/v1/share?project=proj", "tok", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list with token: expected 200, got %d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), token) {
		t.Fatalf("list should include the created share: %s", raw)
	}
}

// TestShare_PublicReadIsTheOnlyPublicAction pins the public surface to exactly
// one verb+path. Broadening it silently is the main risk of this design.
func TestShare_PublicReadIsTheOnlyPublicAction(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "a.md")

	// A token-bearing path with a non-GET verb must NOT be public.
	resp, _ := doReq(t, http.MethodDelete, ts.URL+"/api/v1/share/"+token, "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous DELETE must be 401, got %d", resp.StatusCode)
	}
	// HEAD/POST on the token path is not part of the public contract either.
	resp, _ = doReq(t, http.MethodPost, ts.URL+"/api/v1/share/"+token, "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous POST on token path must be 401, got %d", resp.StatusCode)
	}
}

// ── input validation & traversal ──────────────────────────────

func TestShare_CreateRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})

	tests := []struct {
		name, body string
		want       int
	}{
		{"empty project", `{"project":"","path":"a.md"}`, http.StatusBadRequest},
		{"empty path", `{"project":"proj","path":""}`, http.StatusBadRequest},
		{"unknown project", `{"project":"nope","path":"a.md"}`, http.StatusNotFound},
		{"missing file", `{"project":"proj","path":"ghost.md"}`, http.StatusNotFound},
		{"invalid json", `{`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := doReq(t, http.MethodPost, ts.URL+"/api/v1/share", "tok", []byte(tc.body))
			if resp.StatusCode != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, resp.StatusCode, raw)
			}
		})
	}
}

func TestShare_CannotShareDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})

	body, _ := json.Marshal(map[string]string{"project": "proj", "path": "sub"})
	resp, raw := doReq(t, http.MethodPost, ts.URL+"/api/v1/share", "tok", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when sharing a directory, got %d: %s", resp.StatusCode, raw)
	}
}

// TestShare_NoDirectoryListingViaShareToken verifies a share can never be used
// to enumerate a directory, even if a directory share somehow got persisted.
func TestShare_NoDirectoryListingViaShareToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "sub/inner.md", "inner")
	ts, store := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})

	// Bypass the API's validation to simulate a bad/legacy record.
	sh, err := store.Create("proj", "sub", "sub")
	if err != nil {
		t.Fatal(err)
	}

	resp, body := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+sh.Token, "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a share pointing at a directory must 404, got %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "inner.md") {
		t.Fatal("share leaked a directory listing")
	}
}

// TestShare_TraversalRecordCannotEscapeWorkDir is the security-critical case:
// a share record carrying ../ must not read outside the project root.
//
// The secret lives in the work dir's ACTUAL parent so that "../secret.txt"
// genuinely resolves to an existing readable file. Putting it in an unrelated
// t.TempDir() would make the test pass merely because the target does not
// exist — a false green that survives deleting the guard entirely.
func TestShare_TraversalRecordCannotEscapeWorkDir(t *testing.T) {
	base := t.TempDir()
	workDir := filepath.Join(base, "workdir")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Sits directly above the work dir root.
	secretPath := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Precondition: the traversal target really is reachable by path, so a
	// passing test means the guard worked rather than the file being missing.
	if _, err := os.Stat(filepath.Join(workDir, "..", "secret.txt")); err != nil {
		t.Fatalf("test setup: traversal target must exist: %v", err)
	}

	ts, store := newShareMgmtServer(t, "tok", map[string]string{"proj": workDir})

	for _, rel := range []string{"../secret.txt", "../../secret.txt", "sub/../../secret.txt"} {
		sh, err := store.Create("proj", rel, "secret.txt")
		if err != nil {
			t.Fatal(err)
		}
		resp, body := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+sh.Token, "", nil)
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("traversal %q escaped the work dir: %s", rel, body)
		}
		if strings.Contains(string(body), "TOP SECRET") {
			t.Fatalf("traversal %q leaked the file contents", rel)
		}
	}
}

// TestShare_CannotReachOtherFilesByEditingURL confirms the token is bound to
// one file: swapping the path in the URL is not a thing (there is no path
// segment), and appending one must not widen access.
func TestShare_CannotReachOtherFilesByEditingURL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "AAA")
	writeFile(t, dir, "b.md", "BBB")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "a.md")

	for _, suffix := range []string{"/b.md", "/../b.md", "%2F..%2Fb.md"} {
		resp, body := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+token+suffix, "", nil)
		if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "BBB") {
			t.Fatalf("suffix %q reached another file: %s", suffix, body)
		}
	}
}

// ── Accept-based content negotiation ──────────────────────────
//
// The share URL serves two different things depending on who asks: a rendered
// viewer page for a browser navigation, raw bytes for everything else. Links
// had already been handed out before the viewer existed, so the raw branch is
// the compatibility contract — breaking it would silently change what every
// previously shared link returns to curl, wget and download managers.

func doGetAccept(t *testing.T, url, accept string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// TestShare_RawBytesForNonBrowserClients is the regression guard for every
// link already shared: a plain HTTP client must still get the file itself.
func TestShare_RawBytesForNonBrowserClients(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "# Heading\n\nBODY-TEXT\n")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	for _, accept := range []string{"*/*", "text/plain", ""} {
		resp, body := doGetAccept(t, ts.URL+"/api/v1/share/"+token, accept)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("accept %q: expected 200, got %d", accept, resp.StatusCode)
		}
		if string(body) != "# Heading\n\nBODY-TEXT\n" {
			t.Fatalf("accept %q: expected raw markdown, got %q", accept, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
			t.Fatalf("accept %q: expected markdown content-type, got %q", accept, ct)
		}
	}
}

// TestShare_BrowserNavigationGetsViewerPage is the feature itself.
func TestShare_BrowserNavigationGetsViewerPage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "# Heading\n\nBODY-TEXT\n")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	resp, body := doGetAccept(t, ts.URL+"/api/v1/share/"+token, "text/html,application/xhtml+xml,*/*;q=0.8")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected an HTML page, got %q", ct)
	}
	// The page must not be the raw markdown — that is the bug being fixed.
	if strings.Contains(string(body), "BODY-TEXT") {
		t.Error("viewer page leaked the raw file content instead of rendering it")
	}
	// It must carry the share identity for the client-side viewer to fetch with.
	if !strings.Contains(string(body), "data-share-token=\""+token+"\"") {
		t.Error("viewer page is missing the share token attribute")
	}
	if !strings.Contains(string(body), "data-share-name=\"notes.md\"") {
		t.Error("viewer page is missing the file name attribute")
	}
}

// TestShare_RawParamAlwaysServesBytes: the viewer fetches with ?raw=1, and that
// must beat the Accept header (fetch sends */*, but a browser-driven fetch can
// still carry text/html in some setups).
func TestShare_RawParamAlwaysServesBytes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "RAW-CONTENT")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	resp, body := doGetAccept(t, ts.URL+"/api/v1/share/"+token+"?raw=1", "text/html")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "RAW-CONTENT" {
		t.Fatalf("?raw=1 must return the file bytes, got %q", body)
	}
}

// TestShare_DownloadParamBeatsHTMLAccept: ?download=1 is an explicit request for
// the file and must win over a browser-style Accept header.
func TestShare_DownloadParamBeatsHTMLAccept(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "RAW-CONTENT")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	resp, body := doGetAccept(t, ts.URL+"/api/v1/share/"+token+"?download=1", "text/html,application/xhtml+xml")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "RAW-CONTENT" {
		t.Fatalf("?download=1 must return the file bytes, got %q", body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("expected attachment disposition, got %q", cd)
	}
}

// TestShare_ViewerPageHasStrictCSP: the page renders agent-produced content, so
// it must not be able to run arbitrary scripts or reach remote origins.
func TestShare_ViewerPageHasStrictCSP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "# x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	resp, _ := doGetAccept(t, ts.URL+"/api/v1/share/"+token, "text/html")
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("viewer page must set a Content-Security-Policy")
	}
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
	// 'unsafe-eval' would let injected content escape the sandbox of intent.
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP must not allow unsafe-eval: %s", csp)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected nosniff, got %q", got)
	}
}

// TestShare_ViewerEscapesShareName: the file name lands in an HTML attribute,
// so a crafted name must not be able to break out of it.
func TestShare_ViewerEscapesShareName(t *testing.T) {
	dir := t.TempDir()
	// A file whose name contains characters that would terminate the attribute.
	name := `ev"il<>&.md`
	writeFile(t, dir, name, "x")
	ts, store := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})

	sh, err := store.Create("proj", name, name)
	if err != nil {
		t.Fatal(err)
	}
	resp, body := doGetAccept(t, ts.URL+"/api/v1/share/"+sh.Token, "text/html")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if strings.Contains(string(body), `<>&.md"`) {
		t.Error("file name was injected into the page unescaped")
	}
}

// TestShare_RevokedAndUnknownTokensStill404ForBrowsers: the viewer path must
// not become an oracle that reveals whether a token once existed.
func TestShare_RevokedAndUnknownTokensStill404ForBrowsers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "x")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "notes.md")

	if r, _ := doReq(t, http.MethodDelete, ts.URL+"/api/v1/share/"+token, "tok", nil); r.StatusCode != http.StatusOK {
		t.Fatalf("revoke failed: %d", r.StatusCode)
	}
	for _, tok := range []string{token, "deadbeefdeadbeef"} {
		resp, _ := doGetAccept(t, ts.URL+"/api/v1/share/"+tok, "text/html")
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("token %q with browser Accept: expected 404, got %d", tok, resp.StatusCode)
		}
	}
}

// ── ShareStore unit tests ─────────────────────────────────────

func TestShareStore_CreateGetRevoke(t *testing.T) {
	dir := t.TempDir()
	s, err := NewShareStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	sh, err := s.Create("proj", "a/b.md", "b.md")
	if err != nil {
		t.Fatal(err)
	}
	if sh.Token == "" {
		t.Fatal("empty token")
	}

	got, ok := s.Get(sh.Token)
	if !ok {
		t.Fatal("Get after Create should hit")
	}
	if got.Project != "proj" || got.RelPath != "a/b.md" || got.FileName != "b.md" {
		t.Fatalf("unexpected record: %+v", got)
	}

	if !s.Revoke(sh.Token) {
		t.Fatal("Revoke should report true for a known token")
	}
	if _, ok := s.Get(sh.Token); ok {
		t.Fatal("Revoke should remove the share")
	}
	if s.Revoke(sh.Token) {
		t.Fatal("Revoke should report false the second time")
	}
}

// TestShareStore_PersistsAcrossReload simulates a restart: links must survive.
func TestShareStore_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	s1, err := NewShareStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	keep, err := s1.Create("proj", "keep.md", "keep.md")
	if err != nil {
		t.Fatal(err)
	}
	drop, err := s1.Create("proj", "drop.md", "drop.md")
	if err != nil {
		t.Fatal(err)
	}
	if !s1.Revoke(drop.Token) {
		t.Fatal("revoke failed")
	}

	s2, err := NewShareStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get(keep.Token); !ok {
		t.Fatal("share should survive a restart")
	}
	if _, ok := s2.Get(drop.Token); ok {
		t.Fatal("revoked share must stay revoked after a restart")
	}
}

// TestShareStore_ToleratesCorruptFile: a damaged file must not block startup.
func TestShareStore_ToleratesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shares"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shares", "shares.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewShareStore(dir)
	if err != nil {
		t.Fatalf("corrupt file must not fail construction: %v", err)
	}
	if _, ok := s.Get("anything"); ok {
		t.Fatal("expected an empty store")
	}
}

func TestShareStore_ListByProject(t *testing.T) {
	s, err := NewShareStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("p1", "a.md", "a.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("p2", "b.md", "b.md"); err != nil {
		t.Fatal(err)
	}

	if got := s.ListByProject("p1"); len(got) != 1 || got[0].Project != "p1" {
		t.Fatalf("ListByProject(p1) = %+v", got)
	}
	if got := s.ListByProject(""); len(got) != 2 {
		t.Fatalf("ListByProject(\"\") should return all, got %d", len(got))
	}
}

// TestShareStore_DistinctTokens guards against a token collision returning the
// wrong file.
func TestShareStore_DistinctTokens(t *testing.T) {
	s, err := NewShareStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		sh, err := s.Create("proj", "f.md", "f.md")
		if err != nil {
			t.Fatal(err)
		}
		if seen[sh.Token] {
			t.Fatalf("duplicate token %q", sh.Token)
		}
		seen[sh.Token] = true
	}
}

// TestShare_DisabledStoreReturns503: without a store the endpoints must refuse
// rather than mint links that could never be served.
func TestShare_DisabledStoreReturns503(t *testing.T) {
	mgmt := NewManagementServer(0, "tok", nil)
	mux := http.NewServeMux()
	ts := httptest.NewServer(mgmt.buildHandler(mux))
	defer ts.Close()

	resp, _ := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/whatever", "", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without a share store, got %d", resp.StatusCode)
	}
}

// TestShare_NotSwallowedBySPAFallback is a regression guard for the routing
// trap: anything outside /api/ falls through to index.html, so the share
// endpoint must stay under /api/ and return file bytes, not the SPA shell.
func TestShare_NotSwallowedBySPAFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "FILE-BYTES")
	ts, _ := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	token := createShareViaAPI(t, ts, "tok", "proj", "a.md")

	resp, body := doReq(t, http.MethodGet, ts.URL+"/api/v1/share/"+token, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/html") {
		t.Fatalf("share endpoint served HTML (SPA fallback swallowed it): %q", ct)
	}
	if string(body) != "FILE-BYTES" {
		t.Fatalf("expected raw file bytes, got %q", body)
	}
}

// ── end-to-end ────────────────────────────────────────────────

// TestShare_EndToEndFullFlow walks the whole feature through the real handler
// chain (buildHandler + withStaticFallback) exactly as a browser and an
// anonymous recipient hit it: create, anonymous read, isolation, revoke.
// The per-piece tests above cannot catch wiring mistakes that only surface
// once the pieces are assembled.
func TestShare_EndToEndFullFlow(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "reports"), "q3.md", "# Q3 Report\nsecret-ish content\n")
	// A sibling that must stay unreachable through the share.
	writeFile(t, filepath.Join(work, "reports"), "private.md", "PRIVATE")

	ts, _ := newShareMgmtServer(t, "mgmt-token", map[string]string{"auto-bugfix": work})

	// 1. The authenticated user creates a share.
	body, _ := json.Marshal(map[string]string{"project": "auto-bugfix", "path": "reports/q3.md"})
	resp, raw := doReq(t, http.MethodPost, ts.URL+"/api/v1/share", "mgmt-token", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	var env struct {
		Data struct {
			Token string `json:"token"`
			URL   string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	shareURL := ts.URL + env.Data.URL

	// 2. An anonymous recipient — no headers at all — fetches it.
	anonResp, anonBody := doReq(t, http.MethodGet, shareURL, "", nil)
	if anonResp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous fetch should be 200, got %d", anonResp.StatusCode)
	}
	if !strings.Contains(string(anonBody), "Q3 Report") {
		t.Fatalf("wrong content: %q", anonBody)
	}
	// The SPA fallback would have served HTML; make sure it did not.
	if ct := anonResp.Header.Get("Content-Type"); strings.Contains(ct, "text/html") {
		t.Fatalf("served HTML instead of the file: %q", ct)
	}

	// 3. That same anonymous caller must not reach the sibling file...
	for _, evil := range []string{shareURL + "/../private.md", shareURL + "%2F..%2Fprivate.md"} {
		r, b := doReq(t, http.MethodGet, evil, "", nil)
		if r.StatusCode == http.StatusOK && strings.Contains(string(b), "PRIVATE") {
			t.Fatalf("share leaked a sibling file via %s", evil)
		}
	}
	// ...nor the authenticated files endpoint.
	if r, _ := doReq(t, http.MethodGet, ts.URL+"/api/v1/files/auto-bugfix/reports/private.md", "", nil); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("files endpoint should still require auth, got %d", r.StatusCode)
	}

	// 4. Revoke, then the link is dead.
	if r, _ := doReq(t, http.MethodDelete, ts.URL+"/api/v1/share/"+env.Data.Token, "mgmt-token", nil); r.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d", r.StatusCode)
	}
	if r, _ := doReq(t, http.MethodGet, shareURL, "", nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("after revoke expected 404, got %d", r.StatusCode)
	}
}
func TestRealHTTPShareFlow(t *testing.T) {
	// Requires the REAL embedded bundle (imported by this package's web
	// dependency chain in production). Skip if it is absent rather than
	// failing, so this stays runnable in a bare checkout.
	if core.GetWebAssets() == nil {
		t.Skip("no embedded web assets in this build")
	}
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "r.md"), []byte("# Title\n\n| A | B |\n|---|---|\n| 1 | 2 |\n"), 0o644)

	store, err := NewShareStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := NewManagementServer(0, "tok", nil)
	m.SetShareStore(store)
	m.RegisterProjectWorkDir("proj", work)
	mux := http.NewServeMux()
	ts := httptest.NewServer(m.buildHandler(mux))
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"project": "proj", "path": "r.md"})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/share", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer tok")
	res, _ := http.DefaultClient.Do(req)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var env struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	json.Unmarshal(raw, &env)
	shareURL := ts.URL + env.Data.URL
	t.Logf("share url: %s", shareURL)

	get := func(accept string) (int, string, string) {
		r, _ := http.NewRequest("GET", shareURL, nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
	}

	// Browser navigation -> viewer page (real embed).
	code, ct, page := get("text/html,application/xhtml+xml,*/*;q=0.8")
	t.Logf("browser  -> %d %s (%d bytes)", code, ct, len(page))
	if code != 200 || !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("browser navigation should get the viewer page, got %d %s", code, ct)
	}
	if !strings.Contains(page, "share-root") {
		t.Fatal("viewer page missing its mount point")
	}
	if !strings.Contains(page, "data-share-token=") {
		t.Fatal("viewer page missing the injected token")
	}
	// It must reference the share chunk, not the SPA chunk.
	if !strings.Contains(page, "assets/share-") {
		t.Error("viewer page does not load the share chunk")
	}
	if strings.Contains(page, "BODY") || strings.Contains(page, "# Title") {
		t.Error("viewer page leaked raw markdown")
	}

	// curl -> raw markdown.
	code, ct, rawBody := get("*/*")
	t.Logf("curl     -> %d %s (%d bytes)", code, ct, len(rawBody))
	if code != 200 || !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("curl should still get raw markdown, got %d %s", code, ct)
	}
	if !strings.Contains(rawBody, "# Title") {
		t.Fatalf("raw body wrong: %q", rawBody)
	}

	// The viewer's own fetch (?raw=1) -> raw markdown.
	r2, _ := http.NewRequest("GET", shareURL+"?raw=1", nil)
	r2.Header.Set("Accept", "*/*")
	resp2, _ := http.DefaultClient.Do(r2)
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(b2), "# Title") {
		t.Fatalf("?raw=1 should return the file, got %q", b2)
	}
	t.Log("all three paths correct: browser=page, curl=raw, ?raw=1=raw")
}
