package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// SetupState represents the current state of the setup screen.
type SetupState int

const (
	SetupStateHost       SetupState = iota // Collecting GitLab host URL
	SetupStateToken                        // Collecting personal access token
	SetupStateValidating                   // Validating credentials via API
	SetupStateSuccess                      // Credentials validated successfully
	SetupStateError                        // Validation failed
)

// SetupModel is the bubbletea model for the first-run setup screen.
// It collects GitLab credentials and validates them.
type SetupModel struct {
	state        SetupState
	host         string
	token        string
	userName     string // set after successful validation
	err          error
	cursor       int // cursor position in current input
	spinnerFrame int

	// ValidateFn is a configurable function for credential validation.
	// It receives host and token and returns the authenticated user's name or an error.
	// This makes the model testable without real API calls.
	ValidateFn func(host, token string) (string, error)
}

// Messages used by the setup screen.

type credentialsValidMsg struct {
	userName string
}

type credentialsInvalidMsg struct {
	err error
}

// NewSetupModel creates a new SetupModel in its initial state.
func NewSetupModel() SetupModel {
	return SetupModel{
		state: SetupStateHost,
	}
}

// Init returns the initial command for the model.
func (m SetupModel) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates model state.
func (m SetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		if m.state == SetupStateValidating {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, spinnerTick()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	case tea.PasteMsg:
		return m.handlePaste(msg.Content), nil
	case credentialsValidMsg:
		m.state = SetupStateSuccess
		m.userName = msg.userName
		return m, nil
	case credentialsInvalidMsg:
		m.state = SetupStateError
		m.err = msg.err
		return m, nil
	}
	return m, nil
}

func (m SetupModel) handlePaste(content string) SetupModel {
	// Strip newlines/tabs from pasted content
	content = strings.ReplaceAll(content, "\n", "")
	content = strings.ReplaceAll(content, "\r", "")
	content = strings.ReplaceAll(content, "\t", "")
	switch m.state {
	case SetupStateHost:
		m.host = m.host[:m.cursor] + content + m.host[m.cursor:]
		m.cursor += len(content)
	case SetupStateToken:
		m.token = m.token[:m.cursor] + content + m.token[m.cursor:]
		m.cursor += len(content)
	}
	return m
}

func (m SetupModel) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.state {
	case SetupStateHost:
		return m.handleHostInput(msg, key)
	case SetupStateToken:
		return m.handleTokenInput(msg, key)
	case SetupStateError:
		// Any key press in error state goes back to host input for retry
		m.state = SetupStateHost
		m.host = ""
		m.token = ""
		m.cursor = 0
		m.err = nil
		return m, nil
	}
	return m, nil
}

func (m SetupModel) handleHostInput(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		if m.host != "" {
			m.state = SetupStateToken
			m.cursor = 0
		}
		return m, nil
	case "backspace":
		if m.cursor > 0 && m.cursor <= len(m.host) {
			m.host = m.host[:m.cursor-1] + m.host[m.cursor:]
			m.cursor--
		}
		return m, nil
	case "delete":
		if m.cursor < len(m.host) {
			m.host = m.host[:m.cursor] + m.host[m.cursor+1:]
		}
		return m, nil
	case "left":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "right":
		if m.cursor < len(m.host) {
			m.cursor++
		}
		return m, nil
	case "home", "ctrl+a":
		m.cursor = 0
		return m, nil
	case "end", "ctrl+e":
		m.cursor = len(m.host)
		return m, nil
	case "esc":
		return m, tea.Quit
	default:
		text := msg.Key().Text
		if text != "" {
			m.host = m.host[:m.cursor] + text + m.host[m.cursor:]
			m.cursor += len(text)
		}
		return m, nil
	}
}

func (m SetupModel) handleTokenInput(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		if m.token != "" {
			m.state = SetupStateValidating
			return m, tea.Batch(m.validateCmd(), spinnerTick())
		}
		return m, nil
	case "backspace":
		if m.cursor > 0 && m.cursor <= len(m.token) {
			m.token = m.token[:m.cursor-1] + m.token[m.cursor:]
			m.cursor--
		}
		return m, nil
	case "delete":
		if m.cursor < len(m.token) {
			m.token = m.token[:m.cursor] + m.token[m.cursor+1:]
		}
		return m, nil
	case "left":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "right":
		if m.cursor < len(m.token) {
			m.cursor++
		}
		return m, nil
	case "home", "ctrl+a":
		m.cursor = 0
		return m, nil
	case "end", "ctrl+e":
		m.cursor = len(m.token)
		return m, nil
	case "esc":
		m.state = SetupStateHost
		m.cursor = len(m.host)
		return m, nil
	default:
		text := msg.Key().Text
		if text != "" {
			m.token = m.token[:m.cursor] + text + m.token[m.cursor:]
			m.cursor += len(text)
		}
		return m, nil
	}
}

