package core

import (
	"os"
	"path/filepath"
	"reflect"
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

// An allow-list is meaningful on its own: a models.json that defines only
// availableModels (no custom models) must still take effect. Previously
// this returned nil because the code only checked len(Models).
func TestCodeBuddyConfiguredModels_AllowListWithoutModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	writeCodeBuddyModelsJSON(t, filepath.Join(home, ".codebuddy", "models.json"), `{
		"availableModels": ["a", "b"]
	}`)

	// With no base list there is nothing for the allow-list to filter, so the
	// result is empty-but-non-nil: enough to signal "config exists" and stop
	// the caller falling through to a built-in list.
	got := CodeBuddyConfiguredModels(workDir)
	if got == nil {
		t.Fatal("expected non-nil result for an allow-list-only models.json")
	}
	if len(got) != 0 {
		t.Errorf("expected empty result with no base list, got %v", got)
	}
	if CodeBuddyModelsConfigEmpty(CodeBuddyModelsFile{}, CodeBuddyModelsFile{AvailableModels: []string{"a"}}) {
		t.Error("CodeBuddyModelsConfigEmpty must treat an allow-list as non-empty config")
	}
}

func TestCodeBuddyModelsConfigEmpty(t *testing.T) {
	tests := []struct {
		name    string
		user    CodeBuddyModelsFile
		project CodeBuddyModelsFile
		want    bool
	}{
		{"both empty", CodeBuddyModelsFile{}, CodeBuddyModelsFile{}, true},
		{"user models", CodeBuddyModelsFile{Models: []CodeBuddyModelEntry{{ID: "a"}}}, CodeBuddyModelsFile{}, false},
		{"project models", CodeBuddyModelsFile{}, CodeBuddyModelsFile{Models: []CodeBuddyModelEntry{{ID: "a"}}}, false},
		{"user allow-list only", CodeBuddyModelsFile{AvailableModels: []string{"a"}}, CodeBuddyModelsFile{}, false},
		{"project allow-list only", CodeBuddyModelsFile{}, CodeBuddyModelsFile{AvailableModels: []string{"a"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeBuddyModelsConfigEmpty(tt.user, tt.project); got != tt.want {
				t.Errorf("CodeBuddyModelsConfigEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

// With a base list (the probe result), models.json customises on top rather
// than replacing it wholesale.
func TestMergeCodeBuddyModels_BaseListWithCustomOverrides(t *testing.T) {
	base := []ModelOption{
		{Name: "a", Desc: "A"},
		{Name: "b", Desc: "B"},
	}
	user := CodeBuddyModelsFile{Models: []CodeBuddyModelEntry{
		{ID: "b", Name: "B Custom"},
		{ID: "c", Name: "C"},
	}}

	got := MergeCodeBuddyModels(user, CodeBuddyModelsFile{}, false, base)

	want := []ModelOption{
		{Name: "a", Desc: "A"},
		{Name: "b", Desc: "B Custom"},
		{Name: "c", Desc: "C"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// An override that declares only an id must keep the discovered display
// name rather than blanking it out.
func TestMergeCodeBuddyModels_OverrideWithoutNameKeepsDiscoveredDesc(t *testing.T) {
	base := []ModelOption{{Name: "a", Desc: "Discovered A"}}
	user := CodeBuddyModelsFile{Models: []CodeBuddyModelEntry{{ID: "a"}}}

	got := MergeCodeBuddyModels(user, CodeBuddyModelsFile{}, false, base)

	if len(got) != 1 || got[0].Desc != "Discovered A" {
		t.Errorf("got %+v, want the discovered description preserved", got)
	}
}

// The allow-list filters a base list too.
func TestMergeCodeBuddyModels_AllowListFiltersBase(t *testing.T) {
	base := []ModelOption{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	user := CodeBuddyModelsFile{AvailableModels: []string{"a", "c"}}

	got := MergeCodeBuddyModels(user, CodeBuddyModelsFile{}, false, base)

	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Errorf("got %+v, want only a and c", got)
	}
}

// No base list preserves the original models.json-only behaviour.
func TestMergeCodeBuddyModels_NoBase(t *testing.T) {
	user := CodeBuddyModelsFile{Models: []CodeBuddyModelEntry{{ID: "x", Name: "X"}}}

	got := MergeCodeBuddyModels(user, CodeBuddyModelsFile{}, false)
	if len(got) != 1 || got[0] != (ModelOption{Name: "x", Desc: "X"}) {
		t.Errorf("got %+v, want [{x X}]", got)
	}
}

func TestLoadCodeBuddyModelsConfig_ReportsProjectFilePresence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := t.TempDir()

	_, _, hasFile := LoadCodeBuddyModelsConfig(workDir)
	if hasFile {
		t.Error("expected projectHasFile=false with no project models.json")
	}

	writeCodeBuddyModelsJSON(t, filepath.Join(workDir, ".codebuddy", "models.json"), `{"models":[]}`)
	_, _, hasFile = LoadCodeBuddyModelsConfig(workDir)
	if !hasFile {
		t.Error("expected projectHasFile=true once the file exists")
	}
}
