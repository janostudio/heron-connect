package heron

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// ensureServeProcess makes sure a `heron-ai --serve` answers at the configured
// URL, starting one when serve_manage is on.
//
// The child is started with --exit-with-parent so it disappears on its own if
// this connector is killed without a chance to clean up: a SIGKILL leaves no
// code to run here, and an orphaned engine would keep the port and the timers
// that belong to a conversation nobody can reach any more.
func (a *Agent) ensureServeProcess(ctx context.Context) error {
	a.mu.RLock()
	serveURL := a.serveURL
	manage := a.serveManage
	command := a.command
	workDir := a.workDir
	args := append([]string(nil), a.args...)
	extraEnv := append([]string(nil), a.extraEnv...)
	existing := a.serverCmd
	a.mu.RUnlock()

	if probeHealth(ctx, serveURL) {
		return nil
	}
	if !manage {
		return fmt.Errorf("heron: no engine is answering at %s; start one with `heron-ai --serve` or set serve_manage = true", serveURL)
	}
	if existing != nil {
		return fmt.Errorf("heron: engine at %s stopped answering and is already being restarted", serveURL)
	}

	port := portFromURL(serveURL)
	commandArgs := []string{"--serve", "--exit-with-parent"}
	if port != "" {
		commandArgs = append(commandArgs, "--port", port)
	}
	commandArgs = append(commandArgs, args...)

	cmd := exec.Command(command, commandArgs...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Its own process group: a Ctrl-C in the terminal this connector runs in
	// must reach the connector, which then stops the engine deliberately,
	// rather than killing both at once and leaving the port to time out.
	//
	// Delegated to core so the Windows build stays compilable: syscall.SysProcAttr
	// has no Setpgid field there (it uses CREATE_NEW_PROCESS_GROUP instead). This
	// used to be a direct Setpgid literal, which broke every `GOOS=windows` build.
	core.PrepareCmdForKill(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("heron: start engine at %s: %w", serveURL, err)
	}
	a.mu.Lock()
	a.serverCmd = cmd
	a.mu.Unlock()
	slog.Info("heron: engine started", "pid", cmd.Process.Pid, "url", serveURL)

	if err := waitForHealth(ctx, serveURL, 20*time.Second); err != nil {
		return err
	}
	return nil
}

// stopServeProcess asks the engine to exit. It does not wait: the engine's own
// shutdown path releases the port, and --exit-with-parent covers the case
// where this process never gets here.
func (a *Agent) stopServeProcess() {
	a.mu.Lock()
	cmd := a.serverCmd
	a.serverCmd = nil
	a.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	slog.Info("heron: stopping engine", "pid", cmd.Process.Pid)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		slog.Warn("heron: send SIGTERM to engine", "error", err)
	}
}

func probeHealth(ctx context.Context, baseURL string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/health", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode == http.StatusOK
}

func waitForHealth(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if probeHealth(ctx, baseURL) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("heron: engine at %s did not become ready within %s", baseURL, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// portFromURL extracts the port the engine should bind. An empty result means
// "let the engine use its default", which only works when the URL points at
// that default too.
func portFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Port()
}

// 编译期断言：serve 模式下返回的会话仍是 core.AgentSession。
var _ core.AgentSession = (*httpSession)(nil)
