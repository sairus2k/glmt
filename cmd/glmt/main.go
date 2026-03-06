package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/emdash-ai/glmt/internal/auth"
	"github.com/emdash-ai/glmt/internal/config"
	"github.com/emdash-ai/glmt/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load config
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Try to read existing glab credentials
	var creds *auth.Credentials
	glabDir := auth.DefaultConfigDir()
	host := cfg.GitLab.Host
	c, err := auth.ReadCredentials(glabDir, host)
	if err == nil {
		creds = c
	}

	// Start TUI
	model := tui.NewAppModel(creds, cfg, cfgPath)
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}
