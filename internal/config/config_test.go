package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Behavior.PollRebaseIntervalS != 2 {
		t.Errorf("PollRebaseIntervalS = %d, want 2", cfg.Behavior.PollRebaseIntervalS)
	}
	if cfg.Behavior.PollPipelineIntervalS != 10 {
		t.Errorf("PollPipelineIntervalS = %d, want 10", cfg.Behavior.PollPipelineIntervalS)
	}
	if cfg.Behavior.MainPipelineTimeoutM != 20 {
		t.Errorf("MainPipelineTimeoutM = %d, want 20", cfg.Behavior.MainPipelineTimeoutM)
	}
	if cfg.GitLab.Host != "" {
		t.Errorf("Host = %q, want empty", cfg.GitLab.Host)
	}
	if cfg.Defaults.Repo != "" {
		t.Errorf("Repo = %q, want empty", cfg.Defaults.Repo)
	}
	if cfg.Defaults.ProjectID != 0 {
		t.Errorf("ProjectID = %d, want 0", cfg.Defaults.ProjectID)
	}
}

func TestLoadNonExistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "config.toml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Behavior.PollRebaseIntervalS != 2 {
		t.Errorf("PollRebaseIntervalS = %d, want 2", cfg.Behavior.PollRebaseIntervalS)
	}
	if cfg.Behavior.PollPipelineIntervalS != 10 {
		t.Errorf("PollPipelineIntervalS = %d, want 10", cfg.Behavior.PollPipelineIntervalS)
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "config.toml")

	original := &Config{
		GitLab: GitLabConfig{
			Host:  "gitlab.example.com",
			Token: "glpat-xxxxxxxxxxxxxxxxxxxx",
		},
		Defaults: DefaultsConfig{
			Repo:      "myteam/myrepo",
			ProjectID: 123,
		},
		Behavior: BehaviorConfig{
			PollRebaseIntervalS:   5,
			PollPipelineIntervalS: 20,
			MainPipelineTimeoutM:  30,
		},
	}

	if err := Save(path, original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.GitLab.Host != original.GitLab.Host {
		t.Errorf("Host = %q, want %q", loaded.GitLab.Host, original.GitLab.Host)
	}
	if loaded.GitLab.Token != original.GitLab.Token {
		t.Errorf("Token = %q, want %q", loaded.GitLab.Token, original.GitLab.Token)
	}
	if loaded.Defaults.Repo != original.Defaults.Repo {
		t.Errorf("Repo = %q, want %q", loaded.Defaults.Repo, original.Defaults.Repo)
	}
	if loaded.Defaults.ProjectID != original.Defaults.ProjectID {
		t.Errorf("ProjectID = %d, want %d", loaded.Defaults.ProjectID, original.Defaults.ProjectID)
	}
	if loaded.Behavior.PollRebaseIntervalS != original.Behavior.PollRebaseIntervalS {
		t.Errorf("PollRebaseIntervalS = %d, want %d", loaded.Behavior.PollRebaseIntervalS, original.Behavior.PollRebaseIntervalS)
	}
	if loaded.Behavior.PollPipelineIntervalS != original.Behavior.PollPipelineIntervalS {
		t.Errorf("PollPipelineIntervalS = %d, want %d", loaded.Behavior.PollPipelineIntervalS, original.Behavior.PollPipelineIntervalS)
	}
	if loaded.Behavior.MainPipelineTimeoutM != original.Behavior.MainPipelineTimeoutM {
		t.Errorf("MainPipelineTimeoutM = %d, want %d", loaded.Behavior.MainPipelineTimeoutM, original.Behavior.MainPipelineTimeoutM)
	}
}

func TestLoadPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Write a partial TOML file with only the [gitlab] section.
	// This simulates a user who only configured their GitLab host.
	partialTOML := []byte("[gitlab]\nhost = \"gitlab.internal.io\"\n")
	if err := os.WriteFile(path, partialTOML, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// The gitlab host should come from the file.
	if loaded.GitLab.Host != "gitlab.internal.io" {
		t.Errorf("Host = %q, want %q", loaded.GitLab.Host, "gitlab.internal.io")
	}

	// The behavior defaults should be preserved since they come from DefaultConfig
	// and the file does not set them.
	if loaded.Behavior.PollRebaseIntervalS != 2 {
		t.Errorf("PollRebaseIntervalS = %d, want 2", loaded.Behavior.PollRebaseIntervalS)
	}
	if loaded.Behavior.PollPipelineIntervalS != 10 {
		t.Errorf("PollPipelineIntervalS = %d, want 10", loaded.Behavior.PollPipelineIntervalS)
	}
	if loaded.Behavior.MainPipelineTimeoutM != 20 {
		t.Errorf("MainPipelineTimeoutM = %d, want 20", loaded.Behavior.MainPipelineTimeoutM)
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	// Write syntactically invalid TOML content.
	if err := os.WriteFile(path, []byte("[gitlab\nhost = broken"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want unmarshal error for invalid TOML")
	}
}

func TestSave_ReadOnlyPath(t *testing.T) {
	// /dev/null is a file, so MkdirAll cannot create a directory under it.
	path := "/dev/null/impossible/config.toml"

	err := Save(path, DefaultConfig())
	if err == nil {
		t.Fatal("Save() error = nil, want MkdirAll error")
	}
}

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()

	if !filepath.IsAbs(path) {
		t.Errorf("DefaultPath() = %q, want absolute path", path)
	}

	dir, file := filepath.Split(path)
	if file != "config.toml" {
		t.Errorf("filename = %q, want %q", file, "config.toml")
	}

	// The directory should end with .config/glmt/
	if filepath.Base(filepath.Clean(dir)) != "glmt" {
		t.Errorf("parent dir = %q, want to end with 'glmt'", dir)
	}

	grandparent := filepath.Base(filepath.Dir(filepath.Clean(dir)))
	if grandparent != ".config" {
		t.Errorf("grandparent dir = %q, want '.config'", grandparent)
	}
}
