// Package webnoop registers a no-op "web" platform type so that a project's
// [[projects.platforms]] array can carry a virtual `type = "web"` entry
// purely to hold a per-platform [projects.platforms.display] override (see
// config.EffectiveDisplayForPlatform), without heron-connect trying to
// establish any real connection for it.
//
// The actual Web admin UI does not talk to heron-connect through a
// registered core.Platform at all — it connects via the global [bridge]
// WebSocket server (see package bridge), which is a process-wide service
// shared across all projects, not a per-project platform entry. Messages
// arriving over that bridge carry a "web" platform name string
// (core.Message.Platform) but are dispatched directly to the owning
// project's Engine — they never pass through this package's Platform.
//
// Without this registration, any [[projects.platforms]] entry with
// `type = "web"` would fail core.CreatePlatform's factory lookup and cause
// heron-connect to exit at startup (see cmd/heron-connect/main.go's
// createProjectEngines, which os.Exit(1)s on an unknown platform type).
// This package exists solely to close that gap.
package webnoop

import (
	"context"

	"github.com/janostudio/heron-connect/core"
)

func init() {
	core.RegisterPlatform("web", New)
}

// Platform is an inert core.Platform: Start/Stop are no-ops and it never
// calls the message handler, since no real transport backs it. It exists
// only so `type = "web"` [[projects.platforms]] entries can carry a
// [projects.platforms.display] override block.
type Platform struct{}

// New constructs the no-op "web" platform. Options are accepted (and
// ignored) for symmetry with every other platform factory.
func New(_ map[string]any) (core.Platform, error) {
	return &Platform{}, nil
}

func (p *Platform) Name() string { return "web" }

// Start never invokes handler — this platform has no real transport and
// never receives inbound messages.
func (p *Platform) Start(_ core.MessageHandler) error { return nil }

func (p *Platform) Stop() error { return nil }

// Reply and Send are unreachable in practice (no message ever originates
// from this platform, so no reply is ever routed back through it), but are
// implemented as no-ops rather than returning core.ErrNotSupported so a
// misdirected call (e.g. from a cron job accidentally targeting this
// platform) fails silently instead of surfacing a confusing error.
func (p *Platform) Reply(_ context.Context, _ any, _ string) error { return nil }
func (p *Platform) Send(_ context.Context, _ any, _ string) error  { return nil }
