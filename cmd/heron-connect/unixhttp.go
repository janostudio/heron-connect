package main

import (
	"context"
	"net"
	"net/http"
	"time"
)

// cliHTTPTimeout bounds every CLI request to the local heron-connect instance.
//
// Why a timeout at all: these clients talk to the running instance over a unix
// socket. If the instance (or a platform call behind it) never answers, an
// unbounded client blocks the CLI process forever — and for `send`, which the
// agent invokes mid-turn, that drags the whole agent turn down with it. A
// production process was observed stuck for days before this was added.
//
// 30s is generous for a local socket (normal responses are milliseconds) yet
// well under WeCom's own 30s HTTP budget, so a genuine hang surfaces as a clean
// error rather than an indefinite block.
const cliHTTPTimeout = 30 * time.Second

// cliTimeoutOverride, when > 0, replaces cliHTTPTimeout. Test-only: it lets a
// test drive a real client against a hung server without waiting the full
// production timeout.
var cliTimeoutOverride time.Duration

func cliTimeout() time.Duration {
	if cliTimeoutOverride > 0 {
		return cliTimeoutOverride
	}
	return cliHTTPTimeout
}

// newUnixHTTPClient returns an HTTP client that dials the given unix socket and
// carries a bounded Timeout. All CLI subcommands (send / share / cron / relay)
// must build their client through here so none of them can hang forever.
func newUnixHTTPClient(sockPath string) *http.Client {
	return &http.Client{
		Timeout: cliTimeout(),
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
}
