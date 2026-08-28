package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// CodeBuddyModelsFile mirrors the structure CodeBuddy Code CLI's `models.json`
// documents (see docs/cli/models.md): a "models" array of custom/overriding
// model definitions plus an optional "availableModels" allow-list.
//
// heron-connect only needs enough of this schema to surface model IDs +
// display names for the web admin UI and the /model command — the
// credential/endpoint fields (apiKey, url, etc.) are the CLI's own concern
// and are intentionally not surfaced back to heron-connect callers.
//
// This lives in core (rather than agent/codebuddy) because both the
// dedicated `type = "codebuddy"` agent and the generic `type = "acp"`
// adapter (when launching `codebuddy --acp`) need to read the same file —
// it is the underlying CLI's own config, not tied to either Go package.
type CodeBuddyModelsFile struct {
	Models          []CodeBuddyModelEntry `json:"models"`
	AvailableModels []string              `json:"availableModels"`
}

// CodeBuddyModelEntry is a single model definition inside a models.json
// "models" array.
type CodeBuddyModelEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LoadCodeBuddyModelsFile reads and parses a single models.json file.
// A missing file is not an error — it returns a zero-value result so
// callers can treat "no user/project override" uniformly with "empty
// override". A malformed file is logged by the caller's context and
// also treated as absent (fail open, since these files can be
// hand-edited and CodeBuddy Code itself tolerates errors by ignoring
// the bad file).
func LoadCodeBuddyModelsFile(path string) (CodeBuddyModelsFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CodeBuddyModelsFile{}, false
	}
	var f CodeBuddyModelsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return CodeBuddyModelsFile{}, false
	}
	return f, true
}

// CodeBuddyModelsJSONPaths returns the (user, project) models.json paths
// for the given work directory, matching CodeBuddy Code's own resolution:
// user-level at ~/.codebuddy/models.json, project-level at
// <workDir>/.codebuddy/models.json.
func CodeBuddyModelsJSONPaths(workDir string) (userPath, projectPath string) {
	if home, err := os.UserHomeDir(); err == nil {
		userPath = filepath.Join(home, ".codebuddy", "models.json")
	}
	absDir, err := filepath.Abs(workDir)
	if err != nil {
		absDir = workDir
	}
	projectPath = filepath.Join(absDir, ".codebuddy", "models.json")
	return userPath, projectPath
}

// MergeCodeBuddyModels combines user-level and project-level models.json
// content following the documented precedence rules:
//   - models: SmartMerge by id — project-level entries override user-level
//     entries with the same id; entries with distinct ids are appended.
//   - availableModels: project-level fully replaces user-level (no merge)
//     when the project file sets a non-empty list; otherwise the user-level
//     list applies. An empty/absent list at both levels means "show all".
func MergeCodeBuddyModels(user, project CodeBuddyModelsFile, projectHasFile bool) []ModelOption {
	order := make([]string, 0, len(user.Models)+len(project.Models))
	byID := make(map[string]CodeBuddyModelEntry, len(user.Models)+len(project.Models))

	for _, m := range user.Models {
		if m.ID == "" {
			continue
		}
		if _, exists := byID[m.ID]; !exists {
			order = append(order, m.ID)
		}
		byID[m.ID] = m
	}
	for _, m := range project.Models {
		if m.ID == "" {
			continue
		}
		if _, exists := byID[m.ID]; !exists {
			order = append(order, m.ID)
		}
		byID[m.ID] = m
	}

	// availableModels: project-level presence (even if empty in the file,
	// we can't distinguish "absent" from "explicitly empty" once decoded —
	// treat non-empty project list as a full override per the docs' "project
	// completely overrides, no merge" rule; fall back to user-level otherwise.
	var allow []string
	if projectHasFile && len(project.AvailableModels) > 0 {
		allow = project.AvailableModels
	} else if len(user.AvailableModels) > 0 {
		allow = user.AvailableModels
	}

	var allowSet map[string]struct{}
	if len(allow) > 0 {
		allowSet = make(map[string]struct{}, len(allow))
		for _, id := range allow {
			allowSet[id] = struct{}{}
		}
	}

	options := make([]ModelOption, 0, len(order))
	for _, id := range order {
		if allowSet != nil {
			if _, ok := allowSet[id]; !ok {
				continue
			}
		}
		entry := byID[id]
		options = append(options, ModelOption{Name: entry.ID, Desc: entry.Name})
	}
	return options
}

// CodeBuddyConfiguredModels reads user-level and project-level
// models.json for workDir and returns the merged, filtered model list.
// Returns nil if neither file exists or neither defines any models —
// callers should fall back to a built-in default list in that case.
//
// Shared by agent/codebuddy (type = "codebuddy") and agent/acp (type =
// "acp" with command = "codebuddy") since both ultimately drive the same
// `codebuddy` CLI binary and its config file.
func CodeBuddyConfiguredModels(workDir string) []ModelOption {
	userPath, projectPath := CodeBuddyModelsJSONPaths(workDir)

	var user, project CodeBuddyModelsFile
	if userPath != "" {
		user, _ = LoadCodeBuddyModelsFile(userPath)
	}
	projectHasFile := false
	if projectPath != "" {
		project, projectHasFile = LoadCodeBuddyModelsFile(projectPath)
	}

	if len(user.Models) == 0 && len(project.Models) == 0 {
		return nil
	}
	return MergeCodeBuddyModels(user, project, projectHasFile)
}
