package tui

import (
	"testing"

	"github.com/emdash-ai/glmt/internal/auth"
	"github.com/emdash-ai/glmt/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestExtractProjectPath_SSH(t *testing.T) {
	got := extractProjectPath("git@gitlab.com:team/project.git")
	assert.Equal(t, "team/project", got)
}

func TestExtractProjectPath_HTTPS(t *testing.T) {
	got := extractProjectPath("https://gitlab.com/team/project.git")
	assert.Equal(t, "team/project", got)
}

func TestExtractProjectPath_HTTPSNoGit(t *testing.T) {
	got := extractProjectPath("https://gitlab.com/team/project")
	assert.Equal(t, "team/project", got)
}

func TestExtractProjectPath_Empty(t *testing.T) {
	got := extractProjectPath("")
	assert.Empty(t, got)
}

func TestApp_StartsAtSetupWhenNoCredentials(t *testing.T) {
	m := NewAppModel(nil, config.DefaultConfig(), "/tmp/test-config.toml")
	assert.Equal(t, ScreenSetup, m.screen)
}

func TestApp_StartsAtMRListWhenProjectConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.ProjectID = 42
	cfg.Defaults.Repo = "team/project"
	creds := &auth.Credentials{Host: "gitlab.example.com", Token: "test-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml")
	assert.Equal(t, ScreenMRList, m.screen)
}

func TestApp_StartsAtRepoPickerWhenNoProject(t *testing.T) {
	cfg := config.DefaultConfig()
	creds := &auth.Credentials{Host: "gitlab.example.com", Token: "test-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml")
	assert.Equal(t, ScreenRepoPicker, m.screen)
}
