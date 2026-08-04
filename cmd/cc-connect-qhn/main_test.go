package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/config"
	"github.com/chenhg5/cc-connect/core"
)

type stubMainAgent struct {
	workDir string
}

func (a *stubMainAgent) Name() string { return "stub-main" }

func (a *stubMainAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	return &stubMainAgentSession{}, nil
}

func (a *stubMainAgent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}

func (a *stubMainAgent) Stop() error { return nil }

func (a *stubMainAgent) SetWorkDir(dir string) {
	a.workDir = dir
}

func (a *stubMainAgent) GetWorkDir() string {
	return a.workDir
}

type stubMainAgentSession struct{}

func (s *stubMainAgentSession) Send(string, []core.ImageAttachment, []core.FileAttachment) error {
	return nil
}
func (s *stubMainAgentSession) RespondPermission(string, core.PermissionResult) error { return nil }
func (s *stubMainAgentSession) Events() <-chan core.Event                             { return nil }
func (s *stubMainAgentSession) Close() error                                          { return nil }
func (s *stubMainAgentSession) CancelTurn()                                           {}
func (s *stubMainAgentSession) CurrentSessionID() string                              { return "" }
func (s *stubMainAgentSession) Alive() bool                                           { return true }

// stubMainPlatform is a minimal Platform implementation for testing.
type stubMainPlatform struct {
	name string
}

func (p *stubMainPlatform) Name() string                                   { return p.name }
func (p *stubMainPlatform) Start(_ core.MessageHandler) error              { return nil }
func (p *stubMainPlatform) Reply(_ context.Context, _ any, _ string) error { return nil }
func (p *stubMainPlatform) Send(_ context.Context, _ any, _ string) error  { return nil }
func (p *stubMainPlatform) Stop() error                                    { return nil }

// stubAgentFactory creates a stub agent with a given name for testing.
func stubAgentFactory(name string) core.AgentFactory {
	return func(opts map[string]any) (core.Agent, error) {
		wd, _ := opts["work_dir"].(string)
		return &stubMainAgent{workDir: wd}, nil
	}
}

// stubPlatformFactory creates a stub platform with a given name for testing.
func stubPlatformFactory(name string) core.PlatformFactory {
	return func(opts map[string]any) (core.Platform, error) {
		return &stubMainPlatform{name: name}, nil
	}
}

func init() {
	// Register test agent and platform factories for multi-project tests.
	// These use unique names to avoid conflicting with real registrations.
	core.RegisterAgent("test-claude", stubAgentFactory("test-claude"))
	core.RegisterAgent("test-codex", stubAgentFactory("test-codex"))
	core.RegisterPlatform("test-feishu", stubPlatformFactory("test-feishu"))
	core.RegisterPlatform("test-telegram", stubPlatformFactory("test-telegram"))
}

func TestProjectStatePath(t *testing.T) {
	dataDir := t.TempDir()
	got := projectStatePath(dataDir, "my/project:one")
	want := filepath.Join(dataDir, "projects", "my_project_one.state.json")
	if got != want {
		t.Fatalf("projectStatePath() = %q, want %q", got, want)
	}
}

func TestResolveResetOnIdle(t *testing.T) {
	intPtr := func(v int) *int { return &v }

	cases := []struct {
		name          string
		configured    *int
		wantDuration  time.Duration
		wantDefaulted bool
	}{
		{
			name:          "unset applies default and reports defaulted",
			configured:    nil,
			wantDuration:  time.Duration(defaultResetOnIdleMins) * time.Minute,
			wantDefaulted: true,
		},
		{
			name:          "explicit zero opts out and is not defaulted",
			configured:    intPtr(0),
			wantDuration:  0,
			wantDefaulted: false,
		},
		{
			name:          "explicit positive value is honored",
			configured:    intPtr(45),
			wantDuration:  45 * time.Minute,
			wantDefaulted: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDuration, gotDefaulted := resolveResetOnIdle(tc.configured)
			if gotDuration != tc.wantDuration {
				t.Errorf("duration = %v, want %v", gotDuration, tc.wantDuration)
			}
			if gotDefaulted != tc.wantDefaulted {
				t.Errorf("defaulted = %v, want %v", gotDefaulted, tc.wantDefaulted)
			}
		})
	}
}

func TestApplyProjectStateOverride(t *testing.T) {
	baseDir := t.TempDir()
	overrideDir := filepath.Join(t.TempDir(), "override")
	if err := os.Mkdir(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir override dir: %v", err)
	}

	store := core.NewProjectStateStore(filepath.Join(t.TempDir(), "projects", "demo.state.json"))
	store.SetWorkDirOverride(overrideDir)

	agent := &stubMainAgent{workDir: baseDir}
	got := applyProjectStateOverride("demo", agent, baseDir, store)

	if got != overrideDir {
		t.Fatalf("applyProjectStateOverride() = %q, want %q", got, overrideDir)
	}
	if agent.workDir != overrideDir {
		t.Fatalf("agent workDir = %q, want %q", agent.workDir, overrideDir)
	}
}

