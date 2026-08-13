# Heron Connect Provider/Model Architecture Diagram

## 1. Data Flow: Config → Runtime → Agent Spawn

```
┌─────────────────────────────────────────────────────────────────┐
│ 1. CONFIG PARSING                                               │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  config.toml                                                    │
│  ├── [[providers]]          (global, shared across projects)    │
│  │   ├── name = "anthropic"                                    │
│  │   ├── api_key = "${ANTHROPIC_API_KEY}"                     │
│  │   ├── base_url = ""                                        │
│  │   ├── model = "claude-sonnet-4-6"                  │
│  │   ├── models = [                                          │
│  │   │   { model = "...", alias = "sonnet" }                │
│  │   └── ]                                                    │
│  │                                                             │
│  └── [[projects]]           (project-specific)                 │
│      ├── name = "my-backend"                                  │
│      ├── agent.provider_refs = ["anthropic", "minimaxi"]     │
│      └── agent.providers = [               (inline override)   │
│          { name = "relay", ... }                             │
│          ]                                                    │
│                                                                 │
│  config.Load(path) → ResolveProviderRefs() →                 │
│  ┌─────────────────────────────────────────┐                 │
│  │ Global providers merged into projects    │                 │
│  │ Per-agent-type overrides applied        │                 │
│  │ Inline providers take precedence        │                 │
│  └─────────────────────────────────────────┘                 │
│                                                                 │
│  Result: Project.Agent.Providers = [                         │
│    { name: "anthropic", model: "claude-sonnet-...", ... }   │
│    { name: "relay", model: "...", ... }                     │
│  ]                                                            │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
                            ↓
        ┌───────────────────────────────────────┐
        │ 2. AGENT INITIALIZATION               │
        ├───────────────────────────────────────┤
        │ Agent.New(opts map[string]any)       │
        │ • Creates agent instance             │
        │ • No providers set yet (activeIdx=-1) │
        │ • Returns ready agent                │
        └───────────────────────────────────────┘
                            ↓
┌──────────────────────────────────────────────────────────────────┐
│ 3. PROVIDER INJECTION                                            │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  if agent.(core.ProviderSwitcher) {                            │
│      agent.SetProviders(providers)   ← Set all providers      │
│      agent.SetActiveProvider(name)   ← Activate one           │
│  }                                                              │
│                                                                  │
│  Agent state:                                                   │
│  ├── providers: []ProviderConfig = [anthropic, relay, ...]    │
│  ├── activeIdx: int = 0 (pointing to "anthropic")            │
│  └── providerProxy: optional proxy for thinking override      │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
                            ↓
┌──────────────────────────────────────────────────────────────────┐
│ 4. SESSION START                                                 │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  agent.StartSession(workDir, options)                          │
│  │                                                              │
│  └─→ providerEnv() ← Build env vars for active provider      │
│      ├── If p.BaseURL != "":                                 │
│      │   • If p.Thinking != "": start proxy                  │
│      │   • Set ANTHROPIC_BASE_URL = p.BaseURL (or proxy)    │
│      │   • Set ANTHROPIC_AUTH_TOKEN = p.APIKey (Bearer)     │
│      │                                                         │
│      └── Else:                                               │
│          • Set ANTHROPIC_API_KEY = p.APIKey                 │
│                                                               │
│      • Set ANTHROPIC_MODEL = p.Model (if != "")             │
│      • Add p.Env extras (k=v pairs)                         │
│                                                               │
│  Result: env = [                                             │
│    "ANTHROPIC_BASE_URL=https://api.relay-service.com",      │
│    "ANTHROPIC_AUTH_TOKEN=sk-relay-xxx",                     │
│    "ANTHROPIC_MODEL=claude-sonnet-4-6",              │
│    ...other env vars...                                     │
│  ]                                                            │
│                                                               │
│  spawn claude --input-format stream-json ... (with env)     │
│                                                               │
└──────────────────────────────────────────────────────────────────┘
```

---

## 2. Provider State Management

