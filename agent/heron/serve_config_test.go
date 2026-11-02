package heron

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/janostudio/heron-connect/config"
)

// The serve settings reach the adapter through the TOML options map, which
// means a typo or a key that never gets read looks exactly like "serve mode is
// on" until the first message fails. This test walks the real path: TOML file
// → config.Load → New → the fields the adapter actually branches on.
func TestServeOptionsComeFromTOML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[[projects]]
name = "p1"

[projects.agent]
type = "heron"

[[projects.platforms]]
type = "web"

[projects.agent.options]
work_dir = "`+dir+`"
command = "heron"
serve = "http://127.0.0.1:18080"
serve_token = "tok-123"
serve_manage = true
`), 0o600))

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Projects, 1)

	created, err := New(cfg.Projects[0].Agent.Options)
	require.NoError(t, err)
	agent, ok := created.(*Agent)
	require.True(t, ok)

	require.Equal(t, "http://127.0.0.1:18080", agent.serveURL, "serve must come from [projects.agent.options]")
	require.Equal(t, "tok-123", agent.serveToken)
	require.True(t, agent.serveManage, "serve_manage must survive the TOML round-trip")
	// Serve mode must not require the CLI binary: it never starts one.
	require.Empty(t, agent.args, "no --json-rpc args are built in serve mode")
}

// Without the key the adapter must stay on the stdio path, unchanged: an
// operator who never opts in gets exactly the behaviour they had before.
func TestAbsentServeOptionKeepsStdioMode(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[[projects]]
name = "p1"

[projects.agent]
type = "heron"

[[projects.platforms]]
type = "web"

[projects.agent.options]
work_dir = "`+dir+`"
`), 0o600))

	cfg, err := config.Load(configPath)
	require.NoError(t, err)

	// The stdio path looks the binary up in PATH; point it at this test binary
	// so the assertion is about the branch, not about what is installed.
	options := cfg.Projects[0].Agent.Options
	options["command"] = os.Args[0]

	created, err := New(options)
	require.NoError(t, err)
	agent, ok := created.(*Agent)
	require.True(t, ok)

	require.Empty(t, agent.serveURL, "an absent serve key must not switch modes")
	require.Contains(t, agent.args, "--json-rpc", "stdio mode keeps its JSON-RPC arguments")
}

// serve_manage is a bool in TOML. Decoding it as anything else silently
// disables process supervision, and the connector then reports "no engine is
// answering" for a server it was supposed to start.
func TestServeManageRejectsNonBool(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[[projects]]
name = "p1"

[projects.agent]
type = "heron"

[[projects.platforms]]
type = "web"

[projects.agent.options]
work_dir = "`+dir+`"
serve = "http://127.0.0.1:18080"
serve_manage = "yes"
`), 0o600))

	cfg, err := config.Load(configPath)
	require.NoError(t, err)

	created, err := New(cfg.Projects[0].Agent.Options)
	require.NoError(t, err)
	agent, ok := created.(*Agent)
	require.True(t, ok)
	require.False(t, agent.serveManage, "a non-bool serve_manage must not be read as enabled")
}
