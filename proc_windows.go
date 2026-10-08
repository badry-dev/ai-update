//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// shellCommand owns the job containing PowerShell and all its descendants.
type shellCommand struct {
	*exec.Cmd
	ctx  context.Context
	mu   sync.Mutex // guards the job handle against cancellation and close
	job  windows.Handle
	stop func() bool
}

// shellCmd runs a non-interactive PowerShell command and cancels its process tree.
func shellCmd(ctx context.Context, script string) *shellCommand {
	// Save the final command's status before another statement changes it.
	// Native failures keep their exit code; cmdlet failures may not set one.
	// A successful final command can recover from an earlier native failure.
	script += "\r\n$__aiUpdateOK = $?; if (-not $__aiUpdateOK) { if ($LASTEXITCODE) { exit $LASTEXITCODE }; exit 1 }; exit 0"
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	// Assign the job before PowerShell can start any descendants.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.WaitDelay = 3 * time.Second
	c := &shellCommand{Cmd: cmd, ctx: ctx}
	cmd.Cancel = c.terminateJob
	return c
}

// Start contains the suspended shell in a job before resuming its primary thread.
func (c *shellCommand) Start() error {
	if c.Process != nil {
		return errors.New("exec: already started")
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create process job: %w", err)
	}
	info := new(windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	})
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info))); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("configure process job: %w", err)
	}
	c.mu.Lock()
	c.job = job
	c.mu.Unlock()
	if err := c.Cmd.Start(); err != nil {
		c.closeJob()
		return err
	}
	if err := c.attachAndResume(); err != nil {
		// Failure must not leave a suspended shell or its output pipes behind.
		_ = c.Process.Kill()
		_ = c.Cmd.Wait()
		c.closeJob()
		return err
	}
	return nil
}

// attachAndResume assigns the process and keeps cancellation active after exit.
func (c *shellCommand) attachAndResume() error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid))
	if err != nil {
		return fmt.Errorf("open suspended shell: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(c.job, process); err != nil {
		return fmt.Errorf("assign shell to process job: %w", err)
	}
	// Cmd's watcher can finish when the shell exits, before output pipes drain.
	c.stop = context.AfterFunc(c.ctx, func() { _ = c.terminateJob() })
	if err := c.ctx.Err(); err != nil {
		return err
	}
	return resumeProcess(uint32(c.Process.Pid))
}

// resumeProcess resumes the primary thread of a newly suspended process.
func resumeProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot shell thread: %w", err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err := windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return fmt.Errorf("open shell thread: %w", err)
		}
		defer windows.CloseHandle(thread)
		if _, err := windows.ResumeThread(thread); err != nil {
			return fmt.Errorf("resume shell thread: %w", err)
		}
		return nil
	}
	return errors.New("suspended shell thread not found")
}

// terminateJob kills descendants even when PowerShell has already exited.
func (c *shellCommand) terminateJob() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job == 0 {
		return os.ErrProcessDone
	}
	return windows.TerminateJobObject(c.job, 1)
}

// closeJob releases the handle and kills any descendants left after completion.
func (c *shellCommand) closeJob() {
	if c.stop != nil {
		c.stop()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job != 0 {
		windows.CloseHandle(c.job)
		c.job = 0
	}
}

// Wait drains command output before releasing the job and its cancellation hook.
func (c *shellCommand) Wait() error {
	err := c.Cmd.Wait()
	c.closeJob()
	if err == nil {
		err = c.ctx.Err()
	}
	return err
}

// Run starts the contained shell and waits for its output and status.
func (c *shellCommand) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// CombinedOutput collects both streams while using the contained process lifecycle.
func (c *shellCommand) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil || c.Stderr != nil {
		return nil, errors.New("exec: Stdout or Stderr already set")
	}
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	return out.Bytes(), err
}

// Output collects stdout and preserves stderr diagnostics on native failure.
func (c *shellCommand) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var out, stderr bytes.Buffer
	c.Stdout = &out
	capture := c.Stderr == nil
	if capture {
		c.Stderr = &stderr
	}
	err := c.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); capture && ok {
		exit.Stderr = stderr.Bytes()
	}
	return out.Bytes(), err
}

// sudoAvailable reports whether this platform uses sudo for selected tools.
func sudoAvailable() bool { return false }
