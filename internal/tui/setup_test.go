package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func keyPress(char string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: -1, Text: char})
}

func specialKeyPress(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func typeString(t *testing.T, m tea.Model, s string) tea.Model {
	t.Helper()
	var cmd tea.Cmd
	for _, ch := range s {
		m, cmd = m.Update(keyPress(string(ch)))
		require.Nil(t, cmd)
	}
	return m
}

func asSetup(t *testing.T, m tea.Model) SetupModel {
	t.Helper()
	sm, ok := m.(SetupModel)
	require.True(t, ok, "expected SetupModel")
	return sm
}

func TestSetup_InitialState(t *testing.T) {
	m := NewSetupModel()
	assert.Equal(t, SetupStateHost, m.State())
	assert.Empty(t, m.Host())
	assert.Empty(t, m.Token())
	assert.Empty(t, m.UserName())
	assert.Nil(t, m.Err())
}

func TestSetup_TypeHost(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m = typeString(t, m, "gitlab.example.com")

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateHost, sm.State())
	assert.Equal(t, "gitlab.example.com", sm.Host())
}

func TestSetup_EnterHost(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateToken, sm.State())
	assert.Equal(t, "gitlab.example.com", sm.Host())
}

func TestSetup_EnterHost_EmptyDoesNotAdvance(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateHost, sm.State())
}

func TestSetup_TypeToken(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))
	m = typeString(t, m, "glpat-abc123")

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateToken, sm.State())
	assert.Equal(t, "glpat-abc123", sm.Token())
}

func TestSetup_EnterToken(t *testing.T) {
	sm := NewSetupModel()
	sm.ValidateFn = func(host, token string) (string, error) {
		return "testuser", nil
	}
	var m tea.Model = sm

	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))
	m = typeString(t, m, "glpat-abc123")

	var cmd tea.Cmd
	m, cmd = m.Update(specialKeyPress(tea.KeyEnter))

	sm2 := asSetup(t, m)
	assert.Equal(t, SetupStateValidating, sm2.State())
	require.NotNil(t, cmd, "entering token should return a validation command")
}

func TestSetup_EnterToken_EmptyDoesNotAdvance(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))

	m, cmd := m.Update(specialKeyPress(tea.KeyEnter))
	require.Nil(t, cmd)

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateToken, sm.State())
}

func TestSetup_ValidationSuccess(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))
	m, _ = m.Update(credentialsValidMsg{userName: "Ada Lovelace"})

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateSuccess, sm.State())
	assert.Equal(t, "Ada Lovelace", sm.UserName())
}

func TestSetup_ValidationError(t *testing.T) {
	var m tea.Model = NewSetupModel()
	expectedErr := fmt.Errorf("401 unauthorized")
	m, _ = m.Update(credentialsInvalidMsg{err: expectedErr})

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateError, sm.State())
	assert.Equal(t, expectedErr, sm.Err())
}

func TestSetup_Backspace(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m = typeString(t, m, "gitlab.example.comm")
	m, _ = m.Update(specialKeyPress(tea.KeyBackspace))

	sm := asSetup(t, m)
	assert.Equal(t, "gitlab.example.com", sm.Host())

	m, _ = m.Update(specialKeyPress(tea.KeyEnter))
	m = typeString(t, m, "glpat-abc123x")
	m, _ = m.Update(specialKeyPress(tea.KeyBackspace))

	sm = asSetup(t, m)
	assert.Equal(t, "glpat-abc123", sm.Token())
}

func TestSetup_Backspace_EmptyInput(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m, _ = m.Update(specialKeyPress(tea.KeyBackspace))

	sm := asSetup(t, m)
	assert.Equal(t, "", sm.Host())
	assert.Equal(t, SetupStateHost, sm.State())
}

func TestSetup_EscapeGoesBack(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateToken, sm.State())

	m, _ = m.Update(specialKeyPress(tea.KeyEscape))

	sm = asSetup(t, m)
	assert.Equal(t, SetupStateHost, sm.State())
	assert.Equal(t, "gitlab.example.com", sm.Host())
}

func TestSetup_EscapeInHostState(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m, cmd := m.Update(specialKeyPress(tea.KeyEscape))

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateHost, sm.State())
	require.NotNil(t, cmd, "esc in host state should quit")
}

func TestSetup_CtrlCQuitsAnyState(t *testing.T) {
	ctrlC := tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})

	// Host state
	var m tea.Model = NewSetupModel()
	_, cmd := m.Update(ctrlC)
	require.NotNil(t, cmd, "ctrl+c in host state should quit")

	// Token state
	m = NewSetupModel()
	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))
	_, cmd = m.Update(ctrlC)
	require.NotNil(t, cmd, "ctrl+c in token state should quit")
}

func TestSetup_ViewShowsAsterisks(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))
	m = typeString(t, m, "secret-token")

	sm := asSetup(t, m)
	view := sm.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, "************")
	assert.NotContains(t, viewStr, "secret-token")
}

func TestSetup_ErrorRetry(t *testing.T) {
	var m tea.Model = NewSetupModel()
	m, _ = m.Update(credentialsInvalidMsg{err: fmt.Errorf("connection refused")})

	sm := asSetup(t, m)
	assert.Equal(t, SetupStateError, sm.State())

	m, _ = m.Update(keyPress("r"))

	sm = asSetup(t, m)
	assert.Equal(t, SetupStateHost, sm.State())
	assert.Empty(t, sm.Host())
	assert.Empty(t, sm.Token())
	assert.Nil(t, sm.Err())
}

func TestSetup_ValidationCommand(t *testing.T) {
	sm := NewSetupModel()
	sm.ValidateFn = func(host, token string) (string, error) {
		assert.Equal(t, "gitlab.example.com", host)
		assert.Equal(t, "my-token", token)
		return "Test User", nil
	}
	var m tea.Model = sm

	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))
	m = typeString(t, m, "my-token")

	var cmd tea.Cmd
	_, cmd = m.Update(specialKeyPress(tea.KeyEnter))
	require.NotNil(t, cmd)

	msg := cmd()
	validMsg, ok := msg.(credentialsValidMsg)
	require.True(t, ok, "command should return credentialsValidMsg")
	assert.Equal(t, "Test User", validMsg.userName)
}

func TestSetup_ValidationCommandError(t *testing.T) {
	sm := NewSetupModel()
	sm.ValidateFn = func(host, token string) (string, error) {
		return "", fmt.Errorf("invalid token")
	}
	var m tea.Model = sm

	m = typeString(t, m, "gitlab.example.com")
	m, _ = m.Update(specialKeyPress(tea.KeyEnter))
	m = typeString(t, m, "bad-token")

	var cmd tea.Cmd
	_, cmd = m.Update(specialKeyPress(tea.KeyEnter))
	require.NotNil(t, cmd)

	msg := cmd()
	invalidMsg, ok := msg.(credentialsInvalidMsg)
	require.True(t, ok, "command should return credentialsInvalidMsg")
	assert.EqualError(t, invalidMsg.err, "invalid token")
}
