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

func TestSetup_ViewShowsTokenCreationLink(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		wantURL  string
	}{
		{"bare host", "gitlab.example.com", "https://gitlab.example.com/-/user_settings/personal_access_tokens?name=glmt&scopes=api"},
		{"with https", "https://gitlab.example.com", "https://gitlab.example.com/-/user_settings/personal_access_tokens?name=glmt&scopes=api"},
		{"with http", "http://gitlab.example.com", "https://gitlab.example.com/-/user_settings/personal_access_tokens?name=glmt&scopes=api"},
		{"trailing slash", "gitlab.example.com/", "https://gitlab.example.com/-/user_settings/personal_access_tokens?name=glmt&scopes=api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m tea.Model = NewSetupModel()
			m = typeString(t, m, tt.host)
			m, _ = m.Update(specialKeyPress(tea.KeyEnter))

			sm := asSetup(t, m)
			view := sm.View()
			assert.Contains(t, view.Content, tt.wantURL)
		})
	}
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

// executeBatchCmd runs a command and, if it returns a BatchMsg, collects all
// messages by executing each sub-command. Returns all collected messages.
func executeBatchCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, c := range batch {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
		return msgs
	}
	return []tea.Msg{msg}
}

// findMsg searches a message slice for a message of type T.
func findMsg[T any](msgs []tea.Msg) (T, bool) {
	for _, msg := range msgs {
		if m, ok := msg.(T); ok {
			return m, true
		}
	}
	var zero T
	return zero, false
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

	msgs := executeBatchCmd(cmd)
	validMsg, ok := findMsg[credentialsValidMsg](msgs)
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

	msgs := executeBatchCmd(cmd)
	invalidMsg, ok := findMsg[credentialsInvalidMsg](msgs)
	require.True(t, ok, "command should return credentialsInvalidMsg")
	assert.EqualError(t, invalidMsg.err, "invalid token")
}

func TestSetup_GhostText(t *testing.T) {
	t.Run("shows full suggestion when empty", func(t *testing.T) {
		m := NewSetupModel()
		assert.Equal(t, "gitlab.com", m.hostGhostText())
	})

	t.Run("shows remaining suffix for matching prefix", func(t *testing.T) {
		m := NewSetupModel()
		m.host = "git"
		assert.Equal(t, "lab.com", m.hostGhostText())
	})

	t.Run("empty when input diverges", func(t *testing.T) {
		m := NewSetupModel()
		m.host = "https://"
		assert.Equal(t, "", m.hostGhostText())
	})

	t.Run("empty when input equals suggestion", func(t *testing.T) {
		m := NewSetupModel()
		m.host = "gitlab.com"
		assert.Equal(t, "", m.hostGhostText())
	})

	t.Run("right arrow at end accepts suggestion", func(t *testing.T) {
		var m tea.Model = NewSetupModel()
		m = typeString(t, m, "git")
		m, _ = m.Update(specialKeyPress(tea.KeyRight))

		sm := asSetup(t, m)
		assert.Equal(t, "gitlab.com", sm.Host())
		assert.Equal(t, len("gitlab.com"), sm.cursor)
	})

	t.Run("right arrow mid-input moves cursor", func(t *testing.T) {
		var m tea.Model = NewSetupModel()
		m = typeString(t, m, "git")
		// Move cursor to start, then press right — should move cursor, not accept
		m, _ = m.Update(specialKeyPress(tea.KeyHome))
		m, _ = m.Update(specialKeyPress(tea.KeyRight))

		sm := asSetup(t, m)
		assert.Equal(t, "git", sm.Host())
		assert.Equal(t, 1, sm.cursor)
	})

	t.Run("no ghost text after accepting", func(t *testing.T) {
		var m tea.Model = NewSetupModel()
		m, _ = m.Update(specialKeyPress(tea.KeyRight))

		sm := asSetup(t, m)
		assert.Equal(t, "gitlab.com", sm.Host())
		assert.Equal(t, "", sm.hostGhostText())
	})
}
