package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

// stubShareService stands in for the management server so the socket layer can
// be tested on its own. It records what it was asked to do, which is what these
// tests care about: that the endpoints decode, delegate and map errors.
type stubShareService struct {
	created   []string // "project/path"
	reused    bool
	createErr error
	shares    []ShareResult
	listErr   error
	revoked   []string
	revokeOK  bool
	revokeErr error
}

func (s *stubShareService) CreateShare(project, relPath string) (*ShareResult, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	s.created = append(s.created, project+"/"+relPath)
	return &ShareResult{
		Token: "tok-" + project, URL: "/api/v1/share/tok-" + project,
		Project: project, Path: relPath, FileName: "f", Reused: s.reused,
	}, nil
}

func (s *stubShareService) ListShares(project string) ([]ShareResult, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.shares, nil
}

func (s *stubShareService) RevokeShare(token string) (bool, error) {
	if s.revokeErr != nil {
		return false, s.revokeErr
	}
	s.revoked = append(s.revoked, token)
	return s.revokeOK, nil
}

func newShareAPI(svc ShareService) *APIServer {
	s := &APIServer{engines: make(map[string]*core.Engine), mux: http.NewServeMux()}
	if svc != nil {
		s.SetShareService(svc)
	}
	return s
}

func decodeShareResult(t *testing.T, rec *httptest.ResponseRecorder) ShareResult {
	t.Helper()
	var res ShareResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return res
}

func TestShareCreate_DelegatesToService(t *testing.T) {
	svc := &stubShareService{}
	s := newShareAPI(svc)

	body, _ := json.Marshal(map[string]string{"project": "proj", "path": "reports/q3.md"})
	rec := httptest.NewRecorder()
	s.handleShareCreate(rec, httptest.NewRequest(http.MethodPost, "/share/create", bytes.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(svc.created) != 1 || svc.created[0] != "proj/reports/q3.md" {
		t.Fatalf("service not called correctly: %v", svc.created)
	}
	res := decodeShareResult(t, rec)
	if res.Token == "" || res.URL == "" {
		t.Fatalf("response missing token/url: %+v", res)
	}
}

func TestShareCreate_PropagatesReused(t *testing.T) {
	// The CLI relies on this flag to say "already shared" instead of silently
	// printing an old link as if it were new.
	s := newShareAPI(&stubShareService{reused: true})
	body, _ := json.Marshal(map[string]string{"project": "p", "path": "f"})
	rec := httptest.NewRecorder()
	s.handleShareCreate(rec, httptest.NewRequest(http.MethodPost, "/share/create", bytes.NewReader(body)))

	if !decodeShareResult(t, rec).Reused {
		t.Error("reused flag did not survive the socket layer")
	}
}

func TestShareCreate_ValidatesInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"missing project", `{"path":"a.md"}`, http.StatusBadRequest},
		{"missing path", `{"project":"p"}`, http.StatusBadRequest},
		{"blank project", `{"project":"  ","path":"a.md"}`, http.StatusBadRequest},
		{"invalid json", `{`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newShareAPI(&stubShareService{})
			rec := httptest.NewRecorder()
			s.handleShareCreate(rec, httptest.NewRequest(http.MethodPost, "/share/create", bytes.NewReader([]byte(tc.body))))
			if rec.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestShareCreate_MapsServiceErrors keeps the CLI's error messages meaningful:
// a missing file must not surface as a 500.
func TestShareCreate_MapsServiceErrors(t *testing.T) {
	tests := []struct {
		msg  string
		want int
	}{
		{"file not found", http.StatusNotFound},
		{"path escapes work dir", http.StatusForbidden},
		{"only files can be shared, not directories", http.StatusBadRequest},
		{"project is required", http.StatusBadRequest},
		{"something unexpected", http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.msg, func(t *testing.T) {
			s := newShareAPI(&stubShareService{createErr: errors.New(tc.msg)})
			body, _ := json.Marshal(map[string]string{"project": "p", "path": "f"})
			rec := httptest.NewRecorder()
			s.handleShareCreate(rec, httptest.NewRequest(http.MethodPost, "/share/create", bytes.NewReader(body)))
			if rec.Code != tc.want {
				t.Fatalf("expected %d for %q, got %d", tc.want, tc.msg, rec.Code)
			}
		})
	}
}

func TestShareCreate_RequiresPOST(t *testing.T) {
	s := newShareAPI(&stubShareService{})
	rec := httptest.NewRecorder()
	s.handleShareCreate(rec, httptest.NewRequest(http.MethodGet, "/share/create", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestShareList_PassesProjectFilter(t *testing.T) {
	svc := &stubShareService{shares: []ShareResult{{Token: "a", Project: "p1"}}}
	s := newShareAPI(svc)

	rec := httptest.NewRecorder()
	s.handleShareList(rec, httptest.NewRequest(http.MethodGet, "/share/list?project=p1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got []ShareResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Token != "a" {
		t.Fatalf("unexpected list: %+v", got)
	}
}

// TestShareList_EmptyIsArrayNot404: an empty result must be an empty JSON array
// so the CLI prints "No shared files." rather than choking on null.
func TestShareList_EmptyIsArrayNot404(t *testing.T) {
	s := newShareAPI(&stubShareService{shares: nil})
	rec := httptest.NewRecorder()
	s.handleShareList(rec, httptest.NewRequest(http.MethodGet, "/share/list", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "[]\n" && body != "[]" {
		t.Fatalf("expected an empty array, got %q", body)
	}
}

func TestShareRevoke(t *testing.T) {
	svc := &stubShareService{revokeOK: true}
	s := newShareAPI(svc)

	body, _ := json.Marshal(map[string]string{"token": "abc"})
	rec := httptest.NewRecorder()
	s.handleShareRevoke(rec, httptest.NewRequest(http.MethodPost, "/share/revoke", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(svc.revoked) != 1 || svc.revoked[0] != "abc" {
		t.Fatalf("service not called correctly: %v", svc.revoked)
	}
}

func TestShareRevoke_UnknownTokenIs404(t *testing.T) {
	s := newShareAPI(&stubShareService{revokeOK: false})
	body, _ := json.Marshal(map[string]string{"token": "nope"})
	rec := httptest.NewRecorder()
	s.handleShareRevoke(rec, httptest.NewRequest(http.MethodPost, "/share/revoke", bytes.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestShareRevoke_RequiresToken(t *testing.T) {
	s := newShareAPI(&stubShareService{})
	rec := httptest.NewRecorder()
	s.handleShareRevoke(rec, httptest.NewRequest(http.MethodPost, "/share/revoke", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestShareEndpoints_UnavailableWithoutService: without a wired service the
// endpoints must say so rather than appear to succeed.
func TestShareEndpoints_UnavailableWithoutService(t *testing.T) {
	s := newShareAPI(nil)

	calls := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		body    string
	}{
		{"create", s.handleShareCreate, http.MethodPost, `{"project":"p","path":"f"}`},
		{"list", s.handleShareList, http.MethodGet, ""},
		{"revoke", s.handleShareRevoke, http.MethodPost, `{"token":"t"}`},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			var rdr *bytes.Reader
			if tc.body != "" {
				rdr = bytes.NewReader([]byte(tc.body))
			} else {
				rdr = bytes.NewReader(nil)
			}
			rec := httptest.NewRecorder()
			tc.handler(rec, httptest.NewRequest(tc.method, "/share/"+tc.name, rdr))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected 503 without a share service, got %d", rec.Code)
			}
		})
	}
}
