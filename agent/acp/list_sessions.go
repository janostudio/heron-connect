package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// listSessionsProbeTimeout bounds how long we wait for a one-shot
// `session/list` round-trip before giving up. Keep this short — the
// whole point of the probe is that it's quick; if the ACP agent is
// slow we'd rather return nothing than block `/ls` in IM.
var listSessionsProbeTimeout = 15 * time.Second

// acpModeInfo doubles as a mode or model descriptor: ACP servers
// historically return {id, name, description} for both. For models
// in configOptions the "value" field is the id and "name" is the
// label — we normalise via acpConfigOption.
type acpModeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// acpModesBlock mirrors the `modes` object returned inside `session/new`
// and `session/load` responses.
type acpModesBlock struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []acpModeInfo `json:"availableModes"`
}

// acpModelsBlock mirrors the legacy `models` object returned inside
// `session/new` and `session/load` responses. Newer ACP servers
// (CodeBuddy included) expose models via `configOptions` with
// category "model" instead, but we support both for compatibility.
//
// Note: the model entries in the legacy `models.availableModels`
// array use `modelId` as the identifier field (not `id` like modes),
// so we use a dedicated acpModelEntry type here.
type acpModelsBlock struct {
	CurrentModelID  string          `json:"currentModelId"`
	AvailableModels []acpModelEntry `json:"availableModels"`
}

// acpModelEntry mirrors one entry in the legacy `models.availableModels`
// array. CodeBuddy returns {modelId, name, description}.
type acpModelEntry struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// acpConfigOption mirrors an entry in the `configOptions` array from
// session/new / session/load / config_option_update. The ACP spec
// (Session Config Options) defines category "model" for the model
// selector and category "mode" for the mode selector.
type acpConfigOption struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Category     string              `json:"category"`
	Type         string              `json:"type"`
	CurrentValue string              `json:"currentValue"`
	Options      []acpConfigOptValue `json:"options"`
}

