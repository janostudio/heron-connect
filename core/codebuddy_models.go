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

// LoadCodeBuddyModelsConfig reads the user-level and project-level
// models.json for workDir and returns them plus whether a project file was
// present. Callers that need the raw config (rather than a finished model
// list) use this so they can layer it over a base list fetched elsewhere —
// e.g. the model list discovered from `codebuddy --acp`.
func LoadCodeBuddyModelsConfig(workDir string) (user, project CodeBuddyModelsFile, projectHasFile bool) {
	userPath, projectPath := CodeBuddyModelsJSONPaths(workDir)
	if userPath != "" {
		user, _ = LoadCodeBuddyModelsFile(userPath)
	}
	if projectPath != "" {
		project, projectHasFile = LoadCodeBuddyModelsFile(projectPath)
	}
	return user, project, projectHasFile
}

// CodeBuddyModelsConfigEmpty reports whether a models.json pair defines
// nothing at all — neither custom model entries nor an allow-list.
//
// Note that a file carrying only `availableModels` is NOT empty: the
// allow-list is meaningful on its own even with no custom `models` defined,
// so callers must not short-circuit on len(Models) alone.
func CodeBuddyModelsConfigEmpty(user, project CodeBuddyModelsFile) bool {
	return len(user.Models) == 0 && len(project.Models) == 0 &&
		len(user.AvailableModels) == 0 && len(project.AvailableModels) == 0
}

// MergeCodeBuddyModels combines user-level and project-level models.json
// content following the documented precedence rules:
//   - models: SmartMerge by id — project-level entries override user-level
//     entries with the same id; entries with distinct ids are appended.
//   - availableModels: project-level fully replaces user-level (no merge)
//     when the project file sets a non-empty list; otherwise the user-level
//     list applies. An empty/absent list at both levels means "show all".
//   - base: when non-empty, the caller-supplied model list (typically the
//     account's real models discovered from the CLI) is used as the
//     starting set, and models.json entries are merged on top by id. When
//     empty, the result is built from models.json entries alone.
//
// The allow-list filters the final list in both cases.
func MergeCodeBuddyModels(user, project CodeBuddyModelsFile, projectHasFile bool, base ...[]ModelOption) []ModelOption {
	order := make([]string, 0, len(user.Models)+len(project.Models))
	byID := make(map[string]ModelOption, len(user.Models)+len(project.Models))

	var seed []ModelOption
	if len(base) > 0 {
		seed = base[0]
	}
	for _, m := range seed {
		if m.Name == "" {
			continue
		}
		if _, exists := byID[m.Name]; !exists {
			order = append(order, m.Name)
		}
		byID[m.Name] = m
	}

	// models.json entries are applied user-then-project so project wins,
	// mirroring the id-based override rule for custom model definitions.
	applyEntries := func(entries []CodeBuddyModelEntry) {
		for _, m := range entries {
			if m.ID == "" {
				continue
			}
			opt := ModelOption{Name: m.ID, Desc: m.Name}
			if prev, exists := byID[m.ID]; exists {
				// Preserve a discovered display name when the override
				// declares only an id, so we don't blank out a good label.
				if m.Name == "" && prev.Desc != "" {
					opt.Desc = prev.Desc
				}
			} else {
				order = append(order, m.ID)
			}
			byID[m.ID] = opt
		}
	}
	applyEntries(user.Models)
	applyEntries(project.Models)

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
		options = append(options, byID[id])
	}
	return options
}

// CodeBuddyConfiguredModels reads user-level and project-level
// models.json for workDir and returns the merged, filtered model list
// built from those files alone. Returns nil if neither file defines
// anything (models or allow-list) — callers should fall back to a
// discovered list or a built-in default in that case.
//
// Shared by agent/codebuddy (type = "codebuddy") and agent/acp (type =
// "acp" with command = "codebuddy") since both ultimately drive the same
// `codebuddy` CLI binary and its config file.
func CodeBuddyConfiguredModels(workDir string) []ModelOption {
	user, project, projectHasFile := LoadCodeBuddyModelsConfig(workDir)
	if CodeBuddyModelsConfigEmpty(user, project) {
		return nil
	}
	return MergeCodeBuddyModels(user, project, projectHasFile)
}