type stubProviderRefreshAgent struct {
	stubMainAgent
	providers  []core.ProviderConfig
	activeName string
	calls      []string
	activateOK bool
}

func (a *stubProviderRefreshAgent) SetProviders(providers []core.ProviderConfig) {
	a.providers = append([]core.ProviderConfig(nil), providers...)
	a.calls = append(a.calls, "set_providers")
}

func (a *stubProviderRefreshAgent) SetActiveProvider(name string) bool {
	if !a.activateOK {
		a.calls = append(a.calls, "set_active_provider_failed")
		return false
	}
	a.activeName = name
	a.calls = append(a.calls, "set_active_provider")
	return true
}

func (a *stubProviderRefreshAgent) GetActiveProvider() *core.ProviderConfig {
	for i := range a.providers {
		if a.providers[i].Name == a.activeName {
			return &a.providers[i]
		}
	}
	return nil
}

func (a *stubProviderRefreshAgent) ListProviders() []core.ProviderConfig {
	providers := make([]core.ProviderConfig, len(a.providers))
	copy(providers, a.providers)
	return providers
}

func (a *stubProviderRefreshAgent) StartInitialModelRefresh() {
	a.calls = append(a.calls, "start_initial_model_refresh")
}

func TestBuildAgentOptionsInjectsProjectScope(t *testing.T) {
	proj := config.ProjectConfig{
		Name: "demo-project",
		Agent: config.AgentConfig{
			Options: map[string]any{
				"work_dir": "/tmp/work",
				"model":    "gpt-test",
			},
		},
	}

	got := buildAgentOptions("/tmp/data", proj)

	if got["cc_data_dir"] != "/tmp/data" {
		t.Fatalf("cc_data_dir = %v, want %q", got["cc_data_dir"], "/tmp/data")
	}
	if got["cc_project"] != "demo-project" {
		t.Fatalf("cc_project = %v, want %q", got["cc_project"], "demo-project")
	}
	if got["work_dir"] != "/tmp/work" || got["model"] != "gpt-test" {
		t.Fatalf("buildAgentOptions() lost existing options: %v", got)
	}
	if _, exists := proj.Agent.Options["cc_data_dir"]; exists {
		t.Fatalf("project agent options mutated: %v", proj.Agent.Options)
	}
}

func TestWireAgentProvidersStartsRefreshAfterProviderWiring(t *testing.T) {
	agent := &stubProviderRefreshAgent{activateOK: true}
	proj := config.ProjectConfig{
		Agent: config.AgentConfig{
			Options: map[string]any{"provider": "provider-b"},
			Providers: []config.ProviderConfig{
				{Name: "provider-a", APIKey: "key-a"},
				{Name: "provider-b", APIKey: "key-b", Model: "model-b"},
			},
		},
	}

	result := wireAgentProviders(agent, proj.Agent)
	startInitialRefreshIfReady(agent, result)

	wantCalls := []string{"set_providers", "set_active_provider", "start_initial_model_refresh"}
	if !reflect.DeepEqual(agent.calls, wantCalls) {
		t.Fatalf("call order = %v, want %v", agent.calls, wantCalls)
	}
	if len(agent.providers) != 2 {
		t.Fatalf("provider count = %d, want 2", len(agent.providers))
	}
	if agent.activeName != "provider-b" {
		t.Fatalf("active provider = %q, want %q", agent.activeName, "provider-b")
	}
}

func TestWireAgentProviders_SkipsRefreshWhenExplicitProviderActivationFails(t *testing.T) {
	agent := &stubProviderRefreshAgent{}
	agent.activateOK = false
	agent.workDir = "/tmp/original"
	proj := config.ProjectConfig{
		Agent: config.AgentConfig{
			Options:   map[string]any{"provider": "missing-provider"},
			Providers: []config.ProviderConfig{{Name: "provider-a", APIKey: "key-a"}},
		},
	}

	result := wireAgentProviders(agent, proj.Agent)

	if result.canStartInitialRefresh {
		t.Fatal("canStartInitialRefresh = true, want false")
	}
	if !result.explicitProviderRequested {
		t.Fatal("explicitProviderRequested = false, want true")
	}
	if result.activeProviderApplied {
		t.Fatal("activeProviderApplied = true, want false")
	}
	wantCalls := []string{"set_providers", "set_active_provider_failed"}
	if !reflect.DeepEqual(agent.calls, wantCalls) {
		t.Fatalf("call order = %v, want %v", agent.calls, wantCalls)
	}
}

