package bridge

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/janostudio/heron-connect/core"
)

// wsConnPair returns a connected server/client pair without going through
// handleConnection, so a connWriter can be exercised on its own.
func wsConnPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	serverConns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConns <- conn
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	select {
	case server := <-serverConns:
		t.Cleanup(func() { _ = server.Close() })
		return server, client
	case <-time.After(5 * time.Second):
		t.Fatal("server connection not established")
		return nil, nil
	}
}

// waitForNoAdapters blocks until every connection has been torn down. Tests
// that mutate package-level state (version stamps read while building a
// capabilities snapshot) must call this before restoring it: the handler runs
// on its own goroutine, so reading the snapshot on the client side does not
// order the two accesses.
func waitForNoAdapters(t *testing.T, bs *BridgeServer) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(bs.ConnectedAdapters()) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("adapters did not disconnect")
}

// TestServerPingKeepsIdleClientAlive is the regression for the disconnect this
// change exists to fix: a client that sends nothing at all (a background tab,
// whose timers are throttled) must stay connected. The client here answers
// only the server's control-frame pings, which the browser does in its network
// stack without running page JavaScript.
func TestServerPingKeepsIdleClientAlive(t *testing.T) {
	bs, wsURL := startTestBridge(t, "")
	bs.readTimeout = 300 * time.Millisecond
	bs.pingInterval = 100 * time.Millisecond
	bs.writeTimeout = time.Second

	conn := dialWS(t, wsURL, nil)
	register(t, conn, "bridge", nil)

	// Reading is what lets the gorilla client answer control pings; nothing
	// else is sent.
	frames := make(chan map[string]any, 8)
	go func() {
		for {
			var m map[string]any
			if err := conn.ReadJSON(&m); err != nil {
				close(frames)
				return
			}
			frames <- m
		}
	}()

	time.Sleep(4 * bs.readTimeout)

	if bs.getFirstAdapter("bridge") == nil {
		t.Fatal("idle client was dropped; the server's own ping should have kept it alive")
	}
	if err := bs.sendToAdapter("bridge", map[string]any{"type": "text", "content": "after idle"}); err != nil {
		t.Fatalf("broadcast after idle: %v", err)
	}
	select {
	case m, ok := <-frames:
		if !ok {
			t.Fatal("connection closed while waiting for the frame")
		}
		if m["content"] != "after idle" {
			t.Fatalf("unexpected frame: %v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("frame was not delivered after the idle period")
	}
}

// TestIdleClientWithoutPongDropped keeps the other half of the contract: a
// client that stops answering is still reclaimed, so dead peers cannot pile up.
func TestIdleClientWithoutPongDropped(t *testing.T) {
	bs, wsURL := startTestBridge(t, "")
	bs.readTimeout = 200 * time.Millisecond
	bs.pingInterval = 50 * time.Millisecond
	bs.writeTimeout = 200 * time.Millisecond

	conn := dialWS(t, wsURL, nil)
	// Swallow pings instead of answering them: this is what a vanished client
	// looks like to the server.
	conn.SetPingHandler(func(string) error { return nil })
	register(t, conn, "bridge", nil)

	adapter := bs.getFirstAdapter("bridge")
	if adapter == nil {
		t.Fatal("adapter was not registered")
	}
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && bs.getFirstAdapter("bridge") != nil {
		time.Sleep(20 * time.Millisecond)
	}
	if bs.getFirstAdapter("bridge") != nil {
		t.Fatal("client that never answered a ping was not dropped")
	}
	if reason := adapter.writer.stopReason(); reason != disconnectPongTimeout {
		t.Fatalf("disconnect reason = %q, want %q", reason, disconnectPongTimeout)
	}
}

// TestApplicationPingGetsPong covers the probe the web client sends when the
// page becomes visible again: it is the only way page JavaScript can tell a
// live connection from a half-open one.
func TestApplicationPingGetsPong(t *testing.T) {
	_, wsURL := startTestBridge(t, "")
	conn := dialWS(t, wsURL, nil)
	register(t, conn, "bridge", nil)

	mustWriteJSON(t, conn, map[string]any{"type": "ping", "ts": 1})
	if m := readMsg(t, conn); m["type"] != "pong" {
		t.Fatalf("reply to ping = %v, want a pong frame", m)
	}
}

// TestEnqueueRejectsWhenQueueFull: the writer is deliberately not started, so
// nothing drains the queue. The 257th frame must be refused and the connection
// closed as a slow consumer rather than blocking the producer.
func TestEnqueueRejectsWhenQueueFull(t *testing.T) {
	server, _ := wsConnPair(t)
	w := newConnWriter(server, time.Second, 0)

	for i := 0; i < sendQueueDepth; i++ {
		if !w.enqueue([]byte("x")) {
			t.Fatalf("enqueue #%d rejected before the queue was full", i)
		}
	}
	if w.enqueue([]byte("y")) {
		t.Fatal("enqueue accepted a frame past the queue depth")
	}
	if reason := w.stopReason(); reason != disconnectSlowConsumer {
		t.Fatalf("disconnect reason = %q, want %q", reason, disconnectSlowConsumer)
	}
}

// TestWriteTimeoutDropsSlowPeer: a peer that stops reading must not hold the
// writer forever. The write deadline turns that into a disconnect.
func TestWriteTimeoutDropsSlowPeer(t *testing.T) {
	server, client := wsConnPair(t)
	// Shrink both buffers so a frame larger than their sum has to block in the
	// kernel on loopback, which is where a real stalled peer blocks too.
	if tcp, ok := client.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(4096)
	}
	if tcp, ok := server.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(4096)
	}

	w := newConnWriter(server, 150*time.Millisecond, 0)
	w.start()

	// The client side never reads, so a frame this size cannot be flushed.
	if !w.enqueue(make([]byte, 32<<20)) {
		t.Fatal("enqueue rejected an empty queue")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && w.stopReason() == "" {
		time.Sleep(20 * time.Millisecond)
	}
	if reason := w.stopReason(); reason != disconnectWriteTimeout {
		t.Fatalf("disconnect reason = %q, want %q", reason, disconnectWriteTimeout)
	}
}

// TestReconnectReplacesAdapter: the same client_id keeps exactly one adapter,
// and the superseded connection is closed with a code clients treat as
// terminal (reconnecting on it would kick the newer connection back).
func TestReconnectReplacesAdapter(t *testing.T) {
	bs, wsURL := startTestBridge(t, "")

	oldConn := dialWS(t, wsURL, nil)
	registerWithClientID(t, oldConn, "bridge", "client-a", nil)

	newConn := dialWS(t, wsURL, nil)
	registerWithClientID(t, newConn, "bridge", "client-a", nil)

	if err := oldConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err := oldConn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != bridgeCloseReplaced {
		t.Fatalf("superseded connection closed with %v, want close code %d", err, bridgeCloseReplaced)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bs.mu.RLock()
		n := len(bs.adapters["bridge"])
		bs.mu.RUnlock()
		if n == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	bs.mu.RLock()
	defer bs.mu.RUnlock()
	t.Fatalf("adapter count = %d, want 1", len(bs.adapters["bridge"]))
}

// TestStopClosesAdapters guards the shutdown path: closing adapters while
// holding the registry lock used to be a deadlock waiting to happen, and a
// later broadcast must report "no connection" rather than panic.
func TestStopClosesAdapters(t *testing.T) {
	bs, wsURL := startTestBridge(t, "")
	conn := dialWS(t, wsURL, nil)
	register(t, conn, "bridge", nil)

	bs.Stop()

	if err := bs.sendToAdapter("bridge", map[string]any{"type": "text"}); !errors.Is(err, core.ErrNotConnected) {
		t.Fatalf("broadcast after Stop = %v, want ErrNotConnected", err)
	}
}