```
┌─────────────────────────────────────────────────────────┐
│ Agent (ProviderSwitcher implementation)                 │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  struct Agent {                                         │
│    mu           sync.RWMutex                           │
│    providers    []ProviderConfig   ← All available    │
│    activeIdx    int                ← Active index (-1 if none)
│    model        string             ← Default model   │
│    ...                                                 │
│  }                                                     │
│                                                         │
│  ProviderSwitcher interface:                           │
│  ├── SetProviders([]ProviderConfig)                   │
│  ├── SetActiveProvider(name string) bool             │
│  ├── GetActiveProvider() *ProviderConfig             │
│  └── ListProviders() []ProviderConfig                │
│                                                         │
│  Example:                                               │
│  ┌──────────────────────────────────────┐            │
│  │ providers = [                        │            │
│  │   0: {name: "anthropic", ...}       │            │
│  │   1: {name: "relay", ...}           │            │
│  │   2: {name: "minimaxi", ...}        │            │
│  │ ]                                    │            │
│  │ activeIdx = 1 ← currently using "relay"          │
│  └──────────────────────────────────────┘            │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## 3. Configuration Hierarchy & Overrides

```
┌──────────────────────────────────────────────────────────────┐
│ LEVEL 0: DEFAULT (fallback when no provider/model)          │
├──────────────────────────────────────────────────────────────┤
│ Agent's built-in defaults:                                  │
│ • models = [sonnet, opus, haiku]                           │
│ • model = "sonnet"                                          │
│ • base_url = "https://api.anthropic.com/v1"               │
│ • api_key = ${ANTHROPIC_API_KEY}                          │
│                                                             │
└──────────────────────────────────────────────────────────────┘
                            ↑
┌──────────────────────────────────────────────────────────────┐
│ LEVEL 1: GLOBAL PROVIDERS (config.toml [[providers]])       │
├──────────────────────────────────────────────────────────────┤
│ Shared across all projects via provider_refs                │
│                                                             │
│ [[providers]]                                               │
│ name = "minimaxi-relay"                                    │
│ api_key = "${MINIMAXI_KEY}"                              │
│ base_url = "https://api.minimaxi.chat/v1"               │
│ model = "claude-sonnet-4-6"                       │
│ agent_types = ["claudecode", "codex"]                   │
│ thinking = "disabled"                                   │
│                                                             │
│ [providers.endpoints]          ← Per-agent-type overrides  │
│ codex = "https://api.minimaxi.chat/v2/codex"           │
│                                                             │
│ [providers.agent_models]       ← Per-agent-type models    │
│ claudecode = "claude-opus-4-8"                   │
│ codex = "glm-5.1"                                       │
│                                                             │
│ [providers.agent_model_lists.claudecode]                 │
│ [[providers.agent_model_lists.claudecode]]              │
│ model = "claude-opus-4-8"                        │
│ alias = "opus"                                          │
│ [[providers.agent_model_lists.claudecode]]              │
│ model = "claude-sonnet-4-6"                      │
│ alias = "sonnet"                                        │
│                                                             │
└──────────────────────────────────────────────────────────────┘
                            ↑
┌──────────────────────────────────────────────────────────────┐
│ LEVEL 2: INLINE PROVIDERS (config.toml [[projects]])        │
├──────────────────────────────────────────────────────────────┤
│ Project-specific; overrides global if same name             │
│                                                             │
│ [[projects.agent.providers]]                               │
│ name = "relay"                  ← Different name from global│
│ api_key = "sk-local-xxx"                                  │
│ base_url = "http://localhost:5000"                       │
│ model = "claude-opus-4-8"                         │
│                                                             │
│ [[projects.agent.providers.models]]                        │
│ model = "claude-opus-4-8"                         │
│ alias = "opus"                                           │
│                                                             │
└──────────────────────────────────────────────────────────────┘
                            ↑
┌──────────────────────────────────────────────────────────────┐
│ LEVEL 3: ACTIVE PROVIDER (runtime via /provider command)    │
├──────────────────────────────────────────────────────────────┤
│ User switches: /provider relay                             │
│ Agent: SetActiveProvider("relay") → activeIdx updated      │
│                                                             │
│ GetActiveProvider() → providers[activeIdx]                 │
│                                                             │
│ Active provider's:                                          │
│ • api_key → ANTHROPIC_AUTH_TOKEN env var                 │
│ • base_url → ANTHROPIC_BASE_URL env var                  │
│ • model → ANTHROPIC_MODEL env var                        │
│ • env → extra env vars                                   │
│                                                             │
└──────────────────────────────────────────────────────────────┘
                            ↑
