package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/auth"
	"github.com/sairus2k/glmt/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	m := NewAppModel(nil, config.DefaultConfig(), "/tmp/test-config.toml", 0)
	assert.Equal(t, ScreenSetup, m.screen)
}

func TestApp_StartsAtMRListWhenProjectConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.ProjectID = 42
	cfg.Defaults.Repo = "team/project"
	creds := &auth.Credentials{Host: "gitlab.example.com", Token: "test-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml", 0)
	assert.Equal(t, ScreenMRList, m.screen)
}

func TestApp_StartsAtRepoPickerWhenNoProject(t *testing.T) {
	cfg := config.DefaultConfig()
	creds := &auth.Credentials{Host: "gitlab.example.com", Token: "test-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml", 0)
	assert.Equal(t, ScreenRepoPicker, m.screen)
}

func TestApp_SetupSuccessTransitionsToRepoPicker(t *testing.T) {
	cfg := config.DefaultConfig()
	cfgPath := t.TempDir() + "/config.toml"
	m := NewAppModel(nil, cfg, cfgPath, 0)
	require.Equal(t, ScreenSetup, m.screen)

	// Type host and token to populate the setup model
	var model tea.Model = m
	model = typeString(t, model, "gitlab.example.com")
	model, _ = model.Update(specialKeyPress(tea.KeyEnter))
	model = typeString(t, model, "glpat-test-token")

	// Simulate credential validation success
	model, cmd := model.Update(credentialsValidMsg{userName: "Test User"})
	app := model.(AppModel)

	assert.Equal(t, ScreenRepoPicker, app.screen)
	assert.NotNil(t, app.client, "client must be set after setup success")
	assert.Equal(t, "gitlab.example.com", app.runtimeHost)
	require.NotNil(t, cmd, "should return fetchProjects command")
}

func TestApp_CLIOverrideProjectID_DoesNotAffectConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.ProjectID = 0
	creds := &auth.Credentials{Host: "gitlab.example.com", Token: "test-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml", 99)

	// The override should be used for the current session
	assert.Equal(t, 99, m.projectID)
	assert.Equal(t, ScreenMRList, m.screen)
	// But the config should not be mutated
	assert.Equal(t, 0, cfg.Defaults.ProjectID)
}

func TestApp_CLICredentials_DoNotPersistToConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GitLab.Host = "saved-host.example.com"
	cfg.GitLab.Token = "saved-token"
	creds := &auth.Credentials{Host: "cli-host.example.com", Token: "cli-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml", 0)

	// runtimeHost should reflect the CLI-provided creds
	assert.Equal(t, "cli-host.example.com", m.runtimeHost)
	// But the config should retain its original values
	assert.Equal(t, "saved-host.example.com", cfg.GitLab.Host)
	assert.Equal(t, "saved-token", cfg.GitLab.Token)
}
