# CC-Connect Provider/Model — Quick Reference

## ⚡ 30-Second Summary

**Providers** are API endpoints with credentials. **Models** are AI models served by providers.

- Define globally in `config.toml` via `[[providers]]`
- Reference in projects via `provider_refs` or inline `[[projects.agent.providers]]`
- Switch at runtime: `/provider anthropic` or `/model opus`
- Manage via REST API: `POST /api/v1/providers`

---

## Struct Map

| Level | Struct | Where | Read |
|-------|--------|-------|------|
| **TOML (Config)** | `config.ProviderConfig` | `config/config.go:407` | Contains `AgentTypes`, `Endpoints`, `AgentModels`, `AgentModelLists` |
| **TOML (Model)** | `config.ProviderModelConfig` | `config/config.go:402` | `model` string, `alias` string |
| **Runtime (Core)** | `core.ProviderConfig` | `core/interfaces.go:331` | After resolution; has `Models` as `[]ModelOption` |
| **Runtime (Model)** | `core.ModelOption` | `core/interfaces.go:379` | `Name`, `Desc`, `Alias` |
| **Agent (Interface)** | `core.ProviderSwitcher` | `core/interfaces.go:345` | `SetProviders`, `SetActiveProvider`, `GetActiveProvider`, `ListProviders` |
| **API (Wire)** | `management.GlobalProviderInfo` | `management/mgmt_server.go:135` | JSON struct for HTTP endpoints |

---

## File Map

| File | Purpose | Key Functions/Types |
|------|---------|---------------------|
| `config/config.go` | TOML parsing | `Load()`, `ResolveProviderRefs()`, `ResolveForAgent()` |
| `core/interfaces.go` | Runtime types | `ProviderConfig`, `ModelOption`, `ProviderSwitcher` |
| `core/provider.go` | Helper functions | `GetProviderModels()`, `GetProviderModel()`, `SetProviderModel()` |
| `agent/claudecode/claudecode.go` | Agent impl | `SetProviders()`, `SetActiveProvider()`, `providerEnv()` |
| `management/mgmt_server.go` | REST API | `handleGlobalProviders()`, `handleGlobalProviderRoutes()` |

---

## Config Syntax

### Global Provider (TOML)

```toml
[[providers]]
name = "provider-name"
api_key = "${ENV_VAR}"
base_url = "https://..."  # optional; default = Anthropic API
model = "claude-sonnet-4-20250514"
thinking = "disabled"  # or "enabled" or empty
env = { KEY = "value" }  # extra env vars
agent_types = ["claudecode"]  # restrict to these agents

[providers.endpoints]
codex = "https://..."  # override base_url for codex agent

[providers.agent_models]
codex = "glm-5.1"  # override model for codex agent

[providers.agent_model_lists.claudecode]
[[providers.agent_model_lists.claudecode]]
model = "claude-opus-4-20250514"
alias = "opus"
```

### Project Provider Reference (TOML)

```toml
[[projects]]
name = "my-project"

[projects.agent]
type = "claudecode"
provider_refs = ["anthropic", "minimaxi"]  # reference global providers

# Or inline (project-specific)
[[projects.agent.providers]]
name = "relay"
api_key = "sk-..."
base_url = "http://localhost:5000"
model = "claude-opus-4-20250514"

[[projects.agent.providers.models]]
model = "claude-opus-4-20250514"
alias = "opus"
```

---

## Resolution Flow

1. **Parse TOML** → `config.Load()`
2. **Resolve refs** → `ResolveProviderRefs()`
   - For each project:
     - Look up global providers by name
     - Check `agent_types` filter
     - Apply per-agent overrides via `ResolveForAgent()`
     - Prepend to project's provider list
   - Inline providers appended after (can override)
3. **Agent init** → `agent.New()` (providers not set yet)
4. **Inject** → `agent.SetProviders(providers)`
5. **Activate** → `agent.SetActiveProvider(name)`
6. **Spawn session** → `agent.StartSession()` → `providerEnv()` → set env vars

---

## Runtime API

### Agent Methods (ProviderSwitcher)

```go
// Set all available providers
agent.SetProviders([]ProviderConfig{ {...}, {...} })

// Activate one provider by name
success := agent.SetActiveProvider("relay")

// Get active provider
active := agent.GetActiveProvider()  // → *ProviderConfig

// List all providers
all := agent.ListProviders()  // → []ProviderConfig

// Set model (uses active provider's model)
agent.SetModel("claude-opus-4-20250514")

// Get model
model := agent.GetModel()

// Get available models (from active provider)
models := agent.AvailableModels(ctx)  // → []ModelOption
```

---

## Management API Endpoints

### List Providers
```
GET /api/v1/providers
→ 200: { "providers": [ {...} ] }
```

### Create Provider
```
POST /api/v1/providers
← { "name": "...", "api_key": "...", "base_url": "...", ... }
→ 200: { "name": "...", "message": "provider added" }
→ 409: { "error": "already exists" }
```

### Update Provider
```
PUT /api/v1/providers/{name}
← { "api_key": "...", ... }  # update fields
→ 200: { "message": "provider updated" }
→ 404: { "error": "not found" }
```

### Delete Provider
```
DELETE /api/v1/providers/{name}
→ 200: { "message": "provider removed" }
→ 404: { "error": "not found" }
```

After deletion, `purgeProviderFromEngines(name)` removes it from all running agents.

---

## User Commands

