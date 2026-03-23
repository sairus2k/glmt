# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What is glmt

GitLab Merge Train CLI — a local interactive TUI that merges a sequence of GitLab MRs one by one, rebasing each onto the freshly updated target branch and managing intermediate CI pipelines. Built for self-hosted GitLab Free where merge trains are not available.

## Commands

```bash
# Build
go build -o glmt ./cmd/glmt/

# Lint (CI rejects failures)
golangci-lint run ./...

# Unit tests
go test ./...

# Single package
go test -v ./internal/train/...

# E2E tests (requires Docker, not part of default test run)
go test -v -tags e2e -count=1 ./e2e/...

# Run
./glmt                            # Interactive TUI
./glmt -non-interactive -host <host> -token <token> -project-id <id> -mrs <iids>
./glmt logout
```

## Architecture

**Entry point:** `cmd/glmt/main.go` — CLI flag parsing, routes to TUI or non-interactive mode.

**Core packages under `internal/`:**

- **`gitlab/`** — `Client` interface (`interface.go`) is the primary abstraction seam. `APIClient` in `client.go` wraps the go-gitlab library. All other packages depend on the interface, never on the concrete client.
- **`train/`** — `Runner` executes the merge train state machine: rebase → poll pipeline → merge with SHA guard → cancel/restart intermediate pipelines. Hand-written mock in `mock_client.go`.
- **`tui/`** — Bubble Tea v2 app. `AppModel` (`app.go`) routes between screens: Setup → RepoPicker → MRList → TrainRun. Async ops use Bubble Tea commands; state transitions use typed messages.
- **`auth/`** — Reads credentials from glab CLI config (`~/.config/glab-cli/config.yml`), falls back to glmt config.
- **`config/`** — TOML config at `~/.config/glmt/config.toml`.

**Key flow:** User selects MRs in TUI → `train.Runner.Run()` processes them sequentially → each MR: rebase, wait for pipeline (poll 10s), merge with SHA guard (retry once on 409), cancel intermediate main pipeline.

## Testing Conventions

- 5-layer strategy: auth/config unit → gitlab httptest → train runner with mock → TUI model state → E2E with real GitLab container
- Tests use `testify` for assertions
- TUI tests assert model state after `Update()`, not rendered output (no snapshot tests)
- Train runner tests are table-driven with a recording mock client
- E2E tests use testcontainers-go to spin up GitLab CE; guarded by `e2e` build tag
- Test fixtures live in `testdata/` directories alongside the code

## Code Style

- `golangci-lint` v2 is used for linting (includes gofmt, govet, staticcheck, and others)
- Minimal abstractions — avoid unnecessary indirection
- Errors wrapped with context: `fmt.Errorf("doing thing: %w", err)`
- All API methods accept `context.Context`
- Logger is a callback type `func(mrIID int, step string, message string)`, not a global
