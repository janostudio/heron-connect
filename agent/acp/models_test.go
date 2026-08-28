package acp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

func TestAgent_AvailableModels_FallsBackToCodeBuddyModelsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	modelsPath := filepath.Join(workDir, ".codebuddy", "models.json")
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"models": [{"id": "my-custom-model", "name": "My Custom Model"}]}`
	if err := os.WriteFile(modelsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := New(map[string]any{
		"command":  "true",
		"work_dir": workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	agent.command = "codebuddy" // bypass PATH lookup restriction from New

	// No ACP handshake has happened, so modelsCache is empty — this must
	// fall back to reading .codebuddy/models.json rather than returning nil.
	models := agent.AvailableModels(context.Background())
	if len(models) != 1 || models[0].Name != "my-custom-model" {
		t.Errorf("expected fallback to models.json, got %v", models)
	}
}

func TestAgent_AvailableModels_PrefersACPReportedModelsOverModelsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	modelsPath := filepath.Join(workDir, ".codebuddy", "models.json")
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"models": [{"id": "from-file", "name": "From File"}]}`
	if err := os.WriteFile(modelsPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := New(map[string]any{
		"command":  "true",
		"work_dir": workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	agent.command = "codebuddy"
	agent.reportModels("from-acp", []core.ModelOption{{Name: "from-acp", Desc: "From ACP"}})

	models := agent.AvailableModels(context.Background())
	if len(models) != 1 || models[0].Name != "from-acp" {
		t.Errorf("expected ACP-reported models to win over models.json fallback, got %v", models)
	}
}

func TestAgent_AvailableModels_NonCodeBuddyCommandDoesNotFallBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	a, err := New(map[string]any{
		"command":  "true",
		"work_dir": workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	agent.command = "some-other-cli"

	if models := agent.AvailableModels(context.Background()); len(models) != 0 {
		t.Errorf("expected no models for unrecognized command with empty ACP cache, got %v", models)
	}
}
