package acp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcpCLIBaseName(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"codebuddy", "codebuddy"},
		{"CodeBuddy", "codebuddy"},
		{"/usr/local/bin/codebuddy", "codebuddy"},
		{"codebuddy.exe", "codebuddy"},
		{"agent", "agent"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := acpCLIBaseName(tc.command); got != tc.want {
			t.Errorf("acpCLIBaseName(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

func TestHiddenDirNameForCommand(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"codebuddy", ".codebuddy"},
		{"CodeBuddy", ".codebuddy"},
		{"/usr/local/bin/codebuddy", ".codebuddy"},
		{"codebuddy.exe", ".codebuddy"},
		{"agent", ""},
		{"openclaw", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := hiddenDirNameForCommand(tc.command); got != tc.want {
			t.Errorf("hiddenDirNameForCommand(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

func TestAgent_SkillDirs_KnownCommand(t *testing.T) {
	workDir := t.TempDir()
	a, err := New(map[string]any{
		"command":  "true",
		"work_dir": workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	agent.command = "codebuddy" // bypass PATH lookup restriction from New

	dirs := agent.SkillDirs()
	if len(dirs) == 0 {
		t.Fatal("expected at least one skill dir for known command \"codebuddy\"")
	}
	want := filepath.Join(workDir, ".codebuddy", "skills")
	if dirs[0] != want {
		t.Errorf("dirs[0] = %q, want %q", dirs[0], want)
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		wantHome := filepath.Join(home, ".codebuddy", "skills")
		found := false
		for _, d := range dirs {
			if d == wantHome {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected home skill dir %q in %v", wantHome, dirs)
		}
	}
}

func TestAgent_SkillDirs_UnknownCommand(t *testing.T) {
	a, err := New(map[string]any{
		"command":  "true",
		"work_dir": t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	agent.command = "some-unknown-cli"

	if dirs := agent.SkillDirs(); dirs != nil {
		t.Errorf("expected nil skill dirs for unknown command, got %v", dirs)
	}
}

func TestAgent_CommandDirs_KnownCommand(t *testing.T) {
	workDir := t.TempDir()
	a, err := New(map[string]any{
		"command":  "true",
		"work_dir": workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	agent.command = "codebuddy"

	dirs := agent.CommandDirs()
	if len(dirs) == 0 {
		t.Fatal("expected at least one command dir for known command \"codebuddy\"")
	}
	want := filepath.Join(workDir, ".codebuddy", "commands")
	if dirs[0] != want {
		t.Errorf("dirs[0] = %q, want %q", dirs[0], want)
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		wantHome := filepath.Join(home, ".codebuddy", "commands")
		found := false
		for _, d := range dirs {
			if d == wantHome {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected home command dir %q in %v", wantHome, dirs)
		}
	}
}

func TestAgent_CommandDirs_UnknownCommand(t *testing.T) {
	a, err := New(map[string]any{
		"command":  "true",
		"work_dir": t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	agent.command = "some-unknown-cli"

	if dirs := agent.CommandDirs(); dirs != nil {
		t.Errorf("expected nil command dirs for unknown command, got %v", dirs)
	}
}
