# `glmt` — Sequential Merge Queue for GitLab

A local TUI that queues and merges GitLab MRs one by one — a lightweight alternative to GitLab's merge trains.

[![Go](https://github.com/sairus2k/glmt/actions/workflows/ci.yml/badge.svg)](https://github.com/sairus2k/glmt/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/sairus2k/glmt)](https://goreportcard.com/report/github.com/sairus2k/glmt)

> **Disclaimer:** This project is fully vibe-coded. The author doesn't know Go
> and hasn't read the code. That said, it's used daily and solves a real need.

![glmt demo](demo.gif)

## Why

GitLab merge trains are a Premium/Ultimate feature that runs parallel
merged-result pipelines for queued MRs. On self-hosted GitLab Free, merging
multiple MRs sequentially — rebasing each onto an up-to-date target branch
and waiting for CI — is a tedious manual process.

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
3. **Press Enter** -- the merge queue runs. Each MR is rebased and merged
   directly. Progress is shown live.

## Configuration

`~/.config/glmt/config.toml`

```toml
[defaults]
repo = "myteam/myrepo"          # last-used project path

[behavior]
poll_rebase_interval_s = 2      # how often to check rebase status
poll_pipeline_interval_s = 10   # how often to check pipeline status
main_pipeline_timeout_m = 20    # how long to wait for the main pipeline to finish (minutes)
notify = "off"                  # terminal notification when train finishes: "off", "bel", "osc9"
```

| Value | Effect |
|-------|--------|
| `off` | No notification (default) |
| `bel` | Terminal bell (`\a`) — bounces dock icon, flashes taskbar, or plays a sound depending on terminal settings |
| `osc9` | Desktop notification with summary text (supported by Ghostty, Windows Terminal, ConEmu, and others) |

## How it works

For each selected MR, in order:

1. **Rebase** onto the target branch. If there is a conflict, skip the MR.
   The branch pipeline is skipped after rebase to avoid redundant CI.
2. **Merge** with a SHA guard to ensure the branch has not moved. On SHA
   mismatch, retry the rebase once.
3. After the last MR merges, **wait for the final main-branch pipeline** and
   display its status.

Skipped MRs do not abort the train. Already-merged MRs are never rolled back.

> **Tip:** To avoid wasting CI on intermediate main-branch commits, set
> `interruptible: true` on your CI jobs and enable **Auto-cancel redundant
> pipelines** in your GitLab project settings (CI/CD → General pipelines).
> GitLab will automatically cancel superseded pipelines when a new commit lands
> on the target branch.

## Alternatives

Several open-source tools implement sequential MR merging for GitLab Free tier.
If `glmt` doesn't fit your workflow, one of these might:

| | [marge-bot](https://github.com/smarkets/marge-bot) | [gitlab-merger-bot](https://github.com/pepakriz/gitlab-merger-bot) | [Gitlab-Merge-Train](https://github.com/LatidoHealthTech/Gitlab-Merge-Train) | **glmt** |
|---|---|---|---|---|
| Type | Daemon | Daemon + web UI | Daemon | Local CLI |
| Trigger | Assign MR to bot user | Label | Label | Interactive TUI |
| Queue | Assignment-based | Internal FIFO per repo | Label-based | User-selected, user-ordered |
| Deployment | Docker / PyPI | Docker + Helm | Docker | Single binary |
| Active | ✓ (community fork) | Stale since 2023 | Proof of concept | ✓ |

All of them run as a persistent service and react automatically to label changes
or MR assignments — a good fit for teams that want a fully hands-off pipeline.

`glmt` is different: it's a local CLI you run when you're ready to ship. No
server to deploy, no bot user to maintain, no labels to manage. You pick the
MRs, confirm, and watch it run.

## Requirements

- Go 1.21+
- [glab CLI](https://gitlab.com/gitlab-org/cli) configured, or a GitLab
  personal access token with `api` scope
- Self-hosted GitLab (any tier) or gitlab.com
- If your project requires pipelines to succeed before merging, enable
  **"Skipped pipelines are considered successful"** in Settings > Merge
  requests (GitLab 17.6+). glmt always skips the branch pipeline after
  rebase to avoid redundant CI.
