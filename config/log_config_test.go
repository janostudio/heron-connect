package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestLogConfig_Parse(t *testing.T) {
	data := `
[log]
level = "debug"
file = "/tmp/app.log"
max_size_mb = 20
retention_days = 30
`
	var cfg Config
	if err := toml.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("Level = %q, want debug", cfg.Log.Level)
	}
	if cfg.Log.File != "/tmp/app.log" {
		t.Errorf("File = %q, want /tmp/app.log", cfg.Log.File)
	}
	if cfg.Log.MaxSizeMB != 20 {
		t.Errorf("MaxSizeMB = %d, want 20", cfg.Log.MaxSizeMB)
	}
	if cfg.Log.RetentionDays != 30 {
		t.Errorf("RetentionDays = %d, want 30", cfg.Log.RetentionDays)
	}
}

func TestLogConfig_DefaultsEmpty(t *testing.T) {
	var cfg Config
	if err := toml.Unmarshal([]byte("[log]\nlevel = \"info\"\n"), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Log.File != "" {
		t.Errorf("File = %q, want empty", cfg.Log.File)
	}
	if cfg.Log.MaxSizeMB != 0 {
		t.Errorf("MaxSizeMB = %d, want 0", cfg.Log.MaxSizeMB)
	}
	if cfg.Log.RetentionDays != 0 {
		t.Errorf("RetentionDays = %d, want 0", cfg.Log.RetentionDays)
	}
}
