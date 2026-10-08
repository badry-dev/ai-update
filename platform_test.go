package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestConfigPath checks that configuration uses the host's per-user directory.
func TestConfigPath(t *testing.T) {
	got := configPath()
	if runtime.GOOS == "windows" {
		base, err := os.UserConfigDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(base, "ai-update", "config.yaml")
		if got != want {
			t.Fatalf("configPath() = %q, want %q", got, want)
		}
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base := cmp.Or(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
	want := filepath.Join(base, "ai-update", "config.yaml")
	if got != want {
		t.Fatalf("configPath() = %q, want %q", got, want)
	}
}

// TestLogDir checks that run logs use the host's per-user state directory.
func TestLogDir(t *testing.T) {
	got := logDir()
	if runtime.GOOS == "windows" {
		base, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(base, "ai-update")
		if got != want {
			t.Fatalf("logDir() = %q, want %q", got, want)
		}
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base := cmp.Or(os.Getenv("XDG_STATE_HOME"), filepath.Join(home, ".local", "state"))
	want := filepath.Join(base, "ai-update")
	if got != want {
		t.Fatalf("logDir() = %q, want %q", got, want)
	}
}

// TestDefaultConfigMatchesOS checks tool defaults and privileges for the host.
func TestDefaultConfigMatchesOS(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(defaultConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	byName := map[string]Tool{}
	for _, tool := range cfg.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"claude", "codex", "opencode", "copilot", "grok", "gh"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing tool %s", name)
		}
	}
	if runtime.GOOS == "windows" {
		if strings.Contains(defaultConfig, "apt-get") || strings.Contains(defaultConfig, "snap ") || strings.Contains(defaultConfig, "bash -c") {
			t.Fatalf("windows default config still describes a Linux shell:\n%s", defaultConfig)
		}
		for _, tool := range cfg.Tools {
			if tool.NeedsSudo {
				t.Errorf("%s: needs_sudo is set", tool.Name)
			}
		}
		if !strings.Contains(byName["gh"].UpdateCmd, "winget") || !strings.Contains(byName["gh"].UpdateCmd, "GitHub.cli") {
			t.Errorf("gh update = %q", byName["gh"].UpdateCmd)
		}
		if !strings.Contains(byName["gh"].UpdateCmd, "-1978335189") {
			t.Errorf("gh update should treat 'no applicable upgrade' as success, got %q", byName["gh"].UpdateCmd)
		}
		if byName["claude"].UpdateCmd != "claude update" {
			t.Errorf("claude update = %q", byName["claude"].UpdateCmd)
		}
		if byName["codex"].UpdateCmd != "codex update" {
			t.Errorf("codex update = %q", byName["codex"].UpdateCmd)
		}
		return
	}
	if !strings.Contains(defaultConfig, "bash -c") {
		t.Fatal("unix default config should run commands with bash -c")
	}
	if !byName["codex"].NeedsSudo || !strings.Contains(byName["codex"].UpdateCmd, "snap") {
		t.Errorf("codex = %+v", byName["codex"])
	}
	if !strings.Contains(byName["gh"].UpdateCmd, "apt-get") {
		t.Errorf("gh update = %q", byName["gh"].UpdateCmd)
	}
}

// TestShellCmdUsesHostShell checks shell selection and non-interactive flags.
func TestShellCmdUsesHostShell(t *testing.T) {
	const script = "echo hi"
	cmd := shellCmd(t.Context(), script)
	if cmd.WaitDelay != 3*time.Second {
		t.Fatalf("WaitDelay = %s, want 3s", cmd.WaitDelay)
	}
	if len(cmd.Args) == 0 {
		t.Fatal("shell command has no args")
	}
	joined := strings.Join(cmd.Args, "\n")
	if !strings.Contains(joined, script) {
		t.Fatalf("script not passed to the shell:\n%s", joined)
	}
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(cmd.Args[0], "powershell.exe") {
			t.Fatalf("shell = %q, want powershell.exe", cmd.Args[0])
		}
		for _, flag := range []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"} {
			if !strings.Contains(joined, flag) {
				t.Fatalf("missing %s:\n%s", flag, joined)
			}
		}
		return
	}
	if cmd.Args[0] != "bash" || !strings.Contains(joined, "-c") {
		t.Fatalf("args =\n%s", joined)
	}
}

// TestShellCmdKeepsExitCode checks that native command statuses reach the runner.
func TestShellCmdKeepsExitCode(t *testing.T) {
	script := "exit 7"
	if runtime.GOOS == "windows" {
		script = "cmd /c exit 7"
	}
	cmd := shellCmd(t.Context(), script)
	err := cmd.Run()
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("got %v, want exit status 7", err)
	}

	success := "exit 0"
	if runtime.GOOS == "windows" {
		success = "cmd /c exit 0"
	}
	if err := shellCmd(t.Context(), success).Run(); err != nil {
		t.Fatalf("exit 0 reported failure: %v", err)
	}
}

// TestShellCmdCancelKillsChild checks cancellation of both shell and child.
func TestShellCmdCancelKillsChild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	var script string
	if runtime.GOOS == "windows" {
		script = fmt.Sprintf("$p = Start-Process -FilePath ping.exe -ArgumentList '-n','60','127.0.0.1' -PassThru -WindowStyle Hidden; Set-Content -LiteralPath '%s' -Value $p.Id; Wait-Process -Id $p.Id", pidFile)
	} else {
		script = fmt.Sprintf("sleep 60 & echo $! > '%s'; wait", pidFile)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cmd := shellCmd(ctx, script)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	pid := waitForPID(t, pidFile)
	t.Cleanup(func() { killPID(pid) })

	cancel()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case <-wait:
	case <-time.After(8 * time.Second):
		t.Fatal("shell still running after cancel")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child pid %d still alive after cancel", pid)
}

// waitForPID waits for a test child to publish its PID before cancellation.
func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, conv := strconv.Atoi(strings.TrimSpace(string(data)))
			if conv == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("child pid was not written")
	return 0
}

// processAlive probes whether a test child still exists on the host.
func processAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output()
		return err == nil && strings.Contains(string(out), strconv.Itoa(pid))
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

// TestWindowsIgnoresSudo checks that Unix privilege settings never block Windows.
func TestWindowsIgnoresSudo(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip()
	}
	if sudoAvailable() {
		t.Fatal("sudoAvailable() = true")
	}
	m := newModel([]Tool{{
		Name:      "codex",
		Binary:    "codex",
		UpdateCmd: "codex update",
		NeedsSudo: true,
	}}, nil, &runLog{path: filepath.Join(t.TempDir(), "run.log")})
	t.Cleanup(m.cancel)
	m.w, m.h = 100, 40
	plain := ansiRe.ReplaceAllString(m.View(), "")
	if strings.Contains(plain, "sudo") {
		t.Fatalf("select screen shows sudo:\n%s", plain)
	}

	next, cmd := m.beginRun()
	got := next.(model)
	if got.jobs[0].status != stRunning {
		t.Fatalf("status = %v, sudo prompt blocked the run", got.jobs[0].status)
	}
	if cmd == nil {
		t.Fatal("expected the update to start")
	}
	jobsWG.Done() // startNext's runJob counted a command this test does not run
}

// killPID cleans up test children even when a cancellation assertion fails.
func killPID(pid int) {
	if pid <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		return
	}
	_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
}
