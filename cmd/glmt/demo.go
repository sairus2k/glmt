package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/auth"
	"github.com/sairus2k/glmt/internal/config"
	"github.com/sairus2k/glmt/internal/demo"
	"github.com/sairus2k/glmt/internal/tui"
)

func runDemo() error {
	creds := &auth.Credentials{
		Host:     "https://gitlab.example.com",
		Token:    "demo-token",
		Protocol: "https",
	}

	cfg := config.DefaultConfig()
	cfg.Behavior.PollRebaseIntervalS = 1
	cfg.Behavior.PollPipelineIntervalS = 1

	model := tui.NewAppModel(creds, cfg, os.DevNull, 0, version)
	model.SetClient(demo.NewClient())

	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running demo TUI: %w", err)
	}
	return nil
}
