package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesDefaultsWhenConfigIsMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(envPrefix+"CONFIG_DIR", "")
	t.Setenv(envPrefix+"CONFIG_PATH", "")
	t.Setenv(envPrefix+"DATABASE_PATH", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	wantDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "neurox")
	if cfg.Meta.ConfigDir != wantDir {
		t.Fatalf("ConfigDir = %q, want %q", cfg.Meta.ConfigDir, wantDir)
	}
	if cfg.Database.Path != filepath.Join(wantDir, "neurox.db") {
		t.Fatalf("Database.Path = %q", cfg.Database.Path)
	}
	if cfg.Meta.Source != "defaults" {
		t.Fatalf("Source = %q, want defaults", cfg.Meta.Source)
	}
}

func TestLoadReadsYAMLAndAppliesEnvOverrides(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte("database:\n  path: /tmp/from-yaml.db\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv(envPrefix+"DATABASE_PATH", filepath.Join(root, "from-env.db"))

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Database.Path != filepath.Join(root, "from-env.db") {
		t.Fatalf("Database.Path = %q", cfg.Database.Path)
	}
	if cfg.Meta.LoadedFrom != configPath {
		t.Fatalf("LoadedFrom = %q, want %q", cfg.Meta.LoadedFrom, configPath)
	}
	if cfg.Meta.ConfigPath != configPath {
		t.Fatalf("ConfigPath = %q, want %q", cfg.Meta.ConfigPath, configPath)
	}
	if cfg.Meta.Source != "env" {
		t.Fatalf("Source = %q, want env", cfg.Meta.Source)
	}
}