┌──────────────────────────────────────────────────────────────┐
│ LEVEL 4: ACTIVE MODEL (runtime via /model command)          │
├──────────────────────────────────────────────────────────────┤
│ User switches: /model opus                                  │
│ Agent: SetModel("claude-opus-4-8")                  │
│ OR via provider.Models list (alias-based lookup)            │
│                                                             │
│ GetModel() → provider[activeIdx].Model or fallback         │
│                                                             │
│ Active model's full ID → ANTHROPIC_MODEL env var          │
│                                                             │
└──────────────────────────────────────────────────────────────┘
```

---

## 4. Management API Integration

```
┌─────────────────────────────────────────────────────────────┐
│ HTTP Management Server (port 9820)                         │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  GET /api/v1/providers                                    │
│  ├─→ listGlobalProviders()  [callback]                   │
│  └─→ Returns: { providers: [ {...} ] }                  │
│                                                             │
│  POST /api/v1/providers                                   │
│  ├─→ addGlobalProvider()  [callback]                     │
│  ├─→ Config file updated                                │
│  └─→ purgeProviderFromEngines() sync all agents         │
│                                                             │
│  PUT/PATCH /api/v1/providers/{name}                      │
│  ├─→ updateGlobalProvider()  [callback]                  │
│  ├─→ Config file updated                                │
│  └─→ purgeProviderFromEngines() sync                    │
│                                                             │
│  DELETE /api/v1/providers/{name}                         │
│  ├─→ removeGlobalProvider()  [callback]                  │
│  ├─→ Config file updated                                │
│  └─→ purgeProviderFromEngines() removes from agents    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
                            ↓
        ┌──────────────────────────────────────┐
        │ For each running engine:             │
        ├──────────────────────────────────────┤
        │ if agent.(ProviderSwitcher) {       │
        │   providers = agent.ListProviders() │
        │   remove matching provider          │
        │   agent.SetProviders(updated)      │
        │ }                                   │
        └──────────────────────────────────────┘
```

---

## 5. TOML Schema

```toml
# ═══════════════════════════════════════════════════════════════
# GLOBAL PROVIDERS (shared across projects)
# ═══════════════════════════════════════════════════════════════

[[providers]]
name            = "provider_name"      # string: unique identifier
api_key         = "${ENV_VAR}"         # string: supports env var substitution
base_url        = ""                   # string: custom API endpoint (optional)
model           = ""                   # string: default model (optional)
thinking        = ""                   # string: "enabled", "disabled", or empty
env             = {}                   # map[string]string: extra env vars
agent_types     = []                   # []string: restrict to ["claudecode", "codex", ...]
codex           = {}                   # CodexProviderConfig: Codex-specific settings

# Per-agent-type base URL overrides
[providers.endpoints]
claudecode      = "https://..."        # string: override base_url for this agent
codex           = "https://..."

# Per-agent-type model defaults
[providers.agent_models]
claudecode      = "claude-opus-..."    # string: override model for this agent
codex           = "gpt-4-..."

# Per-agent-type model lists (overrides global .models)
[providers.agent_model_lists]
# [providers.agent_model_lists.claudecode]
# [[providers.agent_model_lists.claudecode]]
# model = "claude-opus-4-8"
# alias = "opus"

# Codex-specific settings
[providers.codex]
wire_api        = ""                   # string: wire API format
http_headers    = {}                   # map[string]string: custom headers


# ═══════════════════════════════════════════════════════════════
# PROJECTS (reference global providers)
# ═══════════════════════════════════════════════════════════════

[[projects]]
name            = "my-project"

# Agent configuration
[projects.agent]
type            = "claudecode"         # string: agent type (claudecode, codex, etc.)
options         = {}                   # map[string]any: agent-specific options
provider_refs   = ["anthropic"]        # []string: reference global providers by name

