package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Tool describes one CLI and how to check / update it.
type Tool struct {
	Name       string `yaml:"name"`
	Binary     string `yaml:"binary"`
	VersionCmd string `yaml:"version_cmd"`
	UpdateCmd  string `yaml:"update_cmd"`
	NeedsSudo  bool   `yaml:"needs_sudo"`
}

type Config struct {
	Tools []Tool `yaml:"tools"`
}

// Written on first run. Commands run via `bash -c`, stdin is closed, so they
// must be non-interactive. needs_sudo tools should use `sudo -n` — ai-update
// asks for your password once before running them.
const defaultConfig = `# ai-update config — one entry per CLI, updated in this order.
#   binary:      looked up on PATH; tools not found are hidden
#   version_cmd: first x.y.z in its output is shown as the version
#   update_cmd:  run with bash -c, no stdin
#   needs_sudo:  prompt for sudo once before the run (use "sudo -n" in the cmd)
tools:
  - name: claude
    binary: claude
    version_cmd: claude --version
    update_cmd: claude update

  - name: codex
    binary: codex
    version_cmd: codex --version
    update_cmd: sudo -n snap refresh codex
    needs_sudo: true

  - name: opencode
    binary: opencode
    version_cmd: opencode --version
    update_cmd: opencode upgrade

  - name: copilot
    binary: copilot
    version_cmd: copilot --version
    update_cmd: copilot update

  - name: grok
    binary: grok
    version_cmd: grok --version
    update_cmd: grok update

  - name: gh
    binary: gh
    version_cmd: gh --version
    update_cmd: sudo -n apt-get update -qq && sudo -n apt-get install --only-upgrade -y gh
    needs_sudo: true
`

func configPath() string {
	home, _ := os.UserHomeDir()
	dir := cmp.Or(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
	return filepath.Join(dir, "ai-update", "config.yaml")
}

// loadConfig reads the config, creating the default one if it doesn't exist.
func loadConfig(path string) (*Config, bool, error) {
	created := false
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(path, []byte(defaultConfig), 0o644); err != nil {
			return nil, false, err
		}
		data, created = []byte(defaultConfig), true
	} else if err != nil {
		return nil, false, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", path, err)
	}
	for i, t := range cfg.Tools {
		if t.Name == "" || t.UpdateCmd == "" {
			return nil, false, fmt.Errorf("%s: tool #%d needs at least name and update_cmd", path, i+1)
		}
		cfg.Tools[i].Binary = cmp.Or(t.Binary, t.Name)
	}
	return &cfg, created, nil
}
