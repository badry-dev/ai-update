package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestMain lets a copy of the test binary act as winget without updating tools.
func TestMain(m *testing.M) {
	if code := os.Getenv("AI_UPDATE_TEST_WINGET_EXIT"); code != "" && strings.EqualFold(filepath.Base(os.Args[0]), "winget.exe") {
		want := []string{"upgrade", "--id", "GitHub.cli", "-e", "--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity"}
		if !slices.Equal(os.Args[1:], want) {
			os.Exit(99)
		}
		exit, err := strconv.Atoi(code)
		if err != nil {
			os.Exit(98)
		}
		os.Exit(exit)
	}
	os.Exit(m.Run())
}

// TestShellCmdPowerShellStatus checks native, cmdlet, and explicit exit statuses.
func TestShellCmdPowerShellStatus(t *testing.T) {
	for _, tt := range []struct {
		name   string
		script string
		code   int
	}{
		{"missing command", "nonexistent-ai-update-command-xyz", 1},
		{"cmdlet error", "Write-Error 'update failed'", 1},
		{"terminating error", "throw 'update failed'", 1},
		{"cmdlet error after native success", "cmd.exe /c exit 0; Write-Error 'update failed'", 1},
		{"cmdlet recovery after native failure", "cmd.exe /c exit 7; Write-Output 'recovered'", 0},
		{"native failure", "cmd.exe /c exit 7", 7},
		{"native success", "cmd.exe /c exit 0", 0},
		{"explicit failure", "exit 9", 9},
		{"explicit success after failure", "cmd.exe /c exit 7; exit 0", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := shellCmd(t.Context(), tt.script)
			assertExitCode(t, cmd, tt.code)
		})
	}
}

// TestShellCmdRunsPowerShellShim checks process-only policy for npm-style shims.
func TestShellCmdRunsPowerShellShim(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ai-update-test-shim.ps1"), []byte("Write-Output 'shim version 1.2.3'"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ai-update-test-shim.cmd"), []byte("@echo wrong shim\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const script = "ai-update-test-shim --version"
	// Prove that the test shim is blocked under the policy we are overriding.
	restricted := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Restricted", "-Command", script)
	restricted.Env = append(os.Environ(), "PATH="+dir)
	if out, err := restricted.CombinedOutput(); err == nil {
		t.Fatalf("Restricted policy unexpectedly allowed the shim: %s", out)
	}
	cmd := shellCmd(t.Context(), script)
	cmd.Env = append(os.Environ(), "PATH="+dir, "PSExecutionPolicyPreference=Restricted")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "shim version 1.2.3" {
		t.Fatalf("PowerShell shim: output = %q, error = %v", out, err)
	}
}

// TestShellCmdCancelAfterShellExit checks children retaining the shell's output.
func TestShellCmdCancelAfterShellExit(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := fmt.Sprintf("$p = Start-Process -FilePath ping.exe -ArgumentList '-n','60','127.0.0.1' -NoNewWindow -PassThru; Set-Content -LiteralPath '%s' -Value $p.Id; exit 0", pidFile)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	cmd := shellCmd(ctx, script)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = 10 * time.Second // Cancellation must not rely on this timeout.
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	pid := waitForPID(t, pidFile)
	t.Cleanup(func() { killPID(pid) })
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(cmd.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("PowerShell did not exit before cancellation")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !processAlive(pid) {
		t.Fatal("child exited before cancellation; inherited-output case was not exercised")
	}
	select {
	case err := <-wait:
		t.Fatalf("Wait returned before cancellation while the child held output: %v", err)
	default:
	}
	cancel()
	select {
	case <-wait:
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return promptly after cancellation")
	}
	deadline = time.Now().Add(3 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("child pid %d survived cancellation after PowerShell exited", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestShellCmdStartCleanup checks job-handle cleanup when startup cannot complete.
func TestShellCmdStartCleanup(t *testing.T) {
	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		cmd := shellCmd(ctx, "exit 0")
		if err := cmd.Run(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context cancellation", err)
		}
		if cmd.job != 0 || cmd.Process != nil {
			t.Fatal("cancelled startup retained a job handle or started a shell")
		}
	})
	t.Run("missing shell", func(t *testing.T) {
		cmd := shellCmd(t.Context(), "exit 0")
		cmd.Path = filepath.Join(t.TempDir(), "missing-powershell.exe")
		if err := cmd.Run(); err == nil {
			t.Fatal("Run succeeded with a missing shell")
		}
		if cmd.job != 0 || cmd.Process != nil {
			t.Fatal("failed startup retained a job handle or started a shell")
		}
	})
}

// TestShellCmdOutput checks containment and status preservation for stdout capture.
func TestShellCmdOutput(t *testing.T) {
	cmd := shellCmd(t.Context(), "Write-Output 'contained output'; cmd.exe /c exit 7")
	out, err := cmd.Output()
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != 7 || strings.TrimSpace(string(out)) != "contained output" {
		t.Fatalf("Output = %q, error = %v", out, err)
	}
	if cmd.job != 0 {
		t.Fatal("completed command retained its job handle")
	}
}

// TestWindowsGHUpdate checks missing winget and real native statuses using a stub.
func TestWindowsGHUpdate(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(defaultConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(cfg.Tools, func(tool Tool) bool { return tool.Name == "gh" })
	if idx < 0 {
		t.Fatal("default config has no gh tool")
	}
	script := cfg.Tools[idx].UpdateCmd
	for _, tt := range []struct {
		name      string
		installed bool
		winget    int
		code      int
	}{
		{"winget missing", false, 0, 1},
		{"upgrade succeeded", true, 0, 0},
		{"already latest", true, -1978335189, 0},
		{"upgrade failed", true, 7, 7},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.installed {
				path, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "winget.exe"), data, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := shellCmd(t.Context(), script)
			cmd.Env = append(os.Environ(), "PATH="+dir, "AI_UPDATE_TEST_WINGET_EXIT="+strconv.Itoa(tt.winget))
			assertExitCode(t, cmd, tt.code)
		})
	}
}

// assertExitCode checks the real shell status and includes diagnostics on failure.
func assertExitCode(t *testing.T, cmd interface{ CombinedOutput() ([]byte, error) }, want int) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if want == 0 {
		if err != nil {
			t.Fatalf("wanted success, got %v: %s", err, out)
		}
		return
	}
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != want {
		t.Fatalf("wanted exit %d, got %v: %s", want, err, out)
	}
}
