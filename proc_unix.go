//go:build unix

package main

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// shellCmd runs a Bash command and cancels its process group.
func shellCmd(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	// Own process group so cancel kills the whole pipeline, not just bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	// Bounds the wait if a tool leaves a background child holding the pipe open.
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

// sudoAvailable reports whether this platform uses sudo for selected tools.
func sudoAvailable() bool { return true }
