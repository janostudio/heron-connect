package wecom

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testHTTPClient(fn func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTripFunc(fn)}
}

func TestWeComAPIURL_DefaultBase(t *testing.T) {
	p := &Platform{}
	got := p.wecomAPIURL("/cgi-bin/gettoken", url.Values{
		"corpid":     []string{"ww-test"},
		"corpsecret": []string{"sec-test"},
	})
	want := "https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=ww-test&corpsecret=sec-test"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestWeComAPIURL_CustomBase(t *testing.T) {
	p := &Platform{apiBaseURL: "https://wecom.internal.example.com/"}
	got := p.wecomAPIURL("/cgi-bin/message/send", url.Values{
		"access_token": []string{"tok"},
	})
	want := "https://wecom.internal.example.com/cgi-bin/message/send?access_token=tok"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestNew_DefaultAPIBaseURL(t *testing.T) {
	pf, err := New(map[string]any{
		"corp_id":          "ww_test",
		"corp_secret":      "sec_test",
		"agent_id":         "1000002",
		"callback_token":   "cb_token",
		"callback_aes_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	p, ok := pf.(*Platform)
	if !ok {
		t.Fatalf("platform type = %T, want *wecom.Platform", pf)
	}
	if p.apiBaseURL != defaultAPIBaseURL {
		t.Fatalf("apiBaseURL = %q, want %q", p.apiBaseURL, defaultAPIBaseURL)
	}
}

func TestNew_CustomAPIBaseURL_TrimTrailingSlash(t *testing.T) {
	pf, err := New(map[string]any{
		"corp_id":          "ww_test",
		"corp_secret":      "sec_test",
		"agent_id":         "1000002",
		"callback_token":   "cb_token",
		"callback_aes_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"api_base_url":     "https://wecom.internal.example.com/",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	p, ok := pf.(*Platform)
	if !ok {
		t.Fatalf("platform type = %T, want *wecom.Platform", pf)
	}
	if p.apiBaseURL != "https://wecom.internal.example.com" {
		t.Fatalf("apiBaseURL = %q, want %q", p.apiBaseURL, "https://wecom.internal.example.com")
	}
}

func TestNew_ConfiguresAccessLogger(t *testing.T) {
	dataDir := t.TempDir()
	pf, err := New(map[string]any{
		"corp_id":          "ww_test",
		"corp_secret":      "sec_test",
		"agent_id":         "1000002",
		"callback_token":   "cb_token",
		"callback_aes_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"cc_data_dir":      dataDir,
		"cc_project":       "proj/test",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	p := pf.(*Platform)
	if p.accessLog == nil {
		t.Fatal("accessLog = nil, want configured logger")
	}
	want := filepath.Join(dataDir, "audit", "wecom_access", "proj_test.jsonl")
	if p.accessLog.path != want {
		t.Fatalf("access log path = %q, want %q", p.accessLog.path, want)
	}
}

func TestWecomAccessLogger_Log(t *testing.T) {
	dataDir := t.TempDir()
	logger := newWecomAccessLogger(dataDir, "proj/a")
	logger.Log(wecomAccessRecord{
		Source:     "callback",
		Allowed:    true,
		UserID:     "zhangsan",
		ChatID:     "zhangsan",
		ChatType:   "single",
		SessionKey: "wecom:zhangsan",
		MessageID:  "123",
		MsgType:    "text",
		Reason:     "received",
	})

	buf, err := os.ReadFile(logger.path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var rec wecomAccessRecord
	if err := json.Unmarshal(buf, &rec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rec.Project != "proj/a" {
		t.Fatalf("project = %q, want %q", rec.Project, "proj/a")
	}
	if rec.UserID != "zhangsan" {
		t.Fatalf("user_id = %q, want %q", rec.UserID, "zhangsan")
	}
	if !rec.Allowed {
		t.Fatal("allowed = false, want true")
	}
	if rec.SessionKey != "wecom:zhangsan" {
		t.Fatalf("session_key = %q", rec.SessionKey)
	}
	if rec.Reason != "received" {
		t.Fatalf("reason = %q, want received", rec.Reason)
	}
	if rec.PromptSentAt.IsZero() {
		t.Fatal("prompt_sent_at is zero")
	}
	if delta := time.Since(rec.PromptSentAt); delta < 0 || delta > 5*time.Second {
		t.Fatalf("prompt_sent_at delta = %v, want recent timestamp", delta)
	}
}

func TestPlatformLogAccess_UnauthorizedRecorded(t *testing.T) {
	dataDir := t.TempDir()
	p := &Platform{allowFrom: "allowed-user", accessLog: newWecomAccessLogger(dataDir, "proj/test")}

	allowed := false
	reason := "allow_from_rejected"
	p.logAccess(wecomAccessRecord{
		Source:     "callback",
		Allowed:    allowed,
		UserID:     "blocked-user",
		ChatID:     "blocked-user",
		ChatType:   "single",
		SessionKey: "wecom:blocked-user",
		MessageID:  "456",
		MsgType:    "text",
		Reason:     reason,
	})

	buf, err := os.ReadFile(p.accessLog.path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var rec wecomAccessRecord
	if err := json.Unmarshal(buf, &rec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rec.Allowed {
		t.Fatal("allowed = true, want false")
	}
	if rec.UserID != "blocked-user" {
		t.Fatalf("user_id = %q, want blocked-user", rec.UserID)
	}
	if rec.Reason != "allow_from_rejected" {
		t.Fatalf("reason = %q, want allow_from_rejected", rec.Reason)
	}
}

func TestReply_UnauthorizedNotificationUsesTextAPI(t *testing.T) {
	var requestBody string
	p := &Platform{
		agentID: "1000002",
		tokenCache: tokenCache{
			token:     "tok",
			expiresAt: time.Now().Add(time.Minute),
		},
		apiClient: testHTTPClient(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/cgi-bin/message/send" {
				t.Fatalf("request path = %q", req.URL.Path)
			}
			buf, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			requestBody = string(buf)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	denyMsg := "无权限使用此机器人，请联系管理员开通。你的 UserID: blocked-user"
	if err := p.Reply(context.Background(), replyContext{userID: "blocked-user"}, denyMsg); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(requestBody, `"msgtype":"text"`) {
		t.Fatalf("request body = %s, want text msgtype", requestBody)
	}
	if !strings.Contains(requestBody, `"touser":"blocked-user"`) {
		t.Fatalf("request body = %s, want blocked user", requestBody)
	}
	if !strings.Contains(requestBody, denyMsg) {
		t.Fatalf("request body = %s, want deny message", requestBody)
	}
}
