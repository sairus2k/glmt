package tui

import (
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

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinnerTickMsg time.Time

func spinnerTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}
