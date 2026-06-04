package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestEmbeddingsOllamaConfigWired(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte(`embeddings:
  ollama_url: http://custom:11434
  ollama_model: custom-model
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Embeddings.OllamaURL != "http://custom:11434" {
		t.Errorf("Embeddings.OllamaURL = %q, want http://custom:11434", cfg.Embeddings.OllamaURL)
	}
	if cfg.Embeddings.OllamaModel != "custom-model" {
		t.Errorf("Embeddings.OllamaModel = %q, want custom-model", cfg.Embeddings.OllamaModel)
	}
}

func TestEmbeddingsOllamaEnvOverrides(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte(`embeddings:
  ollama_url: http://yaml:11434
  ollama_model: yaml-model
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv(envPrefix+"EMBED_OLLAMA_URL", "http://env:11434")
	t.Setenv(envPrefix+"EMBED_OLLAMA_MODEL", "env-model")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Embeddings.OllamaURL != "http://env:11434" {
		t.Errorf("Embeddings.OllamaURL = %q, want http://env:11434", cfg.Embeddings.OllamaURL)
	}
	if cfg.Embeddings.OllamaModel != "env-model" {
		t.Errorf("Embeddings.OllamaModel = %q, want env-model", cfg.Embeddings.OllamaModel)
	}
	if cfg.Meta.Source != "env" {
		t.Errorf("Meta.Source = %q, want env", cfg.Meta.Source)
	}
}

func TestConsolidationDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(envPrefix+"CONFIG_DIR", "")
	t.Setenv(envPrefix+"CONFIG_PATH", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Consolidation.DedupThreshold != 0.85 {
		t.Errorf("DedupThreshold = %f, want 0.85", cfg.Consolidation.DedupThreshold)
	}
	if cfg.Consolidation.ContradictionMin != 0.65 {
		t.Errorf("ContradictionMin = %f, want 0.65", cfg.Consolidation.ContradictionMin)
	}
	if cfg.Consolidation.ContradictionMax != 0.85 {
		t.Errorf("ContradictionMax = %f, want 0.85", cfg.Consolidation.ContradictionMax)
	}
	if cfg.Consolidation.RelatedMin != 0.65 {
		t.Errorf("RelatedMin = %f, want 0.65", cfg.Consolidation.RelatedMin)
	}
	if cfg.Consolidation.RelatedMax != 0.85 {
		t.Errorf("RelatedMax = %f, want 0.85 (DedupThreshold)", cfg.Consolidation.RelatedMax)
	}
}

func TestConsolidationConfigParsing(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte(`consolidation:
  dedup_threshold: 0.90
  contradiction_min: 0.60
  contradiction_max: 0.95
  related_min: 0.55
  related_max: 0.80
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Consolidation.DedupThreshold != 0.90 {
		t.Errorf("DedupThreshold = %f, want 0.90", cfg.Consolidation.DedupThreshold)
	}
	if cfg.Consolidation.ContradictionMin != 0.60 {
		t.Errorf("ContradictionMin = %f, want 0.60", cfg.Consolidation.ContradictionMin)
	}
	if cfg.Consolidation.ContradictionMax != 0.95 {
		t.Errorf("ContradictionMax = %f, want 0.95", cfg.Consolidation.ContradictionMax)
	}
	if cfg.Consolidation.RelatedMin != 0.55 {
		t.Errorf("RelatedMin = %f, want 0.55", cfg.Consolidation.RelatedMin)
	}
	if cfg.Consolidation.RelatedMax != 0.80 {
		t.Errorf("RelatedMax = %f, want 0.80", cfg.Consolidation.RelatedMax)
	}
}

func TestRecallDefaultsWhenConfigMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(envPrefix+"CONFIG_DIR", "")
	t.Setenv(envPrefix+"CONFIG_PATH", "")
	t.Setenv(envPrefix+"RECALL_RRF_K", "")
	t.Setenv(envPrefix+"RECALL_DISABLE_BACKFILL", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Recall.RRF.K != 60 {
		t.Errorf("Recall.RRF.K = %d, want 60", cfg.Recall.RRF.K)
	}
	if cfg.Recall.DisableBackfill != false {
		t.Errorf("Recall.DisableBackfill = %v, want false", cfg.Recall.DisableBackfill)
	}
}

func TestRecallYAMLConfig(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte(`recall:
  rrf:
    k: 30
  disable_backfill: true
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv(envPrefix+"RECALL_RRF_K", "")
	t.Setenv(envPrefix+"RECALL_DISABLE_BACKFILL", "")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Recall.RRF.K != 30 {
		t.Errorf("Recall.RRF.K = %d, want 30", cfg.Recall.RRF.K)
	}
	if cfg.Recall.DisableBackfill != true {
		t.Errorf("Recall.DisableBackfill = %v, want true", cfg.Recall.DisableBackfill)
	}
}

func TestRecallEnvOverridesK(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv(envPrefix+"RECALL_RRF_K", "30")
	t.Setenv(envPrefix+"RECALL_DISABLE_BACKFILL", "")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Recall.RRF.K != 30 {
		t.Errorf("Recall.RRF.K = %d, want 30", cfg.Recall.RRF.K)
	}
	if cfg.Meta.Source != "env" {
		t.Errorf("Meta.Source = %q, want env", cfg.Meta.Source)
	}
}

func TestRecallEnvOverridesDisableBackfill(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv(envPrefix+"RECALL_RRF_K", "")
	t.Setenv(envPrefix+"RECALL_DISABLE_BACKFILL", "true")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Recall.DisableBackfill != true {
		t.Errorf("Recall.DisableBackfill = %v, want true", cfg.Recall.DisableBackfill)
	}
}

func TestRecallEnvOverridesYAML(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte(`recall:
  rrf:
    k: 30
  disable_backfill: false
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv(envPrefix+"RECALL_RRF_K", "90")
	t.Setenv(envPrefix+"RECALL_DISABLE_BACKFILL", "true")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Recall.RRF.K != 90 {
		t.Errorf("Recall.RRF.K = %d, want 90 (env override over YAML 30)", cfg.Recall.RRF.K)
	}
	if cfg.Recall.DisableBackfill != true {
		t.Errorf("Recall.DisableBackfill = %v, want true (env override over YAML false)", cfg.Recall.DisableBackfill)
	}
}

func TestRecallInvalidKReturnsError(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte(`recall:
  rrf:
    k: 0
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv(envPrefix+"RECALL_RRF_K", "")
	t.Setenv(envPrefix+"RECALL_DISABLE_BACKFILL", "")

	_, err := Load(configPath)
	if err == nil {
		t.Fatalf("Load returned nil error, want validation error for K=0")
	}
	if !strings.Contains(err.Error(), "recall.rrf.k") {
		t.Errorf("error = %q, want message mentioning recall.rrf.k", err.Error())
	}
}
