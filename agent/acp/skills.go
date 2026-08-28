package acp

import (
	"os"
	"path/filepath"
	"strings"
)

// acpCLIBaseName normalizes an ACP agent's configured command (a path or
// bare executable name, optionally with a Windows ".exe" suffix) to a
// lowercase base name suitable for switching on well-known CLIs (e.g.
// "codebuddy"). Shared by skill-directory and model-config inference,
// both of which need to recognize "this ACP agent is actually launching
// CodeBuddy Code" from nothing but the configured command string.
func acpCLIBaseName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	base := strings.ToLower(filepath.Base(command))
	return strings.TrimSuffix(base, ".exe")
}

// ── SkillProvider implementation ──────────────────────────────

// SkillDirs implements core.SkillProvider by inferring the skill directory
// convention from the underlying CLI's binary name (a.command). The ACP
// agent type is a generic adapter that can launch any ACP-compatible CLI
// (Cursor Agent, OpenClaw, Trae, Copilot, CodeBuddy Code via `codebuddy
// --acp`, etc.), so unlike the dedicated agent types we cannot assume a
// single fixed skill directory convention — we match on the CLI's own
// binary name instead.
//
// Only CLIs with a known, documented skill directory convention are
// mapped here. Unknown commands return nil (no skills scanned) rather
// than guessing at a convention that may not exist — a wrong guess would
// silently scan nothing anyway (missing directories are ignored), but an
// explicit nil keeps this function's behavior predictable and easy to
// extend as more CLIs are added.
func (a *Agent) SkillDirs() []string {
	return hiddenConfigDirsForCommand(a.command, a.workDir, "skills")
}

// ── CommandProvider implementation ────────────────────────────

// CommandDirs implements core.CommandProvider using the same CLI-name
// inference as SkillDirs — e.g. `codebuddy --acp` reads custom commands
// from the same ".codebuddy/commands" convention as the dedicated
// codebuddy agent type.
func (a *Agent) CommandDirs() []string {
	return hiddenConfigDirsForCommand(a.command, a.workDir, "commands")
}

// hiddenDirNameForCommand maps a known ACP-launched CLI's binary name to
// the hidden directory it stores config/skills/commands under (e.g.
// "codebuddy" -> ".codebuddy").
func hiddenDirNameForCommand(command string) string {
	switch acpCLIBaseName(command) {
	case "codebuddy":
		// Matches agent/codebuddy/codebuddy.go's Skill/CommandDirs convention —
		// this is the case where `codebuddy --acp` is launched through the
		// generic ACP adapter instead of the dedicated codebuddy agent type.
		return ".codebuddy"
	default:
		return ""
	}
}

// hiddenConfigDirsForCommand resolves the project-level and user-level
// paths for a given subdirectory (e.g. "skills" or "commands") under the
// hidden config directory inferred from command. Returns nil if command
// isn't a recognized CLI.
func hiddenConfigDirsForCommand(command, workDir, subdir string) []string {
	dirName := hiddenDirNameForCommand(command)
	if dirName == "" {
		return nil
	}

	absDir, err := filepath.Abs(workDir)
	if err != nil {
		absDir = workDir
	}

	dirs := []string{filepath.Join(absDir, dirName, subdir)}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, dirName, subdir))
	}
	return dirs
}