func (m SetupModel) validateCmd() tea.Cmd {
	host := m.host
	token := m.token
	validateFn := m.ValidateFn

	return func() tea.Msg {
		if validateFn == nil {
			return credentialsInvalidMsg{err: fmt.Errorf("no validation function configured")}
		}
		userName, err := validateFn(host, token)
		if err != nil {
			return credentialsInvalidMsg{err: err}
		}
		return credentialsValidMsg{userName: userName}
	}
}

// View renders the setup screen.
func (m SetupModel) View() tea.View {
	var b strings.Builder

	b.WriteString("\n  ")
	b.WriteString(sHeader.Styled("glmt - GitLab Merge Train CLI"))
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(sFaint.Styled("First-run setup"))
	b.WriteString("\n\n")

	var view tea.View

	switch m.state {
	case SetupStateHost:
		b.WriteString("  ")
		b.WriteString(sBold.Styled("GitLab host:"))
		b.WriteString(" ")
		b.WriteString(m.host)
		b.WriteString("\n")
		b.WriteString("\n  ")
		b.WriteString(sFaint.Styled(sKey.Styled("Enter")+" to continue. "+sKey.Styled("Escape")+" to quit."))
		b.WriteString("\n")

		// Cursor after host text: "  GitLab host: " is col 15 + cursor pos
		// Lines: 0=blank, 1=header, 2=blank, 3=subtitle, 4=blank, 5=host input
		view = tea.NewView(b.String())
		view.Cursor = tea.NewCursor(15+m.cursor, 5)
	case SetupStateToken:
		b.WriteString("  ")
		b.WriteString(sBold.Styled("GitLab host:"))
		b.WriteString(" ")
		b.WriteString(m.host)
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(sBold.Styled("Personal access token (api scope):"))
		b.WriteString(" ")
		b.WriteString(strings.Repeat("*", len(m.token)))
		b.WriteString("\n")
		b.WriteString("\n  ")
		b.WriteString(sFaint.Styled(sKey.Styled("Enter")+" to validate. "+sKey.Styled("Escape")+" to go back."))
		b.WriteString("\n")

		// Cursor after token text: "  Personal access token (api scope): " is col 37 + cursor pos
		// Lines: 0=blank, 1=header, 2=blank, 3=subtitle, 4=blank, 5=host, 6=token input
		view = tea.NewView(b.String())
		view.Cursor = tea.NewCursor(37+m.cursor, 6)
	case SetupStateValidating:
		b.WriteString("  ")
		b.WriteString(sBold.Styled("GitLab host:"))
		b.WriteString(" ")
		b.WriteString(m.host)
		b.WriteString("\n")
		b.WriteString("  ")
		b.WriteString(sBold.Styled("Personal access token (api scope):"))
		b.WriteString(" ")
		b.WriteString(strings.Repeat("*", len(m.token)))
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(sRunning.Styled(spinnerFrames[m.spinnerFrame]+" Validating credentials..."))
		b.WriteString("\n")

		view = tea.NewView(b.String())
	case SetupStateSuccess:
		b.WriteString("  ")
		b.WriteString(sBold.Styled("GitLab host:"))
		b.WriteString(" ")
		b.WriteString(m.host)
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(sSuccess.Styled("✓"))
		fmt.Fprintf(&b, " Authenticated as %s\n", m.userName)

		view = tea.NewView(b.String())
	case SetupStateError:
		b.WriteString("  ")
		b.WriteString(sBold.Styled("GitLab host:"))
		b.WriteString(" ")
		b.WriteString(m.host)
		b.WriteString("\n\n")
		b.WriteString("  ")
		b.WriteString(sError.Styled("✗"))
		fmt.Fprintf(&b, " Error: %s\n", m.err)
		b.WriteString("\n  ")
		b.WriteString(sFaint.Styled("Press any key to retry."))
		b.WriteString("\n")

		view = tea.NewView(b.String())
	default:
		view = tea.NewView(b.String())
	}

	return view
}

// Exported getters for testing.

// State returns the current setup state.
func (m SetupModel) State() SetupState { return m.state }

// Host returns the entered host value.
func (m SetupModel) Host() string { return m.host }

// Token returns the entered token value.
func (m SetupModel) Token() string { return m.token }

// UserName returns the authenticated user name (set after validation success).
func (m SetupModel) UserName() string { return m.userName }

// Err returns the validation error (set after validation failure).
func (m SetupModel) Err() error { return m.err }
