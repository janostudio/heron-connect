//go:build e2e

package heron

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/janostudio/heron-connect/core"
)

// These tests talk to a real `heron-ai --serve`, which is the only way to catch
// a wire mismatch: paths, header/payload split and response casing are things
// a hand-written fixture gets wrong in exactly the same way the client does.
//
// HERON_AI_SERVE_URL=http://127.0.0.1:18080 go test -tags e2e -run TestServeE2E ./agent/heron/ -v
func serveURLForE2E(t *testing.T) string {
	t.Helper()
	url := os.Getenv("HERON_AI_SERVE_URL")
	if url == "" {
		t.Skip("set HERON_AI_SERVE_URL to run the serve end-to-end tests")
	}
	return url
}

// Health is what the adapter's Alive() and the process supervisor both key
// off. If the path or the status code drifts, every session looks dead.
func TestServeE2EHealthReportsAlive(t *testing.T) {
	url := serveURLForE2E(t)

	session := newHTTPSession(url, "", "")
	defer func() { _ = session.Close() }()

	require.True(t, session.Alive(), "the engine at %s must answer /api/health", url)
	require.True(t, probeHealth(context.Background(), url))
}

// A real turn reaches the engine and its rejection comes back through the same
// event channel a success would use. The model backend is unreachable in this
// environment, so the turn fails — but the failure is proof the request shape,
// the path and the error decoding all line up.
func TestServeE2ERunReachesEngine(t *testing.T) {
	url := serveURLForE2E(t)

	session := newHTTPSession(url, "", "")
	defer func() { _ = session.Close() }()
	require.NoError(t, session.Send("hello", nil, nil))

	select {
	case event := <-session.Events():
		require.Equal(t, core.EventError, event.Type, "a turn the engine rejects must surface as an error event")
		// The stdio adapter reports failures on Error, not Content; the two
		// adapters must be interchangeable, so this one does the same.
		require.NotNil(t, event.Error, "the engine's reason must reach the caller")
		require.NotEmpty(t, event.Error.Error())
		t.Logf("engine said: %v", event.Error)
	case <-time.After(60 * time.Second):
		t.Fatal("the engine never answered")
	}
}

// The stream must subscribe without 404 and its frames must decode. The
// subscribe call is made directly so the check does not depend on a wake-up
// actually firing; the session id is supplied because a turn only yields one
// on success, and this environment has no reachable model backend.
func TestServeE2EStreamSubscribes(t *testing.T) {
	url := serveURLForE2E(t)
	sessionID := os.Getenv("HERON_AI_SERVE_SESSION")
	if sessionID == "" {
		t.Skip("set HERON_AI_SERVE_SESSION to a session id the engine already has")
	}

	session := newHTTPSession(url, "", sessionID)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.pumpOnce(ctx) }()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			require.NotContains(t, err.Error(), "404", "the stream path must be the engine's")
			require.NotContains(t, err.Error(), "not found")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream pump did not stop after cancellation")
	}
	// Frames were read and their seq tracked, so the header/payload split
	// decodes: a session with any history must advance past seq 0.
	require.Greater(t, session.lastEventSeq(), int64(0), "no event was decoded from the stream")
	_ = session.Close()
}

// The whole point of serve mode: a turn the engine starts on its own reaches
// the chat with no request carrying it. The session is supplied because the
// wake-up has to be scheduled beforehand — by a model that this environment
// does not have.
//
//	HERON_AI_SERVE_WAKEUP_SESSION=<session with a pending wake-up> \
//	  HERON_AI_SERVE_URL=http://127.0.0.1:18080 \
//	  go test -tags e2e -run TestServeE2EWakeupArrivesOnStream ./agent/heron/ -v
func TestServeE2EWakeupArrivesOnStream(t *testing.T) {
	url := serveURLForE2E(t)
	sessionID := os.Getenv("HERON_AI_SERVE_WAKEUP_SESSION")
	if sessionID == "" {
		t.Skip("set HERON_AI_SERVE_WAKEUP_SESSION to a session with a pending wake-up")
	}

	session := newHTTPSession(url, "", sessionID)
	session.startEventStream()
	defer func() { _ = session.Close() }()

	deadline := time.After(150 * time.Second)
	for {
		select {
		case event := <-session.Events():
			require.Equal(t, core.EventResult, event.Type, "only a user-visible result may reach the chat")
			require.NotEmpty(t, event.Content)
			t.Logf("delivered wake-up: %q", event.Content)
			return
		case <-deadline:
			t.Fatal("the wake-up turn never arrived on the stream")
		}
	}
}
