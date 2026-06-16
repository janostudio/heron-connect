# cc-connect Architecture

This document describes every top-level directory in the repository and how the packages relate to one another.

---

## Directory Overview

| Directory | Package | Purpose |
|-----------|---------|---------|
| `agent/` | `agent/*` | AI coding agent adapters |
| `api/` | `api` | Local Unix socket API server |
| `bridge/` | `bridge` | WebSocket bridge server for remote clients |
| `cmd/` | `main` | Binary entry point (CLI, daemon) |
| `config/` | `config` | TOML config parsing |
| `core/` | `core` | Engine, interfaces, i18n, sessions, cards, registry |
| `daemon/` | `daemon` | systemd / launchd service helpers |
| `docs/` | — | Research, upgrade notes, architecture docs |
| `management/` | `management` | HTTP management API |
| `platform/` | `platform/*` | Messaging platform adapters |
| `proxy/` | `proxy` | Local reverse proxy for third-party providers |
| `relay/` | `relay` | Bot-to-bot relay manager |
| `webhook/` | `webhook` | HTTP webhook server |

---

## Package Descriptions

### `agent/`
One sub-package per supported AI coding agent (Claude Code, Codex, Gemini, Cursor, etc.). Each adapter implements the `core.Agent` interface, translating cc-connect session lifecycle calls into agent-specific API or subprocess calls.

### `api/`
Unix socket API server used by local tooling (TUI, CLI helpers). Exposes session listing, relay forwarding, and interactive message injection. Extracted from `core/` — imports `core/` and `relay/`, never the reverse.

### `bridge/`
WebSocket bridge server that lets remote clients (desktop apps, web UIs, mobile) connect to cc-connect over a single authenticated WebSocket connection. Carries a capabilities snapshot so clients can discover which projects and commands are available. Extracted from `core/` — imports `core/`, never the reverse.

### `cmd/`
The `cc-connect` binary. Wires together all subsystems: reads `config.toml`, instantiates engines, starts platform adapters, and launches the bridge, webhook, API, and management servers. Contains build-tag-gated `plugin_*.go` files for selective agent and platform compilation.

### `config/`
Parses `config.toml` using the TOML library. Defines `Config`, `ProjectConfig`, and related structs. No runtime logic — pure data unmarshalling.

### `core/`
The nucleus of the system. Defines:
- **Engine** — orchestrates agents, platforms, sessions, commands, and i18n for one project
- **Interfaces** — `Agent`, `AgentSession`, `Platform`, `MessageHandler`, `CardSender`, etc.
- **SessionManager** — persists and switches chat sessions
- **CommandRegistry** — built-in and custom slash-command dispatch
- **Cards** — rich interactive card rendering
- **i18n** — multi-language string tables (EN, ZH, ZH-TW, JA, ES)
- **CronScheduler / HeartbeatScheduler** — timed message injection
- **AtomicWriteFile** — safe config persistence helper

`core/` depends only on the Go standard library. No extracted package may be imported from `core/`.

### `daemon/`
Platform-specific helpers for running cc-connect as a background service: generates `systemd` unit files on Linux and `launchd` plist files on macOS.

### `docs/`
Human-readable documentation: platform setup guides, management API reference, upgrade notes, research notes, and this architecture overview.

### `management/`
HTTP REST API server consumed by web dashboards, TUI clients, and GUI desktop apps. Provides endpoints for project status, session management, provider CRUD, cron job management, heartbeat control, and skill browsing. Imported by `cmd/` and depends on `core/` and `bridge/`.

### `platform/`
One sub-package per supported messaging platform (Telegram, Slack, Feishu, WeChat Work, DingTalk, Discord, etc.). Each adapter implements the `core.Platform` interface, translating platform-specific webhook or polling events into `core.Message` structs and delivering `core.Engine` replies back to the chat.

### `proxy/`
A lightweight local reverse proxy that rewrites requests to third-party AI provider endpoints. Used by the Claude Code agent adapter to transparently route API calls through a locally managed URL. Depends only on the Go standard library — no imports from `core/`.

### `relay/`
Bot-to-bot relay manager. Allows one cc-connect instance to forward messages to another, enabling multi-hop or cross-platform relay chains. Extracted from `core/` — imports `core/`, never the reverse.

### `webhook/`
HTTP webhook server that receives inbound messages from platforms that use push delivery (e.g., WeChat Work, DingTalk HTTP mode). Extracted from `core/` — imports `core/`, never the reverse.

---

## Dependency Graph

```
cmd/          → core, bridge, relay, webhook, api, proxy, management
management/   → core, bridge
bridge/       → core
relay/        → core
webhook/      → core
api/          → core, relay
proxy/        → stdlib only
agent/*       → core, proxy   (claudecode only for proxy)
platform/*    → core
core/         → stdlib only
```

The invariant is strict: `core/` never imports any of the extracted packages. This keeps the engine logic self-contained and prevents circular dependencies.
