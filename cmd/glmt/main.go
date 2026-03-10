package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/auth"
	"github.com/sairus2k/glmt/internal/config"
	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/sairus2k/glmt/internal/train"
	"github.com/sairus2k/glmt/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "logout" {
		return runLogout()
	}

	nonInteractive := flag.Bool("non-interactive", false, "Skip TUI, run train directly")
	host := flag.String("host", "", "GitLab host (e.g. gitlab.example.com)")
	token := flag.String("token", "", "Personal access token")
	projectID := flag.Int("project-id", 0, "GitLab project ID")
	mrs := flag.String("mrs", "", "Comma-separated list of MR IIDs to merge (e.g. 42,38,35)")
	flag.Parse()

	if *nonInteractive {
		return runNonInteractive(*host, *token, *projectID, *mrs)
	}

	return runTUI(*host, *token, *projectID)
}

func runNonInteractive(host, token string, projectID int, mrsFlag string) error {
	// Fall back to saved config for host/token if not provided via flags
	if host == "" || token == "" {
		cfgPath := configPath()
		cfg, err := config.Load(cfgPath)
		if err == nil {
			if host == "" {
				host = cfg.GitLab.Host
			}
			if token == "" {
				token = cfg.GitLab.Token
			}
		}
	}

	// Validate required flags
	var missing []string
	if host == "" {
		missing = append(missing, "--host")
	}
	if token == "" {
		missing = append(missing, "--token")
	}
	if projectID == 0 {
		missing = append(missing, "--project-id")
	}
	if mrsFlag == "" {
		missing = append(missing, "--mrs")
	}
	if len(missing) > 0 {
		return fmt.Errorf("non-interactive mode requires flags: %s", strings.Join(missing, ", "))
	}

	// Parse MR IIDs
	parts := strings.Split(mrsFlag, ",")
	mrIIDs := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		iid, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("invalid MR IID %q: %w", p, err)
		}
		mrIIDs = append(mrIIDs, iid)
	}
	if len(mrIIDs) == 0 {
		return fmt.Errorf("no valid MR IIDs provided")
	}

	// Create GitLab client
	client, err := gitlab.NewAPIClient(host, token)
	if err != nil {
		return fmt.Errorf("creating GitLab client: %w", err)
	}

	ctx := context.Background()

	// Fetch MR objects
	mrs := make([]*gitlab.MergeRequest, 0, len(mrIIDs))
	for _, iid := range mrIIDs {
		mr, err := client.GetMergeRequest(ctx, projectID, iid)
		if err != nil {
			return fmt.Errorf("fetching MR !%d: %w", iid, err)
		}
		mrs = append(mrs, mr)
	}

	// Create logger
	logger := func(mrIID int, step string, message string) {
		if mrIID > 0 {
			fmt.Printf("[MR !%d] [%s] %s\n", mrIID, step, message)
		} else {
			fmt.Printf("[%s] %s\n", step, message)
		}
	}

	// Run the train
	runner := train.NewRunner(client, projectID, logger)
	result, err := runner.Run(ctx, mrs)
	if err != nil {
		return fmt.Errorf("running train: %w", err)
	}

	// Print results
	fmt.Println()
	fmt.Println("=== Train Results ===")
	allMerged := true
	for _, mrResult := range result.MRResults {
		switch mrResult.Status {
		case train.MRStatusMerged:
			fmt.Printf("  MR !%d: merged\n", mrResult.MR.IID)
		case train.MRStatusSkipped:
			fmt.Printf("  MR !%d: skipped (%s)\n", mrResult.MR.IID, mrResult.SkipReason)
			allMerged = false
		default:
			fmt.Printf("  MR !%d: pending\n", mrResult.MR.IID)
			allMerged = false
		}
	}

	if result.MainPipelineStatus != "" {
		fmt.Printf("\nMain pipeline: %s (%s)\n", result.MainPipelineStatus, result.MainPipelineURL)
	}

	if !allMerged {
		os.Exit(1)
	}

	return nil
}

func runLogout() error {
	return logout(configPath(), auth.DefaultConfigDir())
}

func logout(cfgPath, glabConfigDir string) error {
	err := os.Remove(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing config: %w", err)
	}
	fmt.Printf("Logged out. Config removed: %s\n", cfgPath)

	// Check if glab credentials still exist
	if _, err := auth.ReadCredentials(glabConfigDir, ""); err == nil {
		fmt.Println("Note: glab CLI credentials still exist at ~/.config/glab-cli/config.yml")
	}

	return nil
}

// configPath returns the config file path, respecting the GLMT_CONFIG env var.
func configPath() string {
	if p := os.Getenv("GLMT_CONFIG"); p != "" {
		return p
	}
	return config.DefaultPath()
}

func runTUI(flagHost, flagToken string, flagProjectID int) error {
	// Load config
	cfgPath := configPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Override config with CLI flags
	if flagHost != "" {
		cfg.GitLab.Host = flagHost
	}
	if flagToken != "" {
		cfg.GitLab.Token = flagToken
	}
	if flagProjectID != 0 {
		cfg.Defaults.ProjectID = flagProjectID
	}

	// Try to read existing credentials: glab config first, then glmt config
	var creds *auth.Credentials
	glabDir := auth.DefaultConfigDir()
	host := cfg.GitLab.Host
	c, err := auth.ReadCredentials(glabDir, host)
	if err == nil {
		creds = c
	} else if cfg.GitLab.Host != "" && cfg.GitLab.Token != "" {
		creds = &auth.Credentials{
			Host:     cfg.GitLab.Host,
			Token:    cfg.GitLab.Token,
			Protocol: "https",
		}
	}

	// Start TUI
	model := tui.NewAppModel(creds, cfg, cfgPath)
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}
