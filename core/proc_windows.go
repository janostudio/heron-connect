//go:build windows

package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// PrepareCmdForKill configures cmd so that the child process runs in its own
// process group. On Windows this sets CREATE_NEW_PROCESS_GROUP.
func PrepareCmdForKill(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// ForceKillProcessGroup kills the process tree rooted at cmd via taskkill /T.
// Falls back to cmd.Process.Kill() if taskkill fails.
func ForceKillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	killCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	output, err := killCmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if bytes.Contains(bytes.ToLower(output), []byte("there is no running instance")) {
		return nil
	}
	if bytes.Contains(bytes.ToLower(output), []byte("not found")) {
		return nil
	}
	if killErr := cmd.Process.Kill(); killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	} else {
		return fmt.Errorf("taskkill failed: %w: %s; process kill fallback failed: %w",
			err, processKillOutput(output), killErr)
	}
}

// SignalProcessGroup is a no-op on Windows; signal semantics don't translate.
// Graceful shutdown should use stdin close or ForceKillProcessGroup.
func SignalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	// Windows has no POSIX signals; fall through to direct kill.
	return nil
}

func processKillOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return "(empty output)"
	}
	return trimmed
}
