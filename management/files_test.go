package management

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// ── preview slicing ─────────────────────────────────────────

func TestMgmt_FilePreviewSlices(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("abcdefghij", 250) // 2500 bytes
	if err := os.WriteFile(filepath.Join(dir, "log.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/log.txt?preview=1&offset=0&limit=1000&token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	if len(body) != 1000 || string(body) != content[:1000] {
		t.Fatalf("first slice length=%d, want 1000 and matching bytes", len(body))
	}
	if got := resp.Header.Get("X-Preview-Total-Size"); got != "2500" {
		t.Fatalf("total size header = %q, want 2500", got)
	}
	if got := resp.Header.Get("X-Preview-Offset"); got != "0" {
		t.Fatalf("offset header = %q", got)
	}
	if got := resp.Header.Get("X-Preview-Truncated"); got != "1" {
		t.Fatalf("truncated header = %q, want 1", got)
	}

	// The final slice reports no truncation.
	resp, body = doGet(t, ts.URL+"/api/v1/files/proj/log.txt?preview=1&offset=2000&limit=1000&token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for tail slice, got %d: %s", resp.StatusCode, body)
	}
	if len(body) != 500 || string(body) != content[2000:] {
		t.Fatalf("tail slice length=%d, want 500", len(body))
	}
	if got := resp.Header.Get("X-Preview-Truncated"); got != "0" {
		t.Fatalf("truncated header on tail = %q, want 0", got)
	}
}

func TestMgmt_FilePreviewClampsLimitAndOffset(t *testing.T) {
	dir := t.TempDir()
	big := bytes.Repeat([]byte("x"), 2<<20) // 2 MiB
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	// A caller asking for the whole file gets one bounded slice instead.
	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/big.txt?preview=1&limit=99999999&token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(body) != previewMaxChunk {
		t.Fatalf("clamped slice length = %d, want %d", len(body), previewMaxChunk)
	}
	if got := resp.Header.Get("X-Preview-Returned"); got != strconv.Itoa(previewMaxChunk) {
		t.Fatalf("returned header = %q", got)
	}

	// An offset past EOF yields an empty, non-truncated response instead of a
	// seek error.
	resp, body = doGet(t, ts.URL+"/api/v1/files/proj/big.txt?preview=1&offset=99999999&token=tok", "")
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("past-EOF preview: status=%d len=%d", resp.StatusCode, len(body))
	}
	if got := resp.Header.Get("X-Preview-Truncated"); got != "0" {
		t.Fatalf("truncated header past EOF = %q, want 0", got)
	}
}

func TestMgmt_FilePreviewRejectsBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/blob.bin?preview=1&token=tok", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for binary preview, got %d: %s", resp.StatusCode, body)
	}
}

