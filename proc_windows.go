//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// CREATE_NO_WINDOW stops taskkill from flashing a console window.
const createNoWindow = 0x08000000

func shellCmd(ctx context.Context, script string) *exec.Cmd {
	// powershell.exe turns a native command's exit code into 1. Exit with
	// $LASTEXITCODE when a native command set it, so the real status survives.
	script += "\r\nif ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }"
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		// Kill the shell and every process it started. Process.Kill leaves those behind.
		kill := exec.Command("taskkill.exe", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
		return kill.Run()
	}
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

func sudoAvailable() bool { return false }
