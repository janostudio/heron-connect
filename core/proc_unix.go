//go:build unix

package core

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// PrepareCmdForKill configures cmd so that the child process runs in its own
// process group. This allows the parent to kill the entire group (including
// grandchildren the CLI may have spawned) with a single negative-PID kill.
// Call this before cmd.Start().
//
// On Unix this sets Setpgid=true. On Windows (see proc_windows.go) it sets
// CREATE_NEW_PROCESS_GROUP.
func PrepareCmdForKill(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// ForceKillProcessGroup sends SIGKILL to the entire process group led by cmd.
// Use only as a last resort after graceful shutdown and SIGTERM have failed.
// Returns nil if the process is already gone.
func ForceKillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil &&
		!errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// SignalProcessGroup sends sig to the entire process group led by cmd.
// Useful for sending SIGTERM to the group during graceful shutdown.
func SignalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil &&
		!errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
