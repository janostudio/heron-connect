package core

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCodeBuddyModelsJSON(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodeBuddyConfiguredModels_NoFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	if got := CodeBuddyConfiguredModels(workDir); got != nil {
		t.Errorf("expected nil with no models.json files, got %v", got)
	}
}

func TestCodeBuddyConfiguredModels_UserLevelOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	writeCodeBuddyModelsJSON(t, filepath.Join(home, ".codebuddy", "models.json"), `{
		"models": [
			{"id": "my-custom-model", "name": "My Custom Model"}
		]
	}`)

	got := CodeBuddyConfiguredModels(workDir)
	want := ModelOption{Name: "my-custom-model", Desc: "My Custom Model"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCodeBuddyConfiguredModels_ProjectOverridesUserByID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	writeCodeBuddyModelsJSON(t, filepath.Join(home, ".codebuddy", "models.json"), `{
		"models": [
			{"id": "shared-id", "name": "User Version"},
			{"id": "user-only", "name": "User Only"}
		]
	}`)
	writeCodeBuddyModelsJSON(t, filepath.Join(workDir, ".codebuddy", "models.json"), `{
		"models": [
			{"id": "shared-id", "name": "Project Version"},
			{"id": "project-only", "name": "Project Only"}
		]
	}`)

	got := CodeBuddyConfiguredModels(workDir)
	byID := make(map[string]string, len(got))
	for _, m := range got {
		byID[m.Name] = m.Desc
	}

	if byID["shared-id"] != "Project Version" {
		t.Errorf("expected project-level entry to win for shared id, got %q", byID["shared-id"])
	}
	if byID["user-only"] != "User Only" {
		t.Errorf("expected user-only entry preserved, got %q", byID["user-only"])
	}
	if byID["project-only"] != "Project Only" {
		t.Errorf("expected project-only entry appended, got %q", byID["project-only"])
	}
	if len(got) != 3 {
		t.Errorf("expected 3 merged models, got %d: %v", len(got), got)
	}
}

func TestCodeBuddyConfiguredModels_ProjectAvailableModelsFullyOverridesUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	writeCodeBuddyModelsJSON(t, filepath.Join(home, ".codebuddy", "models.json"), `{
		"models": [
			{"id": "a", "name": "A"},
			{"id": "b", "name": "B"}
		],
		"availableModels": ["a", "b"]
	}`)
	writeCodeBuddyModelsJSON(t, filepath.Join(workDir, ".codebuddy", "models.json"), `{
		"models": [
			{"id": "c", "name": "C"}
		],
		"availableModels": ["c"]
	}`)

	got := CodeBuddyConfiguredModels(workDir)
	if len(got) != 1 || got[0].Name != "c" {
		t.Errorf("expected only project's availableModels allow-list to apply, got %v", got)
	}
}

func TestCodeBuddyConfiguredModels_MalformedFileIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	writeCodeBuddyModelsJSON(t, filepath.Join(home, ".codebuddy", "models.json"), `{not valid json`)

	if got := CodeBuddyConfiguredModels(workDir); got != nil {
		t.Errorf("expected nil for malformed models.json (fail open), got %v", got)
	}
}
