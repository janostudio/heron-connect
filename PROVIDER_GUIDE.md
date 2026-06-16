# CC-Connect: Custom Models & Providers Architecture

## Overview

CC-Connect supports **runtime provider/model switching** through a flexible system that allows:
- Defining providers globally (shared across projects)
- Referencing global providers in projects
- Defining inline providers per project
- Switching providers and models at runtime via CLI commands (`/provider`, `/model`)
- Managing providers via HTTP REST API (management server)

---

## 1. Struct Definitions & Data Models

### 1.1 Core Types (`core/interfaces.go`)

#### `ProviderConfig` — API Provider Configuration
Stored in `core/interfaces.go` lines 331-342:

```go
type ProviderConfig struct {
    Name     string
    APIKey   string
    BaseURL  string
    Model    string
    Models   []ModelOption     // pre-configured list of available models
    Thinking string            // "disabled", "enabled", or "" (no rewrite)
    Env      map[string]string // arbitrary extra env vars
    // Codex-specific provider config
    CodexWireAPI     string
    CodexHTTPHeaders map[string]string
}
```

**Fields:**
- **Name**: Unique provider identifier (e.g., "anthropic", "minimaxi-claude")
- **APIKey**: API authentication key (can use `${ENV_VAR}` syntax)
- **BaseURL**: Custom API endpoint (default = official Anthropic API)
- **Model**: Default model for this provider (e.g., "claude-sonnet-4-20250514")
- **Models**: Array of `ModelOption` entries (for `/model` command)
- **Thinking**: Override adaptive thinking setting ("disabled", "enabled", or empty)
- **Env**: Extra environment variables passed to the agent
- **CodexWireAPI** / **CodexHTTPHeaders**: Codex CLI-specific config (maps to Codex's `[model_providers.<name>]`)

#### `ModelOption` — Selectable Model
Defined in `core/interfaces.go` lines 379-383:

```go
type ModelOption struct {
    Name  string // model identifier (e.g., "claude-sonnet-4-20250514")
    Desc  string // short description for display (optional)
    Alias string // short alias for /model command (e.g., "sonnet")
}
```

#### `ProviderSwitcher` Interface — Runtime Switching
Defined in `core/interfaces.go` lines 345-350:

```go
type ProviderSwitcher interface {
    SetProviders(providers []ProviderConfig)
    SetActiveProvider(name string) bool
    GetActiveProvider() *ProviderConfig
    ListProviders() []ProviderConfig
}
```

Implemented by agents (e.g., `claudecode`) to support runtime provider switching.

### 1.2 Configuration Types (`config/config.go`)

#### `ProviderConfig` (TOML Config)
Defined in `config/config.go` lines 407-420:

```go
type ProviderConfig struct {
    Name            string                           // provider name
    APIKey          string                           // API key
    BaseURL         string                           // custom endpoint
    Model           string                           // default model
    Models          []ProviderModelConfig            // available models
    Thinking        string                           // thinking override
    Env             map[string]string                // env vars
    AgentTypes      []string                         // restrict to agents (e.g., ["claudecode", "codex"])
    Endpoints       map[string]string                // per-agent-type base URLs
    AgentModels     map[string]string                // per-agent-type default models
    AgentModelLists map[string][]ProviderModelConfig // per-agent-type model lists
    Codex           *CodexProviderConfig             // Codex-specific config
}
```

**Key differences from `core.ProviderConfig`:**
- `AgentTypes`, `Endpoints`, `AgentModels`, `AgentModelLists` for multi-agent support
- `Models` is `[]ProviderModelConfig` instead of `[]ModelOption` (TOML level)

#### `ProviderModelConfig` (TOML Model Entry)
Defined in `config/config.go` lines 402-405:

```go
type ProviderModelConfig struct {
    Model string // model ID
    Alias string // optional short alias for /model command
}
```

#### `AgentConfig` — Per-Project Agent Configuration
Defined in `config/config.go` lines 393-398:

```go
type AgentConfig struct {
    Type         string           // agent type (e.g., "claudecode", "codex")
    Options      map[string]any   // agent-specific options
    ProviderRefs []string         // references to global [[providers]] by name
    Providers    []ProviderConfig // inline providers (project-specific)
}
```

**How it works:**
- `ProviderRefs`: List of global provider names to use
- `Providers`: Inline provider definitions (override or supplement global ones)

---

## 2. Configuration Examples

### 2.1 Global Providers (`config.toml` — `[[providers]]`)

```toml
[[providers]]
name = "anthropic"
api_key = "${ANTHROPIC_API_KEY}"
agent_types = ["claudecode"]  # restrict to Claude Code only

[[providers]]
name = "minimaxi-claude"
api_key = "${MINIMAXI_API_KEY}"
base_url = "https://api.minimaxi.chat/v1"
agent_types = ["claudecode"]
model = "claude-sonnet-4-20250514"

[[providers]]
name = "minimaxi-codex"
api_key = "${MINIMAXI_API_KEY}"
base_url = "https://api.minimaxi.chat/v1"
agent_types = ["codex"]
model = "MiniMax-M2.7"

# Multi-agent provider with per-agent overrides
[[providers]]
name = "relay-service"
api_key = "sk-relay-xxx"
base_url = "https://api.relay-service.com"
model = "claude-sonnet-4-20250514"  # default fallback
thinking = "disabled"  # disable adaptive thinking for this provider

# Per-agent-type base URLs (different endpoint for Codex)
[providers.endpoints]
codex = "https://api.relay-service.com/v2/codex"

# Per-agent-type default models
[providers.agent_models]
claudecode = "claude-opus-4-20250514"
codex = "openai/gpt-5.3-codex"

# Per-agent-type model lists (overrides Models when matched)
[providers.agent_model_lists.claudecode]
[[providers.agent_model_lists.claudecode]]
model = "claude-opus-4-20250514"
alias = "opus"
[[providers.agent_model_lists.claudecode]]
model = "claude-sonnet-4-20250514"
alias = "sonnet"
```

### 2.2 Project-Level Providers (`[[projects]]`)

```toml
[[projects]]
name = "my-backend"
agent = { type = "claudecode" }

# Option 1: Reference global providers
[projects.agent]
provider_refs = ["anthropic", "minimaxi-claude", "dashscope"]

# Option 2: Inline providers (project-specific)
[[projects.agent.providers]]
name = "relay"
api_key = "sk-relay-xxx"
base_url = "https://api.relay-service.com"
model = "claude-sonnet-4-20250514"

# Define available models for this provider
[[projects.agent.providers.models]]
model = "claude-sonnet-4-20250514"
alias = "sonnet"

[[projects.agent.providers.models]]
model = "claude-opus-4-20250514"
alias = "opus"

[[projects.agent.providers.models]]
model = "claude-haiku-3-5-20241022"
alias = "haiku"
```

**Resolution order:**
1. Global providers (via `provider_refs`) are resolved first
2. Inline providers are appended
3. If inline has the same name as global, the **inline wins**

---

## 3. Configuration Parsing & Resolution

### 3.1 TOML Loading (`config/config.go` line 454)

```go
func Load(path string) (*Config, error) {
    // ...
    cfg.ResolveProviderRefs()  // Merge global providers into projects
    // ...
}
```

### 3.2 Provider Reference Resolution (`config/config.go` lines 1110-1147)

**`ResolveProviderRefs()`** merges global providers into each project:

```go
func (cfg *Config) ResolveProviderRefs() {
    // 1. Build global provider map by name
    globalByName := make(map[string]ProviderConfig, len(cfg.Providers))
    for _, p := range cfg.Providers {
        globalByName[p.Name] = p
    }
    
    // 2. For each project
    for i := range cfg.Projects {
        refs := cfg.Projects[i].Agent.ProviderRefs
        if len(refs) == 0 {
            continue
        }
        
        agentType := cfg.Projects[i].Agent.Type
        inlineNames := make(map[string]bool, len(cfg.Projects[i].Agent.Providers))
        for _, p := range cfg.Projects[i].Agent.Providers {
            inlineNames[p.Name] = true
        }
        
        // 3. For each referenced name
        var resolved []ProviderConfig
        for _, name := range refs {
            if inlineNames[name] {
                continue  // inline override takes precedence
            }
            gp := globalByName[name]
            
            // 4. Check agent_types restriction
            if len(gp.AgentTypes) > 0 && !contains(gp.AgentTypes, agentType) {
                continue  // skip if agent type mismatch
            }
            
            // 5. Apply per-agent-type overrides (base_url, model, models list)
            resolved = append(resolved, gp.ResolveForAgent(agentType))
        }
        
        // 6. Prepend resolved, then inline (inline can override)
        cfg.Projects[i].Agent.Providers = append(resolved, cfg.Projects[i].Agent.Providers...)
    }
}
```

### 3.3 Per-Agent Overrides (`config/config.go` lines 1151-1162)

**`ResolveForAgent(agentType)`** applies per-agent-type overrides:

```go
func (p ProviderConfig) ResolveForAgent(agentType string) ProviderConfig {
    // Apply per-agent-type base URL override
    if ep, ok := p.Endpoints[agentType]; ok && ep != "" {
        p.BaseURL = ep
    }
    
    // Apply per-agent-type model override
    if am, ok := p.AgentModels[agentType]; ok && am != "" {
        p.Model = am
    }
    
    // Apply per-agent-type model list override (takes precedence over Models)
    if aml, ok := p.AgentModelLists[agentType]; ok && len(aml) > 0 {
        p.Models = aml
    }
    
    return p
}
```

---

## 4. Agent Adapter Implementation

### 4.1 Claude Code (`agent/claudecode/claudecode.go`)

#### Initialization — Providers Set from Config
Lines 109-226 (`New()` constructor):

```go
func New(opts map[string]any) (core.Agent, error) {
    // ... (other option parsing) ...
    
    // No providers set here — they're set later by the engine
    // via SetProviders()
    return &Agent{
        // ...
        providers: []core.ProviderConfig{},
        activeIdx: -1,  // no active provider yet
    }, nil
}
```

#### `ProviderSwitcher` Interface Implementation
Lines 966-1007:

```go
func (a *Agent) SetProviders(providers []core.ProviderConfig) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.providers = providers
}

func (a *Agent) SetActiveProvider(name string) bool {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.stopProviderProxyLocked()
    
    if name == "" {
        a.activeIdx = -1
        slog.Info("claudecode: provider cleared")
        return true
    }
    
    // Find provider by name
    for i, p := range a.providers {
        if p.Name == name {
            a.activeIdx = i
            slog.Info("claudecode: provider switched", "provider", name)
            return true
        }
    }
    return false
}

func (a *Agent) GetActiveProvider() *ProviderConfig {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.activeIdx < 0 || a.activeIdx >= len(a.providers) {
        return nil
    }
    p := a.providers[a.activeIdx]
    return &p
}

func (a *Agent) ListProviders() []ProviderConfig {
    a.mu.Lock()
    defer a.mu.Unlock()
    result := make([]ProviderConfig, len(a.providers))
    copy(result, a.providers)
    return result
}
```

#### Model/Provider Env Resolution
Lines 1018-1064:

When spawning Claude Code CLI, the agent applies provider settings:

```go
func (a *Agent) providerEnvLocked() []string {
    if a.activeIdx < 0 || a.activeIdx >= len(a.providers) {
        a.stopProviderProxyLocked()
        return nil
    }
    p := a.providers[a.activeIdx]
    var env []string

    // If custom base_url is set
    if p.BaseURL != "" {
        // If thinking override is needed, start a local proxy
        if p.Thinking != "" {
            if err := a.ensureProviderProxyLocked(p.BaseURL, p.Thinking); err != nil {
                env = append(env, "ANTHROPIC_BASE_URL="+p.BaseURL)
            } else {
                env = append(env, "ANTHROPIC_BASE_URL="+a.proxyLocalURL)
            }
        } else {
            env = append(env, "ANTHROPIC_BASE_URL="+p.BaseURL)
        }
        
        // Use Bearer token auth for third-party providers
        if p.APIKey != "" {
            env = append(env, "ANTHROPIC_AUTH_TOKEN="+p.APIKey)
            env = append(env, "ANTHROPIC_API_KEY=")  // clear
        }
        
        // Set model
        if p.Model != "" {
            env = append(env, "ANTHROPIC_MODEL="+p.Model)
        }
    } else {
        // Use standard ANTHROPIC_API_KEY for official Anthropic API
        if p.APIKey != "" {
            env = append(env, "ANTHROPIC_API_KEY="+p.APIKey)
        }
    }

    // Add extra env vars
    for k, v := range p.Env {
        env = append(env, k+"="+v)
    }
    
    return env
}
```

#### Getting Available Models
Lines 312-331:

```go
func (a *Agent) configuredModels() []core.ModelOption {
    a.mu.RLock()
    defer a.mu.RUnlock()
    return core.GetProviderModels(a.providers, a.activeIdx)
}

func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
    // 1. Check pre-configured models from active provider
    if models := a.configuredModels(); len(models) > 0 {
        return models
    }
    
    // 2. Try to fetch from provider API
    if models := a.fetchModelsFromAPI(ctx); len(models) > 0 {
        return models
    }
    
    // 3. Fall back to built-in list
    return []core.ModelOption{
        {Name: "sonnet", Desc: "Claude Sonnet (balanced)"},
        {Name: "opus", Desc: "Claude Opus (most capable)"},
        {Name: "haiku", Desc: "Claude Haiku (fastest)"},
    }
}
```

#### Helper Functions (`core/provider.go`)

```go
// Get available models for active provider
func GetProviderModels(providers []ProviderConfig, activeIdx int) []ModelOption {
    if activeIdx < 0 || activeIdx >= len(providers) {
        return nil
    }
    return providers[activeIdx].Models
}

// Get model for active provider (with fallback)
func GetProviderModel(providers []ProviderConfig, activeIdx int, fallback string) string {
    if activeIdx < 0 || activeIdx >= len(providers) {
        return fallback
    }
    if model := providers[activeIdx].Model; model != "" {
        return model
    }
    return fallback
}

// Update model for a provider by name (returns new slice)
func SetProviderModel(providers []ProviderConfig, name, model string) ([]ProviderConfig, bool) {
    updated := make([]ProviderConfig, len(providers))
    copy(updated, providers)
    for i := range updated {
        if updated[i].Name == name {
            updated[i].Model = model
            return updated, true
        }
    }
    return updated, false
}
```

---

## 5. Management API (`management/mgmt_server.go`)

### 5.1 Global Provider CRUD Endpoints

**HTTP endpoints for runtime provider management:**

#### GET `/api/v1/providers` — List All Providers
Lines 1584-1596:

```
GET /api/v1/providers

Response (200 OK):
{
  "providers": [
    {
      "name": "anthropic",
      "api_key": "sk-ant-...",
      "base_url": "",
      "model": "claude-sonnet-4-20250514",
      "thinking": "",
      "env": {...},
      "agent_types": ["claudecode"],
      "models": [
        {"model": "claude-sonnet-4-20250514", "alias": "sonnet"},
        {"model": "claude-opus-4-20250514", "alias": "opus"}
      ],
      "endpoints": {...},
      "agent_models": {...},
      "agent_model_lists": {...},
      "codex": {...}
    }
  ]
}
```

#### POST `/api/v1/providers` — Create Provider
Lines 1598-1620:

```
POST /api/v1/providers

Request Body:
{
  "name": "relay",
  "api_key": "sk-relay-xxx",
  "base_url": "https://api.relay-service.com",
  "model": "claude-sonnet-4-20250514",
  "thinking": "disabled",
  "env": {...},
  "agent_types": ["claudecode"],
  "models": [
    {"model": "claude-sonnet-4-20250514", "alias": "sonnet"}
  ],
  "endpoints": {...},
  "agent_models": {...},
  "agent_model_lists": {...}
}

Response (200 OK):
{
  "name": "relay",
  "message": "provider added"
}

Response (409 Conflict):
{
  "error": "provider 'relay' already exists"
}
```

#### PUT/PATCH `/api/v1/providers/{name}` — Update Provider
Lines 1651-1669:

```
PUT /api/v1/providers/relay
PATCH /api/v1/providers/relay

Request Body: (same as POST)

Response (200 OK):
{
  "message": "provider updated"
}

Response (404 Not Found):
{
  "error": "provider 'relay' not found"
}
```

#### DELETE `/api/v1/providers/{name}` — Delete Provider
Lines 1671-1685:

```
DELETE /api/v1/providers/relay

Response (200 OK):
{
  "message": "provider removed"
}

Response (404 Not Found):
{
  "error": "provider 'relay' not found"
}
```

After deletion, the management server purges the provider from all running engines:

```go
func (m *ManagementServer) purgeProviderFromEngines(name string) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    for _, e := range m.engines {
        ps, ok := e.GetAgent().(core.ProviderSwitcher)
        if !ok {
            continue  // agent doesn't support provider switching
        }
        providers := ps.ListProviders()
        for i, p := range providers {
            if p.Name == name {
                ps.SetProviders(append(providers[:i], providers[i+1:]...))
                break
            }
        }
    }
}
```

### 5.2 Management API Types

#### `GlobalProviderInfo` — Wire Type for API
Lines 135-163:

```go
type GlobalProviderInfo struct {
    Name       string            `json:"name"`
    APIKey     string            `json:"api_key,omitempty"`
    BaseURL    string            `json:"base_url,omitempty"`
    Model      string            `json:"model,omitempty"`
    Thinking   string            `json:"thinking,omitempty"`
    Env        map[string]string `json:"env,omitempty"`
    AgentTypes []string          `json:"agent_types,omitempty"`
    Models     []struct {
        Model string `json:"model"`
        Alias string `json:"alias,omitempty"`
    } `json:"models,omitempty"`
    Endpoints       map[string]string             `json:"endpoints,omitempty"`
    AgentModels     map[string]string             `json:"agent_models,omitempty"`
    AgentModelLists map[string][]GlobalModelEntry `json:"agent_model_lists,omitempty"`
    Codex           *GlobalCodexConfig            `json:"codex,omitempty"`
}

type GlobalModelEntry struct {
    Model string `json:"model"`
    Alias string `json:"alias,omitempty"`
}

type GlobalCodexConfig struct {
    WireAPI     string            `json:"wire_api,omitempty"`
    HTTPHeaders map[string]string `json:"http_headers,omitempty"`
}
```

### 5.3 Provider Presets & CC-Switch Migration

#### GET `/api/v1/providers/presets` — Fetch Provider Presets
Lines 1712-1727:

Fetches recommended providers from `provider_presets_url` (GitHub/Gitee).

#### GET `/api/v1/providers/cc-switch` — List CC-Switch Providers
Lines 1729-1741:

Lists providers from the cc-switch database (if available).

#### POST `/api/v1/providers/cc-switch` — Migrate Providers from CC-Switch
Lines 1743-1801:

```
POST /api/v1/providers/cc-switch

Request Body:
{
  "names": ["anthropic", "minimaxi"]
}

Response (200 OK):
{
  "imported": ["anthropic"],
  "skipped": ["minimaxi"]
}
```

---

## 6. Wiring in Engine & Projects

### 6.1 Engine Setup (simplified)

When cc-connect starts a project:

1. **Parse config** → load providers via `ResolveProviderRefs()`
2. **Create agent** → call `New(opts)` (providers not set yet)
3. **Set providers** → call `agent.SetProviders(providers)` if agent supports `ProviderSwitcher`
4. **Set initial provider** → call `agent.SetActiveProvider(name)` to activate one

### 6.2 Management Server Registration

When a provider is added/updated/removed via the API:

1. Callback is invoked (`addGlobalProvider`, `updateGlobalProvider`, `removeGlobalProvider`)
2. Config file is updated with new provider
3. Management server syncs running engines via `purgeProviderFromEngines()`
4. Agents are notified of provider changes at runtime

---

## 7. Field Mapping Summary

| Level | Struct | Fields |
|-------|--------|--------|
| **TOML (Global)** | `config.ProviderConfig` | `name`, `api_key`, `base_url`, `model`, `models`, `thinking`, `env`, `agent_types`, `endpoints`, `agent_models`, `agent_model_lists`, `codex` |
| **TOML (Project)** | `config.ProviderConfig` | Same as above (local override) |
| **TOML (Model)** | `config.ProviderModelConfig` | `model`, `alias` |
| **Runtime (Agent)** | `core.ProviderConfig` | `name`, `api_key`, `base_url`, `model`, `models`, `thinking`, `env`, `codex_wire_api`, `codex_http_headers` |
| **Runtime (Model)** | `core.ModelOption` | `name`, `desc`, `alias` |
| **API Wire** | `management.GlobalProviderInfo` | Same as TOML (with JSON tags) |

---

## 8. Example: Adding a New Provider at Runtime

### Via Management API:

```bash
curl -X POST http://localhost:9820/api/v1/providers \
  -H "Authorization: Bearer ${MGMT_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "azure-gpt",
    "api_key": "sk-azure-xxx",
    "base_url": "https://my-azure.openai.azure.com/v1",
    "model": "gpt-4o",
    "agent_types": ["claudecode"],
    "models": [
      {"model": "gpt-4o", "alias": "gpt4o"},
      {"model": "gpt-4-turbo", "alias": "turbo"}
    ]
  }'
```

### Via CLI:

```bash
cc-connect provider add \
  --name azure-gpt \
  --api-key sk-azure-xxx \
  --base-url https://my-azure.openai.azure.com/v1 \
  --model gpt-4o
```

### What happens:

1. Config file is updated: new `[[providers]]` entry added
2. All running engines are notified
3. Agents that support `ProviderSwitcher` get the new provider
4. Users can then switch to it via `/provider azure-gpt`

---

## Summary

**Provider/Model Flow:**
1. **Config** → Global `[[providers]]` + per-project `[[projects.agent.providers]]`
2. **Parse** → `ResolveProviderRefs()` applies per-agent-type overrides
3. **Runtime** → Agent implements `ProviderSwitcher` to switch providers/models
4. **Agent Spawn** → Active provider's credentials/model set as env vars
5. **API** → Management server enables CRUD at runtime

**Key Capabilities:**
- ✅ Multiple providers per project
- ✅ Per-provider model lists
- ✅ Per-agent-type overrides (different models/endpoints for Claude Code vs Codex)
- ✅ Runtime provider switching (`/provider` command)
- ✅ Runtime model switching (`/model` command)
- ✅ HTTP REST API for management tools
- ✅ Environment variable substitution (`${API_KEY}`)
- ✅ Third-party provider support (Azure, Minimaxi, Qwen, etc.)

