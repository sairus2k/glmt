package config

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Config holds the glmt configuration.
type Config struct {
	GitLab   GitLabConfig   `toml:"gitlab"`
	Defaults DefaultsConfig `toml:"defaults"`
	Behavior BehaviorConfig `toml:"behavior"`
}

// GitLabConfig holds GitLab connection settings.
type GitLabConfig struct {
	Host  string `toml:"host"`
	Token string `toml:"token"`
}

// DefaultsConfig holds default project settings.
type DefaultsConfig struct {
	Repo      string `toml:"repo"`
	ProjectID int    `toml:"project_id"`
}

// BehaviorConfig holds behavioral settings.
type BehaviorConfig struct {
	PollRebaseIntervalS   int `toml:"poll_rebase_interval_s"`
	PollPipelineIntervalS int `toml:"poll_pipeline_interval_s"`
}

// DefaultConfig returns the config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Behavior: BehaviorConfig{
			PollRebaseIntervalS:   2,
			PollPipelineIntervalS: 10,
		},
	}
}

// Load reads the config from the given path. If the file doesn't exist, returns DefaultConfig.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, err
	}

	cfg := DefaultConfig()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Save writes the config to the given path, creating parent dirs as needed.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// DefaultPath returns the default config file path (~/.config/glmt/config.toml).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(home, ".config", "glmt", "config.toml")
}
