package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const envPrefix = "NEUROX_"

type Config struct {
	Database   DatabaseConfig   `yaml:"database"`
	LLM        LLMConfig        `yaml:"llm"`
	Embeddings EmbeddingsConfig `yaml:"embeddings"`
	Meta       MetaConfig       `yaml:"-"`
}

type LLMConfig struct {
	Provider string `yaml:"provider"`  // "ollama", "remote", "disabled"
	GateMode string `yaml:"gate_mode"` // "auto", "full", "off"

	// Ollama settings
	OllamaURL   string `yaml:"ollama_url"`
	OllamaModel string `yaml:"ollama_model"`

	// Remote (OpenAI-compatible) settings
	RemoteURL    string `yaml:"remote_url"`
	RemoteAPIKey string `yaml:"remote_api_key"`
	RemoteModel  string `yaml:"remote_model"`
}

type EmbeddingsConfig struct {
	Provider    string `yaml:"provider"`   // "ollama", "remote", "disabled", "" (auto)
	RemoteURL   string `yaml:"remote_url"` // OpenAI-compatible embeddings endpoint base URL
	RemoteKey   string `yaml:"remote_api_key"`
	RemoteModel string `yaml:"remote_model"`
	Dimensions  int    `yaml:"dimensions"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type MetaConfig struct {
	ConfigPath string
	ConfigDir  string
	LoadedFrom string
	Source     string
}

func Load(configPath string) (Config, error) {
	configDir, err := resolveConfigDir()
	if err != nil {
		return Config{}, err
	}

	if configPath == "" {
		configPath = filepath.Join(configDir, "config.yaml")
	}

	cfg := defaultConfig(configDir, configPath)

	if data, readErr := os.ReadFile(configPath); readErr == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", configPath, err)
		}
		cfg.Meta.LoadedFrom = configPath
		cfg.Meta.Source = "file"
	} else if !os.IsNotExist(readErr) {
		return Config{}, fmt.Errorf("read %s: %w", configPath, readErr)
	}

	applyEnvOverrides(&cfg, configPath, configDir)
	applyDerivedDefaults(&cfg, configPath, configDir)

	return cfg, nil

}

func resolveConfigDir() (string, error) {
	if override := strings.TrimSpace(os.Getenv(envPrefix + "CONFIG_DIR")); override != "" {
		return filepath.Clean(override), nil
	}

	// Use XDG_CONFIG_HOME if set, otherwise ~/.config.
	// os.UserConfigDir() returns ~/Library/Application Support on macOS
	// which is wrong for CLI tools.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	baseDir := os.Getenv("XDG_CONFIG_HOME")
	if baseDir == "" {
		baseDir = filepath.Join(homeDir, ".config")
	}

	return filepath.Join(baseDir, "neurox"), nil
}

func defaultConfig(configDir string, configPath string) Config {
	return Config{
		Database: DatabaseConfig{
			Path: filepath.Join(configDir, "neurox.db"),
		},
		Meta: MetaConfig{
			ConfigDir:  configDir,
			ConfigPath: configPath,
			Source:     "defaults",
		},
	}
}

func applyEnvOverrides(cfg *Config, configPath string, configDir string) {
	if value := strings.TrimSpace(os.Getenv(envPrefix + "DATABASE_PATH")); value != "" {
		cfg.Database.Path = value
		cfg.Meta.Source = "env"
	}

	if value := strings.TrimSpace(os.Getenv(envPrefix + "CONFIG_PATH")); value != "" {
		cfg.Meta.ConfigPath = filepath.Clean(value)
		cfg.Meta.Source = "env"
	} else {
		cfg.Meta.ConfigPath = configPath
	}

	if value := strings.TrimSpace(os.Getenv(envPrefix + "CONFIG_DIR")); value != "" {
		cfg.Meta.ConfigDir = filepath.Clean(value)
		cfg.Meta.Source = "env"
	} else {
		cfg.Meta.ConfigDir = configDir
	}

	if value := strings.TrimSpace(os.Getenv(envPrefix + "LLM_PROVIDER")); value != "" {
		cfg.LLM.Provider = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "LLM_GATE_MODE")); value != "" {
		cfg.LLM.GateMode = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "LLM_OLLAMA_URL")); value != "" {
		cfg.LLM.OllamaURL = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "LLM_OLLAMA_MODEL")); value != "" {
		cfg.LLM.OllamaModel = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "LLM_REMOTE_URL")); value != "" {
		cfg.LLM.RemoteURL = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "LLM_REMOTE_API_KEY")); value != "" {
		cfg.LLM.RemoteAPIKey = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "LLM_REMOTE_MODEL")); value != "" {
		cfg.LLM.RemoteModel = value
		cfg.Meta.Source = "env"
	}

	if value := strings.TrimSpace(os.Getenv(envPrefix + "EMBED_PROVIDER")); value != "" {
		cfg.Embeddings.Provider = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "EMBED_REMOTE_URL")); value != "" {
		cfg.Embeddings.RemoteURL = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "EMBED_REMOTE_API_KEY")); value != "" {
		cfg.Embeddings.RemoteKey = value
		cfg.Meta.Source = "env"
	}
	if value := strings.TrimSpace(os.Getenv(envPrefix + "EMBED_REMOTE_MODEL")); value != "" {
		cfg.Embeddings.RemoteModel = value
		cfg.Meta.Source = "env"
	}
}

func applyDerivedDefaults(cfg *Config, configPath string, configDir string) {
	if strings.TrimSpace(cfg.Database.Path) == "" {
		cfg.Database.Path = filepath.Join(cfg.Meta.ConfigDir, "neurox.db")
	}

	if cfg.Meta.ConfigPath == "" {
		cfg.Meta.ConfigPath = configPath
	}

	if cfg.Meta.ConfigDir == "" {
		cfg.Meta.ConfigDir = configDir
	}

	if cfg.Meta.LoadedFrom == "" && cfg.Meta.Source == "file" {
		cfg.Meta.LoadedFrom = cfg.Meta.ConfigPath
	}
	if cfg.Meta.Source == "" {
		cfg.Meta.Source = "defaults"
	}
	if cfg.Meta.LoadedFrom == "" {
		cfg.Meta.LoadedFrom = cfg.Meta.Source
	}
	cfg.Database.Path = filepath.Clean(cfg.Database.Path)
}
