package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

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
func assertExitCode(t *testing.T, cmd *exec.Cmd, want int) {
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
