package heron

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/janostudio/heron-connect/core"
)

// The first message of a conversation creates the engine-side session; the
// answer arrives as an EventResult, the same contract the stdio adapter has.
func TestHTTPSessionCreatesSessionAndReturnsResult(t *testing.T) {
	var created bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/run":
			created = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Session": map[string]any{"id": "fs_1"},
				"Turn":    map[string]any{"id": "ft_1", "origin": "user"},
				"Reply":   "hello",
			})
		case "/api/stream":
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	session := newHTTPSession(server.URL, "", "")
	require.NoError(t, session.Send("hi", nil, nil))

	select {
	case event := <-session.Events():
		require.Equal(t, core.EventResult, event.Type)
		require.Equal(t, "hello", event.Content)
		require.True(t, event.Done)
		require.Equal(t, "fs_1", event.SessionID)
	case <-time.After(3 * time.Second):
		t.Fatal("no result event")
	}
	require.True(t, created, "the first message must create the session")
	require.Equal(t, "fs_1", session.CurrentSessionID())
	require.NoError(t, session.Close())
}

// sessionEventWire is the shape the engine actually writes to the stream: the
// event header is flattened at the top level and the turn fields live under
// "payload". Building the fixture from that shape — not from a convenient
// nested guess — is what caught the turn id being read from the wrong object.
//
// It is a struct rather than a map so the field set is the contract: a field
// moved between header and payload breaks the test instead of silently
// decoding to zero.
type sessionEventWire struct {
	Seq           int64          `json:"seq"`
	Type          string         `json:"type"`
	FlowSessionID string         `json:"flow_session_id,omitempty"`
	FlowTurnID    string         `json:"flow_turn_id,omitempty"`
	Payload       map[string]any `json:"payload"`
}

// turnEvent builds a finished-turn event the way the engine does.
func turnEvent(seq int64, flowTurnID, origin, reply string, intermediate bool) sessionEventWire {
	return sessionEventWire{
		Seq:           seq,
		Type:          "flow_turn.waiting_input",
		FlowSessionID: "fs_1",
		FlowTurnID:    flowTurnID,
		Payload: map[string]any{
			"reply":        reply,
			"origin":       origin,
			"intermediate": intermediate,
			"deliveries":   nil,
		},
	}
}

func newStreamServer(t *testing.T, events []sessionEventWire) (*httptest.Server, *lastEventIDRecorder) {
	t.Helper()
	recorder := &lastEventIDRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"ready":true}`))
		case "/api/stream":
			recorder.record(r.Header.Get("Last-Event-ID"))
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for _, event := range events {
				data, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.Seq, data)
				if flusher != nil {
					flusher.Flush()
				}
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, recorder
}

type lastEventIDRecorder struct {
	mu     sync.Mutex
	values []string
}

func (r *lastEventIDRecorder) record(value string) {
	r.mu.Lock()
	r.values = append(r.values, value)
	r.mu.Unlock()
}

func (r *lastEventIDRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.values...)
}

// A turn the engine started on its own has no request carrying it. The event
// stream is what makes it reachable — and only that turn: a normal turn is
// already delivered by the POST response, and an intermediate wake-up is still
// waiting on work it dispatched.
func TestHTTPSessionDeliversOnlyWakeupTurnsFromStream(t *testing.T) {
	server, _ := newStreamServer(t, []sessionEventWire{
		turnEvent(1, "ft_user", "user", "normal answer", false),
		turnEvent(2, "ft_mid", "wakeup", "已转给执行侧", true),
		turnEvent(3, "ft_wake", "wakeup", "下游已跑 5 小时，无新增产出", false),
	})

	session := newHTTPSession(server.URL, "", "fs_1")
	session.startEventStream()
	defer func() { _ = session.Close() }()

	select {
	case event := <-session.Events():
		require.Equal(t, core.EventResult, event.Type)
		require.Contains(t, event.Content, "5 小时")
	case <-time.After(3 * time.Second):
		t.Fatal("the wake-up turn never reached the event channel")
	}

	// Nothing else may follow: the user turn and the intermediate one are not
	// messages for the chat.
	select {
	case event := <-session.Events():
		t.Fatalf("unexpected extra event: %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
}

// A wake-up whose reply was cleared (it was intermediate when it finished, but
// the engine still recorded a delivery) must reach the user through the
// delivery text. This is the shape the engine produces for a wake-up that ends
// while its downstream Team is still running.
func TestHTTPSessionFallsBackToDeliveryText(t *testing.T) {
	event := turnEvent(7, "ft_wake_delivery", "wakeup", "", false)
	event.Payload["deliveries"] = []map[string]any{
		{"text": "下游已跑 6 小时，已催一次", "team_id": "default", "message_id": "ft_wake_delivery"},
	}
	server, _ := newStreamServer(t, []sessionEventWire{event})

	session := newHTTPSession(server.URL, "", "fs_1")
	session.startEventStream()
	defer func() { _ = session.Close() }()

	select {
	case got := <-session.Events():
		require.Equal(t, core.EventResult, got.Type)
		require.Contains(t, got.Content, "已催一次")
	case <-time.After(3 * time.Second):
		t.Fatal("the delivery text never reached the event channel")
	}
}

// A reconnect must resume from where the last connection stopped. Replaying
// from seq 1 would deliver every wake-up turn of the session again, which the
// in-memory de-duplication cannot catch after a process restart.
func TestHTTPSessionResumesStreamFromLastEventID(t *testing.T) {
	server, recorder := newStreamServer(t, []sessionEventWire{
		turnEvent(11, "ft_first", "user", "answer", false),
	})

	session := newHTTPSession(server.URL, "", "fs_1")
	session.startEventStream()
	defer func() { _ = session.Close() }()

	// Wait until the stream has been read up to seq 11.
	require.Eventually(t, func() bool { return session.lastEventSeq() == 11 }, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, int64(11), session.lastEventSeq())

	// The first connection carried no resume header.
	require.Equal(t, []string{""}, recorder.all())
}

// The stream path is the engine's real one. A wrong path silently 404s and the
// whole feature becomes "works in tests, delivers nothing in production".
func TestHTTPSessionSubscribesToTheEngineStreamPath(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/api/stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			<-r.Context().Done()
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	session := newHTTPSession(server.URL, "", "fs_1")
	session.startEventStream()
	defer func() { _ = session.Close() }()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(paths) > 0
	}, 2*time.Second, 20*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "/api/stream", paths[0], "the engine registers /api/stream, not /api/sessions/stream")
}