// UnmarshalJSON tolerates both string and boolean encodings of the
// currentValue field. The ACP spec types currentValue as a string, but
// some agents (notably recent codebuddy builds) emit a bare JSON boolean
// for type:"boolean" options such as "multitask". We normalise both to a
// string so downstream model parsing stays string-typed.
func (o *acpConfigOption) UnmarshalJSON(data []byte) error {
	type plain struct {
		ID           string              `json:"id"`
		Name         string              `json:"name"`
		Category     string              `json:"category"`
		Type         string              `json:"type"`
		CurrentValue json.RawMessage     `json:"currentValue"`
		Options      []acpConfigOptValue `json:"options"`
	}
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	o.ID = p.ID
	o.Name = p.Name
	o.Category = p.Category
	o.Type = p.Type
	o.Options = p.Options

	if len(p.CurrentValue) == 0 {
		o.CurrentValue = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(p.CurrentValue, &s); err == nil {
		o.CurrentValue = s
		return nil
	}
	var b bool
	if err := json.Unmarshal(p.CurrentValue, &b); err == nil {
		o.CurrentValue = strconv.FormatBool(b)
		return nil
	}
	var n float64
	if err := json.Unmarshal(p.CurrentValue, &n); err == nil {
		o.CurrentValue = strconv.FormatFloat(n, 'f', -1, 64)
		return nil
	}
	// Anything else (null, object, array): keep the raw JSON text so we
	// never error out, but normalise a bare null to the empty string.
	if string(p.CurrentValue) == "null" {
		o.CurrentValue = ""
		return nil
	}
	o.CurrentValue = string(p.CurrentValue)
	return nil
}

type acpConfigOptValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// parseModels extracts (current, available) from either configOptions
// (category "model", the v2 ACP way) or the legacy models block. When
// both are present, configOptions wins. Returns ("", nil) if neither
// contains a model selector.
func parseModels(configOptions []acpConfigOption, legacy *acpModelsBlock) (string, []core.ModelOption) {
	for _, opt := range configOptions {
		if opt.Category != "model" {
			continue
		}
		available := make([]core.ModelOption, 0, len(opt.Options))
		for _, v := range opt.Options {
			available = append(available, core.ModelOption{
				Name: v.Value,
				Desc: v.Name,
			})
		}
		return opt.CurrentValue, available
	}
	if legacy != nil && len(legacy.AvailableModels) > 0 {
		available := make([]core.ModelOption, 0, len(legacy.AvailableModels))
		for _, m := range legacy.AvailableModels {
			available = append(available, core.ModelOption{
				Name: m.ModelID,
				Desc: m.Name,
			})
		}
		return legacy.CurrentModelID, available
	}
	return "", nil
}

// acpInitializeResult is the subset of `initialize` fields this package
// cares about. Additional vendor metadata is ignored.
type acpInitializeResult struct {
	ProtocolVersion   int `json:"protocolVersion"`
	AgentCapabilities struct {
		LoadSession         bool `json:"loadSession"`
		SessionCapabilities struct {
			// ACP advertises capabilities as objects (possibly empty);
			// treat "field present" as "supported" regardless of contents.
			List json.RawMessage `json:"list,omitempty"`
		} `json:"sessionCapabilities"`
	} `json:"agentCapabilities"`
}

// acpSessionListResult mirrors a `session/list` response.
type acpSessionListResult struct {
	Sessions []acpSessionListEntry `json:"sessions"`
}

type acpSessionListEntry struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// probeSpawn launches `<cmd> <args...>`, sets up a JSON-RPC transport
// and starts its readLoop. The caller owns the returned `teardown`
// func and must invoke it to reap the child process.
func (a *Agent) probeSpawn(ctx context.Context, cwd string) (*transport, *bytes.Buffer, func(), error) {
	cmd := exec.CommandContext(ctx, a.command, a.args...)
	cmd.Dir = cwd
	cmd.Env = core.MergeEnv(os.Environ(), a.extraEnv)
	// Put the probe process in its own group so teardown can kill it
	// reliably (including any grandchildren it spawns).
	core.PrepareCmdForKill(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acp: probe stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acp: probe stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(&stderrBuf)

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("acp: probe start %s: %w", a.command, err)
	}

	// The server-request handler needs to reference `tr` itself in order
	// to respondError; declare via var so the closure captures the
	// variable (which is assigned to a *transport below) rather than an
	// uninitialised copy.
	var tr *transport
	tr = newTransport(stdout, stdin,
		func(method string, _ json.RawMessage) {
			slog.Debug("acp-probe: notification", "method", method)
		},
		func(_ string, id json.RawMessage, _ json.RawMessage) {
			_ = tr.respondError(id, -32601, "probe: method not implemented")
		},
	)

	readCtx, cancelRead := context.WithCancel(ctx)
	go tr.readLoop(readCtx)

	teardown := func() {
		cancelRead()
		_ = stdin.Close()
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// Kill the entire process group, not just the main process,
			// so grandchildren (e.g. shell tools the CLI spawned) are
			// reaped too.
			_ = core.ForceKillProcessGroup(cmd)
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				// Last resort: abandon the wait goroutine.
			}
		}
	}
	return tr, &stderrBuf, teardown, nil
}

// probeInitialize performs the ACP handshake on an already-spawned
// transport and returns the parsed initialize result.
func probeInitialize(ctx context.Context, tr *transport) (*acpInitializeResult, error) {
	raw, err := tr.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{
			"name":    "heron-connect",
			"title":   "heron-connect",
			"version": core.CurrentVersion,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("acp: probe initialize: %w", err)
	}
	var res acpInitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("acp: probe parse initialize: %w", err)
	}
	return &res, nil
}

// probeListSessions runs `session/list` on the given transport.
// Returns (nil, nil) if the agent refuses the call with
// method-not-found / invalid-request — callers interpret that as
// "unsupported" rather than "real error".
func probeListSessions(ctx context.Context, tr *transport, cwdFilter string) ([]acpSessionListEntry, error) {
	params := map[string]any{}
	if cwdFilter != "" {
		params["cwd"] = cwdFilter
	}
	raw, err := tr.call(ctx, "session/list", params)
	if err != nil {
		if rpcErr, ok := err.(*rpcErrPayload); ok {
			if rpcErr.Code == -32601 || rpcErr.Code == -32600 {
				return nil, nil
			}
		}
		return nil, err
	}
	var out acpSessionListResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("acp: parse session/list: %w", err)
	}
	return out.Sessions, nil
}