```bash
# Switch provider (chat command)
/provider relay

# List providers (chat command)
/provider

# Switch model (chat command)
/model opus

# List available models (chat command)
/model

# CLI: Add provider at runtime
cc-connect-qhn provider add --name relay --api-key sk-... --base-url https://...

# CLI: List providers
cc-connect-qhn provider list --project my-project

# CLI: Delete provider
cc-connect-qhn provider delete --name relay
```

---

## Key Concepts

### Multi-Agent Support

One provider can serve multiple agents with different settings:

```toml
[[providers]]
name = "relay"
base_url = "https://relay.com/v1"

# Different endpoint for Codex
[providers.endpoints]
codex = "https://relay.com/v2/codex"

# Different default models
[providers.agent_models]
claudecode = "claude-opus-..."
codex = "glm-5.1"

# Different model lists
[providers.agent_model_lists.claudecode]
[[providers.agent_model_lists.claudecode]]
model = "claude-opus-4-20250514"
[[providers.agent_model_lists.claudecode]]
model = "claude-sonnet-4-20250514"

[providers.agent_model_lists.codex]
[[providers.agent_model_lists.codex]]
model = "glm-5.1"
```

### Agent Type Filtering

```toml
[[providers]]
name = "anthropic"
agent_types = ["claudecode"]  # Only Claude Code can use this

[[providers]]
name = "openai"
agent_types = ["codex"]  # Only Codex can use this

[[providers]]
name = "relay"
agent_types = ["claudecode", "codex"]  # Both can use this
```

### Thinking Override

For providers that don't support adaptive thinking, use a local proxy:

```toml
[[providers]]
name = "qwen"
base_url = "https://dashscope.aliyuncs.com/v1"
thinking = "disabled"  # Proxy will strip thinking parameters
```

---

## Sync Behavior

| Trigger | Config Updated | Engines Notified | Result |
|---------|----------------|------------------|--------|
| Manual config edit | ✅ | ❌ (reload needed) | Changes on next startup |
| CLI `provider add` | ✅ | ✅ | Running engines updated immediately |
| API `POST /providers` | ✅ | ✅ | Running engines updated immediately |
| API `PUT /providers/{name}` | ✅ | ✅ | Running engines updated immediately |
| API `DELETE /providers/{name}` | ✅ | ✅ | Provider removed from all agents immediately |
| Chat `/provider relay` | ❌ | N/A | Just switches active provider (memory only) |
| Chat `/model opus` | ❌ | N/A | Just switches active model (memory only) |

---

## Env Vars Passed to Agent

When spawning the Claude Code CLI, active provider settings become env vars:

```bash
# If custom base_url is set
ANTHROPIC_BASE_URL=https://api.relay-service.com
ANTHROPIC_AUTH_TOKEN=sk-relay-xxx  # Bearer auth instead of x-api-key
ANTHROPIC_API_KEY=                 # cleared

# Otherwise
ANTHROPIC_API_KEY=sk-ant-xxx

# Model (always)
ANTHROPIC_MODEL=claude-sonnet-4-20250514

# Extra env vars from provider.Env
KEY1=value1
KEY2=value2
```

---

## Common Patterns

### Pattern 1: Single Official API

```toml
[[providers]]
name = "anthropic"
api_key = "${ANTHROPIC_API_KEY}"
# base_url omitted → uses official Anthropic API

[[projects.agent]]
provider_refs = ["anthropic"]
```

### Pattern 2: Third-Party Relay

```toml
[[providers]]
name = "relay"
api_key = "sk-relay-xxx"
base_url = "https://api.relay.com/v1"
model = "claude-sonnet-4-20250514"

[[projects.agent]]
provider_refs = ["relay"]
```

### Pattern 3: Multi-Provider Switching

```toml
[[providers]]
name = "anthropic"
api_key = "${ANTHROPIC_API_KEY}"

[[providers]]
name = "minimaxi"
api_key = "${MINIMAXI_KEY}"
base_url = "https://api.minimaxi.chat/v1"

[[projects.agent]]
provider_refs = ["anthropic", "minimaxi"]
# User can switch between them via /provider command
```

### Pattern 4: Multi-Agent, Multi-Provider

```toml
# Serve both Claude Code and Codex from same provider
[[providers]]
name = "relay"
base_url = "https://relay.com/v1"
model = "claude-sonnet-4-20250514"

[providers.agent_models]
codex = "gpt-4o"

# Project 1: Claude Code
[[projects]]
name = "project1"
[projects.agent]
type = "claudecode"
provider_refs = ["relay"]

# Project 2: Codex
[[projects]]
name = "project2"
[projects.agent]
type = "codex"
provider_refs = ["relay"]
```

---

## Troubleshooting

**Q: Provider not showing up in /provider**
- Check `agent_types` restriction
- Verify provider_refs are spelled correctly
- Ensure `ResolveProviderRefs()` ran (happens during config load)

**Q: Model not switching**
- Check if agent implements `ModelSwitcher` (not all agents do)
- Verify model is in provider's `models` list
- Try full model name if alias doesn't work

**Q: API gives "not found"**
- Ensure provider name exists (case-sensitive)
- Check config file was reloaded

**Q: "thinking override" not working**
- Set `thinking = "disabled"` (or "enabled")
- Must also set `base_url` (proxy only runs for custom endpoints)

---

## See Also

- **Full Guide:** [PROVIDER_GUIDE.md](./PROVIDER_GUIDE.md)
- **Architecture Diagram:** [PROVIDER_ARCHITECTURE.md](./PROVIDER_ARCHITECTURE.md)
- **Config Example:** [config.example.toml](./config.example.toml)

