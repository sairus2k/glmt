package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/auth"
	"github.com/sairus2k/glmt/internal/config"
	"github.com/sairus2k/glmt/internal/gitlab"
	glmtlog "github.com/sairus2k/glmt/internal/log"
	"github.com/sairus2k/glmt/internal/notify"
	"github.com/sairus2k/glmt/internal/train"
	"github.com/sairus2k/glmt/internal/tui"
)

var version = "dev"

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
	demoMode := flag.Bool("demo", false, "Run with mock data for demo recording")
	host := flag.String("host", "", "GitLab host (e.g. gitlab.example.com)")
	token := flag.String("token", "", "Personal access token")
	projectID := flag.Int("project-id", 0, "GitLab project ID")
	mrs := flag.String("mrs", "", "Comma-separated list of MR IIDs to merge (e.g. 42,38,35)")
	enableLog := flag.Bool("log", false, "Write JSON Lines log file to ~/.local/state/glmt/")
	flag.Parse()

	if *demoMode {
		return runDemo()
	}

	if *nonInteractive {
		return runNonInteractive(*host, *token, *projectID, *mrs, *enableLog)
	}

	return runTUI(*host, *token, *projectID, *enableLog)
}

func runNonInteractive(host, token string, projectID int, mrsFlag string, enableLog bool) error {
	cfg, host, token, mrIIDs, err := prepareNonInteractive(host, token, projectID, mrsFlag)
	if err != nil {
		return err
	}

	client, err := gitlab.NewAPIClient(host, token)
	if err != nil {
		return fmt.Errorf("creating GitLab client: %w", err)
	}

	return runTrain(client, projectID, mrIIDs, cfg, enableLog, token)
}

// prepareNonInteractive loads config, applies flag overrides, validates required
// fields, and parses MR IIDs. Returns values ready for client creation and runTrain.
func prepareNonInteractive(host, token string, projectID int, mrsFlag string) (*config.Config, string, string, []int, error) {
	cfgPath := configPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		cfg = config.DefaultConfig()
	}

	// Fall back to saved config for host/token if not provided via flags
	if host == "" {
		host = cfg.GitLab.Host
	}
	if token == "" {
		token = cfg.GitLab.Token
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
		return nil, "", "", nil, fmt.Errorf("non-interactive mode requires flags: %s", strings.Join(missing, ", "))
	}

	mrIIDs, err := parseMRIIDs(mrsFlag)
	if err != nil {
		return nil, "", "", nil, err
	}

	return cfg, host, token, mrIIDs, nil
}

// runTrain fetches MRs, executes the merge train, and prints results.
// This is the testable core of non-interactive mode — it accepts a gitlab.Client
// so tests can inject a mock.
func runTrain(client gitlab.Client, projectID int, mrIIDs []int, cfg *config.Config, enableLog bool, token string) error {
	// Set up file logging — wraps client BEFORE any API calls
	fileLogger := setupFileLogger(enableLog, token)
	apiClient := client
	if fileLogger != nil {
		defer fileLogger.Close()
		fileLogger.LogSession()
		apiClient = glmtlog.NewLoggingClient(client, fileLogger)
	}

	ctx := context.Background()

	// Fetch MR objects — these API calls are now logged via apiClient
	mrs := make([]*gitlab.MergeRequest, 0, len(mrIIDs))
	for _, iid := range mrIIDs {
		mr, err := apiClient.GetMergeRequest(ctx, projectID, iid)
		if err != nil {
			return fmt.Errorf("fetching MR !%d: %w", iid, err)
		}
		mrs = append(mrs, mr)
	}

	// Create logger (composite if file logging enabled)
	logger := buildLogFunc(fileLogger)

	// Log train meta before run
	if fileLogger != nil {
		fileLogger.LogMeta(projectID, mrIIDs)
	}

	trainStart := time.Now()
	runner := train.NewRunner(apiClient, projectID, logger)
	runner.PollRebaseInterval = time.Duration(cfg.Behavior.PollRebaseIntervalS) * time.Second
	runner.PollPipelineInterval = time.Duration(cfg.Behavior.PollPipelineIntervalS) * time.Second
	if cfg.Behavior.PollPipelineIntervalS > 0 {
		runner.MaxMainPipelineAttempts = cfg.Behavior.MainPipelineTimeoutM * 60 / cfg.Behavior.PollPipelineIntervalS
	}
	result, err := runner.Run(ctx, mrs)

	// Log run end (before error check — partial results are still logged)
	if fileLogger != nil {
		merged, skipped, pending := countResults(result)
		pipelineStatus := ""
		if result != nil {
			pipelineStatus = result.MainPipelineStatus
		}
		fileLogger.LogRunEnd(merged, skipped, pending, pipelineStatus, time.Since(trainStart))
	}

	if err != nil {
		return fmt.Errorf("running train: %w", err)
	}

	merged, skipped, _ := countResults(result)
	notify.Send(cfg.Behavior.Notify, notify.FormatMessage(merged, skipped, result.MainPipelineStatus))

	return printTrainResults(result)
}

