package main

// webnoop registers the "web" virtual platform type used to carry
// per-platform display overrides for the Web admin UI (see
// platform/webnoop). Unlike real IM platforms (feishu, wecom, etc.), this
// is not a selectable/excludable platform category — it's always compiled
// in, matching how the [bridge] WebSocket server and Web admin UI
// themselves are core (not optional) heron-connect features.
import _ "github.com/janostudio/heron-connect/platform/webnoop"
