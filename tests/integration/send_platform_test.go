//go:build integration

// Integration tests for explicit-platform proactive sends (--platform).
// These exercise the full HTTP path: a real Engine wired into a real APIServer
// on a unix socket, driven by HTTP requests that mirror what
// `heron-connect send --platform ...` produces.
// Run with: go test -tags=integration ./tests/integration/ -run TestIntegration_SendPlatform
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/janostudio/heron-connect/api"
	"github.com/janostudio/heron-connect/core"
	"github.com/janostudio/heron-connect/tests/mocks"
	"github.com/janostudio/heron-connect/tests/mocks/fake"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	sendPlatformProject = "it-send"
	sendPlatformKey     = "wecom:chat-1:user-1"
)

// sendPlatformHarness wires a real Engine (bridge + wecom mock platforms)
// behind a real APIServer on a unix socket, plus an HTTP client that talks to
// that socket — the same path the CLI uses.
type sendPlatformHarness struct {
	t      *testing.T
	bridge *mocks.MockPlatform
	wecom  *mocks.MockPlatformWithReplyCtxReconstructor
	client *http.Client
}

func newSendMockPlatform(name string) *mocks.MockPlatform {
	p := new(mocks.MockPlatform)
	p.On("Name").Return(name).Maybe()
	p.On("Start", mock.Anything).Return(nil).Maybe()
	p.On("Stop").Return(nil).Maybe()
	return p
}

func newSendPlatformHarness(t *testing.T) *sendPlatformHarness {
	t.Helper()

	bridge := newSendMockPlatform("bridge")
	slack := newSendMockPlatform("slack")

	wecomMock := newSendMockPlatform("wecom")
	wecom := &mocks.MockPlatformWithReplyCtxReconstructor{MockPlatform: wecomMock}
	wecom.On("ReconstructReplyCtx", sendPlatformKey).
		Return("wecom-rctx", nil).Maybe()

	engine := core.NewEngine(
		sendPlatformProject,
		fake.NewFakeAgent("it-agent"),
		[]core.Platform{bridge, slack, wecom},
		"", core.LangEnglish,
	)

	// The session key is bound to bridge (as if the Web admin had used it).
	engine.InjectInteractiveStateForTest(sendPlatformKey, bridge, "bridge-rctx")

	dataDir, err := os.MkdirTemp("/tmp", "heron-it-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dataDir) })
	// Unix socket paths are capped (~104 chars on macOS); t.TempDir() paths
	// are too long, hence the short /tmp dir above.
	server, err := api.NewAPIServer(dataDir)
	require.NoError(t, err)
	server.RegisterEngine(sendPlatformProject, engine)
	server.Start()
	t.Cleanup(server.Stop)

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", filepath.Join(dataDir, "run", "api.sock"))
			},
		},
	}

	return &sendPlatformHarness{t: t, bridge: bridge, wecom: wecom, client: client}
}

// post sends a /send request body and returns status code plus decoded JSON
// response (empty map for non-JSON error bodies).
func (h *sendPlatformHarness) post(body map[string]any) (int, map[string]string) {
	h.t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(h.t, err)
	resp, err := h.client.Post("http://unix/send", "application/json", bytes.NewReader(payload))
	require.NoError(h.t, err)
	defer resp.Body.Close()

	var decoded map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

func (h *sendPlatformHarness) expectSend(p *mocks.MockPlatform, msg string, err error) {
	h.t.Helper()
	p.On("Send", mock.Anything, mock.Anything, msg).Return(err).Once()
}

func (h *sendPlatformHarness) assertNoSends() {
	h.t.Helper()
	h.bridge.AssertNotCalled(h.t, "Send", mock.Anything, mock.Anything, mock.Anything)
	h.wecom.AssertNotCalled(h.t, "Send", mock.Anything, mock.Anything, mock.Anything)
}

func TestIntegration_SendPlatform_DefaultFollowsBinding(t *testing.T) {
	h := newSendPlatformHarness(t)
	h.expectSend(h.bridge, "via binding", nil)

	status, ack := h.post(map[string]any{
		"project":     sendPlatformProject,
		"session_key": sendPlatformKey,
		"message":     "via binding",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, map[string]string{
		"status":      "ok",
		"platform":    "bridge",
		"session_key": sendPlatformKey,
	}, ack)

	h.bridge.AssertExpectations(t)
	h.wecom.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
}

func TestIntegration_SendPlatform_ExplicitWecomOverridesBinding(t *testing.T) {
	h := newSendPlatformHarness(t)
	h.expectSend(h.wecom.MockPlatform, "back to wecom", nil)

	status, ack := h.post(map[string]any{
		"project":     sendPlatformProject,
		"session_key": sendPlatformKey,
		"message":     "back to wecom",
		"platform":    "wecom",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok", ack["status"])
	require.Equal(t, "wecom", ack["platform"])
	require.Equal(t, sendPlatformKey, ack["session_key"])

	h.wecom.AssertExpectations(t)
	h.bridge.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
}

func TestIntegration_SendPlatform_OriginRoutesByPrefix(t *testing.T) {
	h := newSendPlatformHarness(t)
	h.expectSend(h.wecom.MockPlatform, "to origin", nil)

	status, ack := h.post(map[string]any{
		"project":     sendPlatformProject,
		"session_key": sendPlatformKey,
		"message":     "to origin",
		"platform":    "origin",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "wecom", ack["platform"])

	h.wecom.AssertExpectations(t)
	h.bridge.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
}

func TestIntegration_SendPlatform_WebAliasUsesBindingCtx(t *testing.T) {
	h := newSendPlatformHarness(t)
	h.expectSend(h.bridge, "to web", nil)

	status, ack := h.post(map[string]any{
		"project":     sendPlatformProject,
		"session_key": sendPlatformKey,
		"message":     "to web",
		"platform":    "web",
	})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "bridge", ack["platform"])

	h.bridge.AssertExpectations(t)
}

func TestIntegration_SendPlatform_UnknownPlatformIs400(t *testing.T) {
	h := newSendPlatformHarness(t)

	status, _ := h.post(map[string]any{
		"project":     sendPlatformProject,
		"session_key": sendPlatformKey,
		"message":     "hi",
		"platform":    "nope",
	})
	require.Equal(t, http.StatusBadRequest, status)

	h.assertNoSends()
}

func TestIntegration_SendPlatform_PrefixMismatchIs422(t *testing.T) {
	h := newSendPlatformHarness(t)

	status, _ := h.post(map[string]any{
		"project":     sendPlatformProject,
		"session_key": sendPlatformKey,
		"message":     "hi",
		"platform":    "slack",
	})
	require.Equal(t, http.StatusUnprocessableEntity, status)

	h.assertNoSends()
}

func TestIntegration_SendPlatform_PlatformSendFailureIsNon200(t *testing.T) {
	h := newSendPlatformHarness(t)
	// Mirror the wecom-ws ack error: the platform accepts the request but the
	// delivery fails — the API must not report 200/ok.
	h.expectSend(h.wecom.MockPlatform, "boom", errors.New("wecom-ws: ack error: errcode=93006"))

	status, _ := h.post(map[string]any{
		"project":     sendPlatformProject,
		"session_key": sendPlatformKey,
		"message":     "boom",
		"platform":    "wecom",
	})
	require.Equal(t, http.StatusInternalServerError, status)

	h.wecom.AssertExpectations(t)
}
