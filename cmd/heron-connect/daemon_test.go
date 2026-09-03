package main

import (
	"path/filepath"
	"testing"
)

func TestParseDaemonInstallArgs_ConfigSetsWorkDir(t *testing.T) {
	cfg, force, _, err := parseDaemonInstallArgs([]string{"--config", "/tmp/example/config.toml"})
	if err != nil {
		t.Fatalf("parseDaemonInstallArgs returned error: %v", err)
	}
	if force {
		t.Fatalf("force = true, want false")
	}

	want := filepath.Clean("/tmp/example")
	if cfg.WorkDir != want {
		t.Fatalf("cfg.WorkDir = %q, want %q", cfg.WorkDir, want)
	}
}

func TestParseDaemonInstallArgs_ConfigEqualsFormSetsWorkDir(t *testing.T) {
	cfg, _, _, err := parseDaemonInstallArgs([]string{"--config=/tmp/example/config.toml"})
	if err != nil {
		t.Fatalf("parseDaemonInstallArgs returned error: %v", err)
	}

	want := filepath.Clean("/tmp/example")
	if cfg.WorkDir != want {
		t.Fatalf("cfg.WorkDir = %q, want %q", cfg.WorkDir, want)
	}
}

func TestParseDaemonInstallArgs_WorkDirOverridesConfig(t *testing.T) {
	cfg, force, _, err := parseDaemonInstallArgs([]string{
		"--config", "/tmp/example/config.toml",
		"--work-dir", "/tmp/override",
		"--force",
	})
	if err != nil {
		t.Fatalf("parseDaemonInstallArgs returned error: %v", err)
	}
	if !force {
		t.Fatalf("force = false, want true")
	}

	want := filepath.Clean("/tmp/override")
	if cfg.WorkDir != want {
		t.Fatalf("cfg.WorkDir = %q, want %q", cfg.WorkDir, want)
	}
}

func TestParseDaemonInstallArgs_LogRetentionDays(t *testing.T) {
	cfg, _, flags, err := parseDaemonInstallArgs([]string{
		"--config", "/tmp/example/config.toml",
		"--log-retention-days", "30",
	})
	if err != nil {
		t.Fatalf("parseDaemonInstallArgs returned error: %v", err)
	}
	if cfg.LogRetentionDays != 30 {
		t.Fatalf("cfg.LogRetentionDays = %d, want 30", cfg.LogRetentionDays)
	}
	if !flags.logRetentionDays {
		t.Fatalf("flags.logRetentionDays = false, want true")
	}
}