func TestWireAgentProviders_AllowsRefreshWithoutProviders(t *testing.T) {
	agent := &stubProviderRefreshAgent{stubMainAgent: stubMainAgent{workDir: "/tmp/original"}}
	proj := config.ProjectConfig{Agent: config.AgentConfig{Options: map[string]any{}}}

	result := wireAgentProviders(agent, proj.Agent)

	if !result.canStartInitialRefresh {
		t.Fatal("canStartInitialRefresh = false, want true")
	}
	if result.explicitProviderRequested {
		t.Fatal("explicitProviderRequested = true, want false")
	}
	if result.activeProviderApplied {
		t.Fatal("activeProviderApplied = true, want false")
	}
	if len(agent.calls) != 0 {
		t.Fatalf("calls = %v, want no provider wiring calls", agent.calls)
	}
}

func TestStartInitialRefresh_AfterProjectStateOverride(t *testing.T) {
	agent := &stubProviderRefreshAgent{activateOK: true, stubMainAgent: stubMainAgent{workDir: "/tmp/original"}}
	overrideDir := filepath.Join(t.TempDir(), "override")
	if err := os.Mkdir(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir override dir: %v", err)
	}
	store := core.NewProjectStateStore(filepath.Join(t.TempDir(), "projects", "demo.state.json"))
	store.SetWorkDirOverride(overrideDir)
	proj := config.ProjectConfig{
		Name: "demo",
		Agent: config.AgentConfig{
			Options:   map[string]any{"provider": "provider-b", "work_dir": "/tmp/original"},
			Providers: []config.ProviderConfig{{Name: "provider-a"}, {Name: "provider-b"}},
		},
	}

	result := wireAgentProviders(agent, proj.Agent)
	applyProjectStateOverride(proj.Name, agent, "/tmp/original", store)
	startInitialRefreshIfReady(agent, result)

	wantCalls := []string{"set_providers", "set_active_provider", "start_initial_model_refresh"}
	if !reflect.DeepEqual(agent.calls, wantCalls) {
		t.Fatalf("call order = %v, want %v", agent.calls, wantCalls)
	}
	if agent.workDir != overrideDir {
		t.Fatalf("agent workDir at refresh = %q, want %q", agent.workDir, overrideDir)
	}
}

func TestCreateProjectEngines_MultiProject(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "config.toml")
	configContent := `
data_dir = "` + strings.ReplaceAll(dataDir, `\`, `\\`) + `"

[[projects]]
name = "backend"
[projects.agent]
type = "test-claude"
[projects.agent.options]
work_dir = "` + strings.ReplaceAll(dataDir, `\`, `\\`) + `/backend"

[[projects.platforms]]
type = "test-feishu"

[[projects]]
name = "frontend"
[projects.agent]
type = "test-codex"
[projects.agent.options]
work_dir = "` + strings.ReplaceAll(dataDir, `\`, `\\`) + `/frontend"

[[projects.platforms]]
type = "test-telegram"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	observeFlag := false
	observeChannel := ""
	engines, workDirs := createProjectEngines(cfg, configPath, &observeFlag, &observeChannel)

	if len(engines) != 2 {
		t.Fatalf("engines count = %d, want 2", len(engines))
	}
	if len(workDirs) != 2 {
		t.Fatalf("workDirs count = %d, want 2", len(workDirs))
	}

	// Verify project names
	if engines[0].Name() != "backend" {
		t.Errorf("engine[0] name = %q, want backend", engines[0].Name())
	}
	if engines[1].Name() != "frontend" {
		t.Errorf("engine[1] name = %q, want frontend", engines[1].Name())
	}
}

func TestCreateProjectEngines_SingleProject(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "config.toml")
	configContent := `
data_dir = "` + strings.ReplaceAll(dataDir, `\`, `\\`) + `"

[[projects]]
name = "solo"
[projects.agent]
type = "test-claude"
[projects.agent.options]
work_dir = "` + strings.ReplaceAll(dataDir, `\`, `\\`) + `/solo"

[[projects.platforms]]
type = "test-feishu"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	observeFlag := false
	observeChannel := ""
	engines, workDirs := createProjectEngines(cfg, configPath, &observeFlag, &observeChannel)

	if len(engines) != 1 {
		t.Fatalf("engines count = %d, want 1", len(engines))
	}
	if len(workDirs) != 1 {
		t.Fatalf("workDirs count = %d, want 1", len(workDirs))
	}
	if engines[0].Name() != "solo" {
		t.Errorf("engine name = %q, want solo", engines[0].Name())
	}
}
