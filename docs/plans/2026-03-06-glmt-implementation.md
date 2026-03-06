# glmt Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement the full glmt CLI — a local interactive TUI that merges a sequence of GitLab MRs one by one.

**Architecture:** Layered Go application with clear seams: auth reader, config manager, GitLab API client behind an interface, train execution state machine, and bubbletea TUI screens. The GitLabClient interface is the critical seam enabling testability.

**Tech Stack:** Go 1.26, bubbletea + lipgloss (TUI), go-gitlab (API), go-toml (config), testify (assertions)

---

## Dependency Graph

```
Phase 0: Scaffolding (go.mod, dirs, interface, shared types)
    │
    ├── Phase 1a: internal/auth (independent)
    ├── Phase 1b: internal/config (independent)
    ├── Phase 1c: internal/gitlab client (depends on interface)
    └── Phase 1d: internal/train (depends on interface)
         │
         ├── Phase 2a: tui/setup (depends on auth, config)
         ├── Phase 2b: tui/repopicker (depends on gitlab interface)
         ├── Phase 2c: tui/mrlist (depends on gitlab interface)
         └── Phase 2d: tui/trainrun (depends on train)
              │
              └── Phase 3: cmd/glmt/main.go + integration + E2E
```

## Phase 0: Scaffolding

Create: go.mod, directory structure, GitLabClient interface, shared model types.
This unblocks all parallel work.

## Phase 1: Core Packages (4 parallel agents)

### Task 1a: internal/auth
- Parse glab config from ~/.config/glab-cli/
- Support single and multi-host configs
- Unit tests with testdata fixtures

### Task 1b: internal/config
- Load/save ~/.config/glmt/config.toml
- Default values, merge with file
- Unit tests

### Task 1c: internal/gitlab
- Concrete go-gitlab client implementing GitLabClient interface
- httptest-based tests for all methods
- testdata JSON fixtures

### Task 1d: internal/train
- Train runner state machine using GitLabClient interface
- Hand-written mock client
- Table-driven tests for all scenarios from DESIGN.md

## Phase 2: TUI Screens (4 parallel agents)

### Task 2a: tui/setup
- First-run credential setup screen
- bubbletea model with Update/View
- Tests on model state

### Task 2b: tui/repopicker
- Searchable project list, auto-detect from git remote
- Tests on model state

### Task 2c: tui/mrlist
- MR list with eligible/ineligible split, selection, reordering
- Full keybinding support
- Tests on model state

### Task 2d: tui/trainrun
- Live log display of train execution
- Tests on model state

## Phase 3: Integration

### Task 3a: cmd/glmt/main.go
- Wire all components together
- Screen flow management

### Task 3b: E2E tests
- Non-interactive mode flag
- Test against mock or real GitLab
