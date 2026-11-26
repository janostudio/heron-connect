package bridge

import (
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Timing for one WebSocket connection. They live together because they have to
// agree with each other: the read timeout must cover several ping intervals,
// and the write timeout must be short enough that one stuck peer cannot hold a
// frame indefinitely.
//
// They are defaults on BridgeServer (see newBridgeServer) instead of bare
// constants used at the call site, so tests can drive the whole cycle at
// millisecond scale.
const (
	defaultPingInterval = 20 * time.Second
	defaultReadTimeout  = 60 * time.Second
	defaultWriteTimeout = 10 * time.Second

	// sendQueueDepth bounds how far behind a client may fall before it is
	// disconnected. A frozen browser tab stops draining the socket; letting
	// frames pile up unbounded would turn that into memory growth, and
	// blocking the producer would stall every other client on the platform.
	sendQueueDepth = 256
)

// Why a connection went away. Every disconnect path sets one of these, so a
// log line answers "who dropped it" without correlating with anything else.
const (
	disconnectPeerClose    = "peer-close"
	disconnectPongTimeout  = "pong-timeout"
	disconnectPingFailed   = "ping-failed"
	disconnectWriteTimeout = "write-timeout"
	disconnectWriteError   = "write-error"
	disconnectSlowConsumer = "slow-consumer"
	disconnectReplaced     = "replaced"
	disconnectShutdown     = "server-shutdown"
)

// connWriter owns every byte written to one WebSocket connection.
//
// Single writer is the point: gorilla allows one concurrent writer, and doing
// the writes inline (as this package used to) meant a peer that stopped
// reading could block a broadcast to every other client. Here producers only
// enqueue; the goroutine below applies a write deadline, sends the server's
// keepalive pings on the same serial path, and closes the connection when
// anything goes wrong.
type connWriter struct {
	conn         *websocket.Conn
	send         chan []byte
	done         chan struct{}
	writeTimeout time.Duration
	pingInterval time.Duration

	once   sync.Once
	reason string

	// onStop runs exactly once, on the goroutine that called stop. It must not
	// take a lock that the caller may already hold.
	onStop func(reason string)
}

func newConnWriter(conn *websocket.Conn, writeTimeout, pingInterval time.Duration) *connWriter {
	return &connWriter{
		conn:         conn,
		send:         make(chan []byte, sendQueueDepth),
		done:         make(chan struct{}),
		writeTimeout: writeTimeout,
		pingInterval: pingInterval,
	}
}

func (w *connWriter) start() {
	go w.run()
}

// enqueue hands one frame to the writer. It never blocks: a client that cannot
// keep up is disconnected instead of slowing down the caller.
func (w *connWriter) enqueue(frame []byte) bool {
	select {
	case <-w.done:
		return false
	default:
	}
	select {
	case w.send <- frame:
		return true
	default:
		// The peer is not draining. Dropping the frame would leave it
		// permanently out of sync with the conversation, so close it and let
		// the client reconnect and resync from history.
		w.stop(disconnectSlowConsumer)
		return false
	}
}

func (w *connWriter) run() {
	var ping <-chan time.Time
	if w.pingInterval > 0 {
		ticker := time.NewTicker(w.pingInterval)
		defer ticker.Stop()
		ping = ticker.C
	}

	for {
		select {
		case <-w.done:
			return
		case frame := <-w.send:
			if !w.writeFrame(frame) {
				return
			}
		case <-ping:
			// A control-frame ping is answered by the browser's network stack,
			// not by page JavaScript, so a background tab that is throttled or
			// frozen still keeps its connection alive.
			if err := w.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(w.writeTimeout)); err != nil {
				w.stop(writeFailureReason(disconnectPingFailed, err))
				return
			}
		}
	}
}

func (w *connWriter) writeFrame(frame []byte) bool {
	if err := w.conn.SetWriteDeadline(time.Now().Add(w.writeTimeout)); err != nil {
		w.stop(disconnectWriteError)
		return false
	}
	if err := w.conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		w.stop(writeFailureReason(disconnectWriteError, err))
		return false
	}
	return true
}

// stop closes the connection once and reports the reason. Idempotent: the
// first caller wins, later ones are no-ops (a read timeout racing a slow
// consumer must not produce two disconnect lines).
func (w *connWriter) stop(reason string) {
	w.once.Do(func() {
		w.reason = reason
		close(w.done)
		_ = w.conn.Close()
		if w.onStop != nil {
			w.onStop(reason)
		}
	})
}

// stopWithCloseCode tells the peer why it is being closed before dropping it.
// Without the explicit code the client sees an abnormal closure (1006) and
// reconnects immediately, which kicks the newer connection that replaced it.
func (w *connWriter) stopWithCloseCode(code int, reason string) {
	_ = w.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(time.Second),
	)
	w.stop(reason)
}

func (w *connWriter) stopReason() string {
	select {
	case <-w.done:
		return w.reason
	default:
		return ""
	}
}

func writeFailureReason(base string, err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return disconnectWriteTimeout
	}
	slog.Debug("bridge: write failed", "reason", base, "error", err)
	return base
}

// classifyReadError maps a read failure to a disconnect reason. A read
// deadline expiry produces a plain net timeout, which is not a close error —
// that is exactly why the old code logged nothing at all for the most common
// disconnect of all.
func classifyReadError(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return disconnectPongTimeout
	}
	return disconnectPeerClose
}
