package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestNewUnixHTTPClient_HasTimeout guards the CLI-wide invariant: every client
// that talks to the local instance must carry a Timeout, or a hung server can
// block the CLI forever. `heron-connect send` runs mid-turn, so an unbounded
// client there drags the whole agent turn down — a process was observed stuck
// for days before this was added.
func TestNewUnixHTTPClient_HasTimeout(t *testing.T) {
	c := newUnixHTTPClient("/nonexistent/heron.sock")
	if c.Timeout <= 0 {
		t.Fatalf("newUnixHTTPClient Timeout = %v, want a positive bound", c.Timeout)
	}
	if c.Timeout != cliHTTPTimeout {
		t.Fatalf("newUnixHTTPClient Timeout = %v, want %v", c.Timeout, cliHTTPTimeout)
	}
}

// TestCLISubcommandClients_HaveTimeout covers each subcommand's client
// construction path, so a future refactor that bypasses newUnixHTTPClient (and
// forgets the timeout) fails here rather than in production.
func TestCLISubcommandClients_HaveTimeout(t *testing.T) {
	// send + share build their client through the shared helper.
	checks := map[string]*http.Client{
		"send":  newSendClient("/nonexistent/heron.sock"),
		"share": shareClientHTTP(t),
	}
	for name, c := range checks {
		if c == nil {
			t.Fatalf("%s client is nil", name)
		}
		if c.Timeout <= 0 {
			t.Errorf("%s client Timeout = %v, want a positive bound", name, c.Timeout)
		}
	}
}

// shareClientHTTP returns the http.Client used by the share subcommand, by
// exercising its real constructor (newShareClient) against a throwaway socket.
func shareClientHTTP(t *testing.T) *http.Client {
	t.Helper()
	// resolveSocketPath(dataDir) → <dataDir>/run/api.sock
	dir, err := os.MkdirTemp("/tmp", fmt.Sprintf("heron-share-%d-", os.Getpid()))
	if err != nil {
		t.Skipf("cannot create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	runDir := filepath.Join(dir, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}
	ln, err := net.Listen("unix", filepath.Join(runDir, "api.sock"))
	if err != nil {
		t.Skipf("cannot create unix socket: %v", err)
	}
	defer ln.Close()

	sc, err := newShareClient(dir)
	if err != nil {
		t.Fatalf("newShareClient: %v", err)
	}
	return sc.http
}

// TestUnixHTTPClient_GivesUpOnHungServer drives the real shared client against a
// unix-socket server that accepts the connection but never responds. The client
// must return well before the test deadline instead of blocking forever. If the
// client's Timeout were missing, this test would hang until the go test
// timeout — exactly the production bug it guards.
func TestUnixHTTPClient_GivesUpOnHungServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket test")
	}
	// Shrink the timeout so the test is fast while still going through
	// newUnixHTTPClient (not a hand-built client).
	prev := cliTimeoutOverride
	cliTimeoutOverride = 200 * time.Millisecond
	t.Cleanup(func() { cliTimeoutOverride = prev })

	// macOS caps unix socket paths at ~104 bytes, so t.TempDir() is too long
	// there. Use a short /tmp path and clean it up explicitly.
	sockPath := filepath.Join("/tmp", fmt.Sprintf("heron-hung-%d.sock", os.Getpid()))
	_ = os.Remove(sockPath)
	t.Cleanup(func() { _ = os.Remove(sockPath) })

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	// Accept connections but never write a response. Hands each conn to a
	// goroutine that just blocks reading, holding it open without replying.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(io.Discard, c) // drain until the client gives up
			}(conn)
		}
	}()

	c := newUnixHTTPClient(sockPath)

	req, err := http.NewRequest(http.MethodPost, "http://unix/send", bytes.NewReader([]byte(`{"message":"x"}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	start := time.Now()
	_, err = c.Do(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("client returned nil error against a hung server; expected a timeout")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("client blocked %v on a hung server; expected it to give up quickly", elapsed)
	}
}