// ListSessions returns past sessions reported by the ACP agent, scoped
// to the agent's work_dir. If the agent does not advertise
// sessionCapabilities.list or the call soft-fails, falls back to the
// locally tracked session list (populated on StartSession).
//
// This runs a one-shot `<command>` process that performs only
// initialize + session/list, so it does NOT allocate a real session on
// the backend (unlike session/new). Cost is roughly a single ACP
// handshake round-trip (~100-500ms for Devin).
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	if a.listUnsupported.Load() {
		// Already learned this agent doesn't support session/list;
		// fast-path to the local fallback.
		return a.localFallback(), nil
	}

	a.mu.RLock()
	workDir := a.workDir
	a.mu.RUnlock()
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		absWorkDir = workDir
	}

	probeCtx, cancel := context.WithTimeout(ctx, listSessionsProbeTimeout)
	defer cancel()

	started := time.Now()
	tr, stderrBuf, teardown, err := a.probeSpawn(probeCtx, absWorkDir)
	if err != nil {
		// Spawn failed — fall back to local rather than blocking /list.
		slog.Warn("acp: probe spawn failed, using local fallback", "error", err)
		return a.localFallback(), nil
	}
	defer teardown()

	init, err := probeInitialize(probeCtx, tr)
	if err != nil {
		slog.Warn("acp: probe initialize failed, using local fallback",
			"error", err,
			"stderr", truncateForLog(strings.TrimSpace(stderrBuf.String()), 200),
		)
		return a.localFallback(), nil
	}
	if len(init.AgentCapabilities.SessionCapabilities.List) == 0 {
		slog.Info("acp: session/list unsupported by agent, caching + using local fallback", "command", a.command)
		a.listUnsupported.Store(true)
		return a.localFallback(), nil
	}

	entries, err := probeListSessions(probeCtx, tr, absWorkDir)
	if err != nil {
		slog.Warn("acp: session/list call failed, using local fallback", "error", err)
		return a.localFallback(), nil
	}
	if entries == nil {
		a.listUnsupported.Store(true)
		return a.localFallback(), nil
	}
	out := convertSessionList(entries, absWorkDir)
	slog.Info("acp: session/list completed",
		"total_entries", len(entries),
		"matching_cwd", len(out),
		"cwd", absWorkDir,
		"elapsed", time.Since(started),
	)
	return out, nil
}

// localFallback returns the locally tracked sessions filtered by the
// agent's work_dir. Used when the ACP server does not support
// session/list or the probe fails for any reason.
func (a *Agent) localFallback() []core.AgentSessionInfo {
	a.mu.RLock()
	workDir := a.workDir
	a.mu.RUnlock()
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		absWorkDir = workDir
	}
	local := a.listLocalSessions(absWorkDir)
	if len(local) == 0 {
		return nil
	}
	slog.Info("acp: using local session fallback", "count", len(local), "cwd", absWorkDir)
	return local
}

// convertSessionList maps ACP session/list entries to core.AgentSessionInfo.
// If `cwdFilter` is non-empty, entries whose cwd does not match are dropped;
// ACP servers SHOULD filter themselves when the request includes cwd, but
// we defend against servers that ignore the hint (see probe_caps.py output
// against devin acp: filter is respected there, but we still double-check).
func convertSessionList(entries []acpSessionListEntry, cwdFilter string) []core.AgentSessionInfo {
	out := make([]core.AgentSessionInfo, 0, len(entries))
	for _, e := range entries {
		if cwdFilter != "" && e.Cwd != "" && !strings.EqualFold(filepath.Clean(e.Cwd), filepath.Clean(cwdFilter)) {
			continue
		}
		info := core.AgentSessionInfo{
			ID:      e.SessionID,
			Summary: strings.TrimSpace(e.Title),
		}
		if t, err := time.Parse(time.RFC3339, e.UpdatedAt); err == nil {
			info.ModifiedAt = t
		} else if t, err := time.Parse(time.RFC3339Nano, e.UpdatedAt); err == nil {
			info.ModifiedAt = t
		}
		out = append(out, info)
	}
	return out
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
