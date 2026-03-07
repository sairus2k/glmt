package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	tea "charm.land/bubbletea/v2"
)

var (
	sHeader   = ansi.NewStyle(ansi.AttrBold, ansi.AttrBrightWhiteForegroundColor)
	sSuccess  = ansi.NewStyle(ansi.AttrGreenForegroundColor)
	sError    = ansi.NewStyle(ansi.AttrRedForegroundColor)
	sWarning  = ansi.NewStyle(ansi.AttrYellowForegroundColor)
	sRunning  = ansi.NewStyle(ansi.AttrCyanForegroundColor)
	sFaint    = ansi.NewStyle(ansi.AttrFaint)
	sSelected = ansi.NewStyle(ansi.AttrBold, ansi.AttrBrightCyanForegroundColor)
	sCursor   = ansi.NewStyle(ansi.AttrBold, ansi.AttrBrightMagentaForegroundColor)
	sDim      = ansi.NewStyle(ansi.AttrBrightBlackForegroundColor)
	sKey      = ansi.NewStyle(ansi.AttrBold)
	sBold     = ansi.NewStyle(ansi.AttrBold)
)

// KeyHint represents a keyboard hint shown in the footer.
type KeyHint struct {
	Key  string // e.g. "[Space]", "[Enter]", "[Esc]"
	Desc string // e.g. "toggle", "start", "quit"
}

// renderFooter renders the footer line with keyboard hints on the left and login status on the right.
func renderFooter(hints []KeyHint, loginStatus string, width int) string {
	var parts []string
	for _, h := range hints {
		parts = append(parts, sKey.Styled(h.Key)+" "+h.Desc)
	}
	left := "  " + sFaint.Styled(strings.Join(parts, "  "))
	right := sFaint.Styled(loginStatus)

	// Calculate visible widths (strip ANSI escape sequences)
	leftLen := ansi.StringWidth(left)
	rightLen := ansi.StringWidth(right)

	gap := width - leftLen - rightLen
	if gap < 2 {
		gap = 2
	}
	return left + strings.Repeat(" ", gap) + right
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinnerTickMsg time.Time

func spinnerTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}