func TestMgmt_FilePreviewSplitsMultiByteChars(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("中文内容", 100) // 3 bytes per rune
	if err := os.WriteFile(filepath.Join(dir, "zh.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	// 4 bytes per slice lands mid-character on purpose: the server slices
	// bytes, and the client is expected to stitch. What matters here is that a
	// misaligned boundary neither errors nor drops bytes when concatenated.
	var got []byte
	for offset := 0; ; {
		resp, body := doGet(t, ts.URL+"/api/v1/files/proj/zh.txt?preview=1&offset="+
			strconv.Itoa(offset)+"&limit=4&token=tok", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("slice at %d: status %d", offset, resp.StatusCode)
		}
		got = append(got, body...)
		offset += len(body)
		if resp.Header.Get("X-Preview-Truncated") == "0" {
			break
		}
	}
	if string(got) != content {
		t.Fatalf("stitched content mismatch: got %d bytes, want %d", len(got), len(content))
	}
}

func TestMgmt_FilePreviewRequiresTextContentType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	resp, _ := doGet(t, ts.URL+"/api/v1/files/proj/notes.md?preview=1&token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for markdown preview, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("preview content-type = %q, want text/plain", ct)
	}
}

func TestMgmt_FileDirListingPagination(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	type page struct {
		OK   bool `json:"ok"`
		Data struct {
			Entries []struct {
				Name string `json:"name"`
			} `json:"entries"`
			Total      int `json:"total"`
			Offset     int `json:"offset"`
			NextOffset int `json:"next_offset"`
		} `json:"data"`
	}

	resp, body := doGet(t, ts.URL+"/api/v1/files/proj/?limit=2&token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var first page
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(first.Data.Entries) != 2 || first.Data.Total != 3 || first.Data.NextOffset != 2 {
		t.Fatalf("first page = %+v", first.Data)
	}

	resp, body = doGet(t, ts.URL+"/api/v1/files/proj/?offset=2&limit=2&token=tok", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for second page, got %d", resp.StatusCode)
	}
	var second page
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(second.Data.Entries) != 1 || second.Data.Total != 3 || second.Data.NextOffset != 0 {
		t.Fatalf("second page = %+v", second.Data)
	}
	if second.Data.Entries[0].Name != "c.txt" {
		t.Fatalf("second page entry = %q, want c.txt", second.Data.Entries[0].Name)
	}
}

func TestMgmt_FileDirListingReturnsEverythingWithoutPagingParams(t *testing.T) {
	dir := t.TempDir()
	const count = 520
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("file-%03d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts := newFileMgmtServer(t, "tok", map[string]string{"proj": dir})

	type page struct {
		OK   bool `json:"ok"`
		Data struct {
			Entries    []struct{ Name string } `json:"entries"`
			Total      int                     `json:"total"`
			NextOffset int                     `json:"next_offset"`
		} `json:"data"`
	}

	// A caller that sends no paging parameters is the pre-pagination client and
	// must still receive the whole listing: silently truncating it would look
	// like files disappearing from the directory.
	_, body := doGet(t, ts.URL+"/api/v1/files/proj/?token=tok", "")
	var full page
	if err := json.Unmarshal(body, &full); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(full.Data.Entries) != count || full.Data.Total != count || full.Data.NextOffset != 0 {
		t.Fatalf("unpaged listing = %d entries, total %d, next %d; want %d/0",
			len(full.Data.Entries), full.Data.Total, full.Data.NextOffset, count)
	}

	// Asking for a page is what opts into truncation.
	_, body = doGet(t, ts.URL+"/api/v1/files/proj/?limit=500&token=tok", "")
	var paged page
	if err := json.Unmarshal(body, &paged); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(paged.Data.Entries) != 500 || paged.Data.Total != count || paged.Data.NextOffset != 500 {
		t.Fatalf("paged listing = %d entries, total %d, next %d; want 500/%d/500",
			len(paged.Data.Entries), paged.Data.Total, paged.Data.NextOffset, count)
	}
}

func TestMgmt_SharePreviewServesBytesNotTheViewerPage(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("a line of text\n", 100)
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, store := newShareMgmtServer(t, "tok", map[string]string{"proj": dir})
	sh, err := store.Create("proj", "notes.md", "notes.md")
	if err != nil {
		t.Fatalf("create share: %v", err)
	}

	// ?raw=1 is what the viewer's own fetch sends; the combination with
	// preview=1 must yield sliced bytes, never the HTML page.
	resp, body := doGet(t, ts.URL+"/api/v1/share/"+sh.Token+"?raw=1&preview=1&offset=0&limit=10", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share preview status = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("share preview content-type = %q, want text/plain", ct)
	}
	if len(body) != 10 || strings.Contains(string(body), "<html") {
		t.Fatalf("share preview body = %q (len %d)", string(body), len(body))
	}

	// Without raw=1 a browser navigation still gets the viewer page: preview=1
	// alone must not hijack the anonymous entry point.
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/share/"+sh.Token+"?preview=1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	pageResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET share page: %v", err)
	}
	defer pageResp.Body.Close()
	pageBody, _ := io.ReadAll(pageResp.Body)
	if !strings.Contains(string(pageBody), "share-root") {
		t.Fatalf("expected the viewer page, got %q", string(pageBody))
	}
}