# Inline providers (project-specific, override global if same name)
[[projects.agent.providers]]
name            = "relay"
api_key         = "sk-..."
base_url        = "http://..."
model           = "..."
thinking        = "disabled"

# Models available for this provider
[[projects.agent.providers.models]]
model           = "claude-sonnet-4-6"
alias           = "sonnet"

[[projects.agent.providers.models]]
model           = "claude-opus-4-8"
alias           = "opus"
```

---

## 6. Configuration Resolution Algorithm

```python
def ResolveProviderRefs(config):
    # Build global provider lookup
    globalByName = {}
    for p in config.Providers:
        globalByName[p.Name] = p
    
    # For each project
    for project in config.Projects:
        agentType = project.Agent.Type
        refs = project.Agent.ProviderRefs
        inlineNames = {p.Name for p in project.Agent.Providers}
        
        resolved = []
        
        # Resolve global provider refs
        for name in refs:
            # Skip if inline provider overrides
            if name in inlineNames:
                continue
            
            p = globalByName.get(name)
            if p is None:
                warn(f"Provider {name} not found")
                continue
            
            # Check agent_types restriction
            if p.AgentTypes and agentType not in p.AgentTypes:
                debug(f"Skipping {name}: agent type mismatch")
                continue
            
            # Apply per-agent-type overrides
            p = p.ResolveForAgent(agentType)
            resolved.append(p)
        
        # Final provider list: resolved refs + inline providers
        project.Agent.Providers = resolved + project.Agent.Providers


def ResolveForAgent(provider, agentType):
    # Apply per-agent-type overrides
    if agentType in provider.Endpoints:
        provider.BaseURL = provider.Endpoints[agentType]
    
    if agentType in provider.AgentModels:
        provider.Model = provider.AgentModels[agentType]
    
    if agentType in provider.AgentModelLists:
        provider.Models = provider.AgentModelLists[agentType]
    
    return provider
```

---

## 7. Runtime Switching Flow

```
User Input:
    /provider relay
         ↓
    engine.HandleCommand(/provider relay)
         ↓
    if agent.(ProviderSwitcher) {
        agent.SetActiveProvider("relay")
    }
         ↓
    Agent updates:
    ├── activeIdx = 1 (pointing to "relay" provider)
    └── Next session will use:
        • ANTHROPIC_BASE_URL=https://api.relay-service.com
        • ANTHROPIC_AUTH_TOKEN=sk-relay-xxx
        • ANTHROPIC_MODEL=...


User Input:
    /model opus
         ↓
    engine.HandleCommand(/model opus)
         ↓
    if agent.(ModelSwitcher) {
        agent.SetModel("claude-opus-4-8")
    }
         ↓
    Agent updates:
    ├── model = "claude-opus-4-8"
    └── Next session will use:
        • ANTHROPIC_MODEL=claude-opus-4-8
```

---

## 8. Key Takeaways

1. **Struct Hierarchy:**
   - TOML level: `config.ProviderConfig` (with agent-type overrides)
   - Runtime: `core.ProviderConfig` (after resolution)
   - API: `management.GlobalProviderInfo` (JSON-serializable)

2. **Resolution Steps:**
   - Parse TOML → Load config
   - Resolve global providers into projects
   - Apply per-agent-type overrides
   - Merge with inline providers (inline takes precedence)
   - Set providers on agent via `SetProviders()`
   - Set active provider via `SetActiveProvider()`

3. **Agent Integration:**
   - Agents that support `ProviderSwitcher` can switch providers at runtime
   - Active provider determines:
     - API credentials (api_key, base_url)
     - Model (ANTHROPIC_MODEL)
     - Extra env vars
     - Thinking override (local proxy if needed)

4. **Management API:**
   - REST endpoints for CRUD on global providers
   - Changes automatically sync to running engines
   - Callbacks allow config persistence

5. **Multi-Agent Support:**
   - Same provider can have different settings per agent type
   - `agent_types` restriction filters what agents can use a provider
   - `endpoints`, `agent_models`, `agent_model_lists` per-agent overrides

