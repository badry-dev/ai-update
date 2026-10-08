package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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

func configPath() string {
	if runtime.GOOS == "windows" {
		dir, err := os.UserConfigDir()
		if err != nil {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(dir, "ai-update", "config.yaml")
	}
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
