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

// shellCmd runs a non-interactive PowerShell command and cancels its process tree.
func shellCmd(ctx context.Context, script string) *exec.Cmd {
	// Save the final command's status before another statement changes it.
	// Native failures keep their exit code; cmdlet failures may not set one.
	// A successful final command can recover from an earlier native failure.
	script += "\r\n$__aiUpdateOK = $?; if (-not $__aiUpdateOK) { if ($LASTEXITCODE) { exit $LASTEXITCODE }; exit 1 }; exit 0"
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
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

// sudoAvailable reports whether this platform uses sudo for selected tools.
func sudoAvailable() bool { return false }
