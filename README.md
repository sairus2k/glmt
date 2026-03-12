# `glmt` — GitLab Merge Train CLI

A local interactive TUI that merges a sequence of GitLab MRs one by one.

[![Go](https://github.com/sairus2k/glmt/actions/workflows/ci.yml/badge.svg)](https://github.com/sairus2k/glmt/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/sairus2k/glmt)](https://goreportcard.com/report/github.com/sairus2k/glmt)

## Why

GitLab merge trains are a Premium/Ultimate feature. On self-hosted GitLab Free,
merging multiple MRs sequentially — rebasing each onto an up-to-date target
branch and waiting for CI — is a tedious manual process.

`glmt` automates this. Run it, pick the MRs you want to ship, and watch them
merge in order. No server, no labels, no webhooks. Close the terminal when it
is done.

## Install

### Homebrew

```
brew tap sairus2k/tap
brew install glmt
```

### Go

```
go install github.com/sairus2k/glmt/cmd/glmt@latest
```

`glmt` reads credentials from the [glab CLI](https://gitlab.com/gitlab-org/cli)
config (`~/.config/glab-cli/`). If you have `glab` configured, no additional
auth setup is needed. On first run without `glab` credentials, `glmt` will
prompt for your GitLab host and personal access token.

## Usage

```
glmt
```

1. **Select a repository** -- auto-detected from the current directory, or
   picked from a searchable list. Remembered for next time.
2. **Select MRs** -- all open MRs are shown. Eligible MRs (approved, pipeline
   passed, no conflicts, discussions resolved) can be toggled for merging.
   Ineligible MRs are shown grayed out with a reason. Reorder with
   `Shift+Up/Down`.
3. **Press Enter** -- the merge train runs. Each MR is rebased, its pipeline is
   awaited, and it is merged. Progress is shown live.

## Configuration

`~/.config/glmt/config.toml`

```toml
[defaults]
repo = "myteam/myrepo"          # last-used project path

[behavior]
skip_ci = true                  # skip the branch pipeline after rebase (useful with merge trains)
poll_rebase_interval_s = 2      # how often to check rebase status
poll_pipeline_interval_s = 10   # how often to check pipeline status
```

## How it works

For each selected MR, in order:

1. **Rebase** onto the target branch. If there is a conflict, skip the MR.
2. **Wait for the pipeline** to pass. If it fails, skip the MR.
3. **Merge** with a SHA guard to ensure the branch has not moved. On SHA
   mismatch, retry the rebase once.
4. **Cancel the main-branch pipeline** triggered by the merge (if more MRs
   remain) to avoid wasting CI on intermediate states.
5. After the last MR, let the final main pipeline run and display its status.
   If the last MR was skipped but earlier MRs merged, restart the cancelled
   main pipeline so CI runs against the actual final state.

Skipped MRs do not abort the train. Already-merged MRs are never rolled back.

## Requirements

- Go 1.21+
- [glab CLI](https://gitlab.com/gitlab-org/cli) configured, or a GitLab
  personal access token with `api` scope
- Self-hosted GitLab (any tier) or gitlab.com