// parseMRIIDs parses a comma-separated list of MR IIDs.
func parseMRIIDs(mrsFlag string) ([]int, error) {
	parts := strings.Split(mrsFlag, ",")
	mrIIDs := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		iid, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid MR IID %q: %w", p, err)
		}
		if iid <= 0 {
			return nil, fmt.Errorf("invalid MR IID %q: must be a positive integer", p)
		}
		mrIIDs = append(mrIIDs, iid)
	}
	if len(mrIIDs) == 0 {
		return nil, fmt.Errorf("no valid MR IIDs provided")
	}
	return mrIIDs, nil
}

// setupFileLogger creates a file logger if enabled, printing a warning on failure.
func setupFileLogger(enabled bool, token string) *glmtlog.FileLogger {
	if !enabled {
		return nil
	}
	fl, err := glmtlog.NewFileLogger(glmtlog.DefaultStateDir(), token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create log file: %v\n", err)
		return nil
	}
	return fl
}

// buildLogFunc creates a train log function, optionally writing to a file logger.
func buildLogFunc(fileLogger *glmtlog.FileLogger) func(int, string, string) {
	logger := func(mrIID int, step string, message string) {
		if mrIID > 0 {
			fmt.Printf("[MR !%d] [%s] %s\n", mrIID, step, message)
		} else {
			fmt.Printf("[%s] %s\n", step, message)
		}
	}
	if fileLogger != nil {
		origLogger := logger
		logger = func(mrIID int, step string, message string) {
			origLogger(mrIID, step, message)
			fileLogger.LogStep(mrIID, step, message)
		}
	}
	return logger
}

// printTrainResults prints the train results and returns an error if not all MRs were merged.
func printTrainResults(result *train.Result) error {
	if result == nil {
		return fmt.Errorf("no results to display")
	}
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
		return fmt.Errorf("not all MRs were merged")
	}

	return nil
}

func countResults(result *train.Result) (merged, skipped, pending int) {
	if result == nil {
		return
	}
	for _, mr := range result.MRResults {
		switch mr.Status {
		case train.MRStatusMerged:
			merged++
		case train.MRStatusSkipped:
			skipped++
		default:
			pending++
		}
	}
	return
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

func runTUI(flagHost, flagToken string, flagProjectID int, enableLog bool) error {
	// Load config
	cfgPath := configPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	creds := resolveCredentials(cfg, flagHost, flagToken)

	// Set up file logging — wraps client BEFORE AppModel creation
	var fileLogger *glmtlog.FileLogger
	if enableLog && creds == nil {
		fmt.Fprintf(os.Stderr, "Warning: --log requires pre-existing credentials (config or flags), logging disabled\n")
	} else if enableLog {
		fileLogger = setupFileLogger(true, creds.Token)
		if fileLogger != nil {
			defer fileLogger.Close()
			fileLogger.LogSession()
		}
	}

	// Start TUI
	model := tui.NewAppModel(creds, cfg, cfgPath, flagProjectID, version)
	model.FileLogger = fileLogger

	// Wrap m.client in LoggingClient so ALL API calls are logged
	if fileLogger != nil && model.Client() != nil {
		model.SetClient(glmtlog.NewLoggingClient(model.Client(), fileLogger))
	}

	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}

	return nil
}

// resolveCredentials resolves credentials from glab config or CLI flags.
func resolveCredentials(cfg *config.Config, flagHost, flagToken string) *auth.Credentials {
	effectiveHost := cfg.GitLab.Host
	effectiveToken := cfg.GitLab.Token
	if flagHost != "" {
		effectiveHost = flagHost
	}
	if flagToken != "" {
		effectiveToken = flagToken
	}

	glabDir := auth.DefaultConfigDir()
	c, err := auth.ReadCredentials(glabDir, effectiveHost)
	if err == nil {
		// Persist glab-discovered host only (not CLI-provided host)
		if flagHost == "" {
			cfg.GitLab.Host = c.Host
		}
		return c
	}

	if effectiveHost != "" && effectiveToken != "" {
		return &auth.Credentials{
			Host:     effectiveHost,
			Token:    effectiveToken,
			Protocol: "https",
		}
	}

	return nil
}
