//go:build unix

package main

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

func shellCmd(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	// Own process group so cancel kills the whole pipeline, not just bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	// Bounds the wait if a tool leaves a background child holding the pipe open.
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

func sudoAvailable() bool { return true }
