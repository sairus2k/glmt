package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/emdash-ai/glmt/internal/gitlab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testProjects = []*gitlab.Project{
	{ID: 1, PathWithNamespace: "team/project-alpha", WebURL: "https://gitlab.com/team/project-alpha"},
	{ID: 2, PathWithNamespace: "team/project-beta", WebURL: "https://gitlab.com/team/project-beta"},
	{ID: 3, PathWithNamespace: "other/gamma", WebURL: "https://gitlab.com/other/gamma"},
}

func loadProjects(t *testing.T, m RepoPickerModel) RepoPickerModel {
	t.Helper()
	updated, _ := m.Update(projectsLoadedMsg{projects: testProjects})
	return updated.(RepoPickerModel)
}

func repoPickerPressKey(t *testing.T, m RepoPickerModel, key string) RepoPickerModel {
	t.Helper()
	var msg tea.KeyPressMsg
	switch key {
	case "up":
		msg = specialKeyPress(tea.KeyUp)
	case "down":
		msg = specialKeyPress(tea.KeyDown)
	case "enter":
		msg = specialKeyPress(tea.KeyEnter)
	case "escape":
		msg = specialKeyPress(tea.KeyEscape)
	case "backspace":
		msg = specialKeyPress(tea.KeyBackspace)
	default:
		msg = keyPress(key)
	}
	updated, _ := m.Update(msg)
	return updated.(RepoPickerModel)
}

func TestRepoPicker_InitialState(t *testing.T) {
	m := NewRepoPickerModel("")

	assert.Equal(t, 0, m.Cursor())
	assert.Empty(t, m.Search())
	assert.Nil(t, m.Selected())
	assert.Nil(t, m.Filtered())
	assert.Nil(t, m.projects)
}

func TestRepoPicker_ProjectsLoaded(t *testing.T) {
	m := NewRepoPickerModel("")
	m = loadProjects(t, m)

	require.Len(t, m.Filtered(), 3)
	assert.Equal(t, "team/project-alpha", m.Filtered()[0].PathWithNamespace)
	assert.Equal(t, "team/project-beta", m.Filtered()[1].PathWithNamespace)
	assert.Equal(t, "other/gamma", m.Filtered()[2].PathWithNamespace)
	assert.Equal(t, 0, m.Cursor())
}

func TestRepoPicker_CursorMovement(t *testing.T) {
	m := NewRepoPickerModel("")
	m = loadProjects(t, m)

	// Move down
	m = repoPickerPressKey(t, m, "down")
	assert.Equal(t, 1, m.Cursor())

	// Move down again
	m = repoPickerPressKey(t, m, "down")
	assert.Equal(t, 2, m.Cursor())

	// Move up
	m = repoPickerPressKey(t, m, "up")
	assert.Equal(t, 1, m.Cursor())

	// Test j/k movement
	m = repoPickerPressKey(t, m, "j")
	assert.Equal(t, 2, m.Cursor())

	m = repoPickerPressKey(t, m, "k")
	assert.Equal(t, 1, m.Cursor())
}

func TestRepoPicker_CursorWraps(t *testing.T) {
	m := NewRepoPickerModel("")
	m = loadProjects(t, m)

	// Cursor should not go above 0
	m = repoPickerPressKey(t, m, "up")
	assert.Equal(t, 0, m.Cursor())

	// Move to last item
	m = repoPickerPressKey(t, m, "down")
	m = repoPickerPressKey(t, m, "down")
	assert.Equal(t, 2, m.Cursor())

	// Cursor should not go below list length - 1
	m = repoPickerPressKey(t, m, "down")
	assert.Equal(t, 2, m.Cursor())
}

func TestRepoPicker_Search(t *testing.T) {
	m := NewRepoPickerModel("")
	m = loadProjects(t, m)

	// Type "alpha" to filter
	for _, ch := range "alpha" {
		m = repoPickerPressKey(t, m, string(ch))
	}

	assert.Equal(t, "alpha", m.Search())
	require.Len(t, m.Filtered(), 1)
	assert.Equal(t, "team/project-alpha", m.Filtered()[0].PathWithNamespace)
}

func TestRepoPicker_SearchClear(t *testing.T) {
	m := NewRepoPickerModel("")
	m = loadProjects(t, m)

	// Type some search
	for _, ch := range "beta" {
		m = repoPickerPressKey(t, m, string(ch))
	}
	require.Len(t, m.Filtered(), 1)

	// Clear search with Escape
	m = repoPickerPressKey(t, m, "escape")
	assert.Empty(t, m.Search())
	assert.Len(t, m.Filtered(), 3)
	assert.Equal(t, 0, m.Cursor())
}

func TestRepoPicker_SelectProject(t *testing.T) {
	m := NewRepoPickerModel("")
	m = loadProjects(t, m)

	// Move to second project and select
	m = repoPickerPressKey(t, m, "down")
	m = repoPickerPressKey(t, m, "enter")

	require.NotNil(t, m.Selected())
	assert.Equal(t, "team/project-beta", m.Selected().PathWithNamespace)
	assert.Equal(t, 2, m.Selected().ID)
}

func TestRepoPicker_AutoDetect(t *testing.T) {
	m := NewRepoPickerModel("team/project-beta")
	m = loadProjects(t, m)

	// Cursor should be at index 1 (team/project-beta)
	assert.Equal(t, 1, m.Cursor())
}

func TestRepoPicker_SearchBackspace(t *testing.T) {
	m := NewRepoPickerModel("")
	m = loadProjects(t, m)

	// Type "alph"
	for _, ch := range "alph" {
		m = repoPickerPressKey(t, m, string(ch))
	}
	assert.Equal(t, "alph", m.Search())
	require.Len(t, m.Filtered(), 1)

	// Backspace removes 'h'
	m = repoPickerPressKey(t, m, "backspace")
	assert.Equal(t, "alp", m.Search())
	require.Len(t, m.Filtered(), 1) // still matches "alpha"

	// Backspace again removes 'p'
	m = repoPickerPressKey(t, m, "backspace")
	assert.Equal(t, "al", m.Search())
	require.Len(t, m.Filtered(), 1) // still matches "alpha"

	// Backspace twice more to get to empty
	m = repoPickerPressKey(t, m, "backspace")
	m = repoPickerPressKey(t, m, "backspace")
	assert.Empty(t, m.Search())
	assert.Len(t, m.Filtered(), 3)
}
