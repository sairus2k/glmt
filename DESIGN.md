# `glmt` — GitLab Merge Train CLI

> A local interactive TUI that merges a sequence of GitLab MRs one by one,
> rebasing each onto the freshly updated target branch and managing intermediate
> CI pipelines — for self-hosted GitLab Free where merge trains are not available.

---

## Goals

- Minimal user interaction: pick MRs once, then watch it run
- No server component, no labels, no webhooks — pure CLI
- Works against any self-hosted GitLab Free instance

---

## Prior art

Several tools solve the same underlying problem — sequential MR merging with
rebase on GitLab Free. None of them fit the use case `glmt` targets.

| | marge-bot | gitlab-merger-bot | Gitlab-Merge-Train | **glmt** |
|---|---|---|---|---|
| Type | Daemon / service | Daemon + web UI | Daemon | Local CLI |
| Deployment | Docker / PyPI | Docker + Helm | Docker | Single binary |
| Trigger | Assign MR to bot user | Label | Label | Interactive TUI |
| Queue management | Assignment-based | Internal FIFO per repo | Label-based | User-selected, user-ordered |
| MR selection | Automatic (all assigned) | Automatic (all labelled) | Automatic (all labelled) | Manual, per-run |
| Requires server | Yes | Yes | Yes | No |
| Language | Python | TypeScript | TypeScript | Go |
| Active | Yes (community fork) | Stale since 2023 | Proof of concept | — |

**The core difference** is the deployment model and interaction style. Every
existing tool is designed to run continuously as a service, reacting to label
changes or MR assignments. They solve the problem for teams that want a
fully automated, hands-off pipeline.

`glmt` is for a developer who wants to sit down, pick exactly which MRs to
ship right now, in what order, and watch it happen — then close the terminal.
No infrastructure, no labels to manage, no bot user to maintain. The workflow
is closer to running `git push` than to configuring a CI system.

This also means `glmt` makes different tradeoffs: it has no persistence across
runs, no webhook integration, and no multi-user coordination. It is intentionally
single-user and single-session.

---

## Non-goals

- Stacked branches / MRs targeting other MRs (not main)
- Multi-project support (single repo at a time)
- Persistent queue across invocations
- Slack / notification integrations

---

## Stack

| Concern | Choice | Reason |
|---|---|---|
| Language | Go | Single binary, good TUI ecosystem |
| TUI | bubbletea + lipgloss | De-facto standard for Go TUIs |
| GitLab API | `go-gitlab` library | Direct API, no subprocess overhead |
| Auth storage | Read from `glab` config (`~/.config/glab-cli/`) | Reuse existing credentials, no re-auth |
| Config | `~/.config/glmt/config.toml` | Last-used repo, preferences |

The tool shells out to **nothing at runtime** — all operations go through the
`go-gitlab` REST client using the token stored by `glab`.

---

## Screens

```
┌─────────────────────────────────────────────────────────┐
│  1. First-run setup   (only if no glab credentials)     │
│  2. Repo picker                                         │
│  3. MR list + selection                                 │
│  4. Train run (live log)                                │
└─────────────────────────────────────────────────────────┘
```

### 1. First-run setup

Shown only if no credentials are found in the `glab` config.

- Prompt: GitLab host (e.g. `gitlab.example.com`)
- Prompt: Personal access token (scopes needed: `api`)
- Validate by calling `GET /user`, show name on success
- Store in `~/.config/glab-cli/` in glab-compatible format

Subsequent runs skip this screen entirely.

---

### 2. Repo picker

Shown on first run only (or when explicitly triggered from the MR list screen).

- Auto-detect current repo from `git remote get-url origin` in cwd
- If detected, pre-select it in the list
- Show searchable list of user's accessible projects
- Selection is persisted as the new default; subsequent runs go straight to screen 3

---

### 3. MR list + selection

All open MRs for the selected repo, split into two groups:

**Selectable** (all conditions met):
- Not a draft
- Approved (at least the required number of approvals)
- Pipeline passed on the source branch (status: `success`)
- No merge conflicts (`detailed_merge_status == "mergeable"`)
- All discussions resolved (`blocking_discussions_resolved == true`)

**Ineligible** (shown grayed out, not toggleable):
- Displayed with a reason badge: `draft` · `not approved` · `pipeline failed` ·
  `pipeline running` · `conflicts` · `unresolved threads`
- Sorted the same way so the user can see what's blocking them

**Sort order**: by `created_at` ascending (oldest first) by default — this is the
train execution order. User can manually reorder selectable MRs with `Shift+↑`/`Shift+↓`.
Ineligible MRs are always shown below and cannot be reordered.

**Keybindings**:

| Key | Action |
|-----|--------|
| `↑` / `k` | Move cursor up |
| `↓` / `j` | Move cursor down |
| `Shift+↑` / `Shift+K` | Move selected MR up in train order |
| `Shift+↓` / `Shift+J` | Move selected MR down in train order |
| `Space` | Toggle selection |
| `a` | Select all eligible |
| `A` | Deselect all |
| `r` | Change repo (go to repo picker) |
| `R` | Refetch MR list from GitLab |
| `Enter` | Start train with selected MRs |
| `q` | Quit |

**Start is disabled** if zero MRs are selected.

**Layout sketch**:
```
  Repo: gitlab.example.com / myteam / myrepo          [r] change repo

  Open merge requests                          3 selected / 5 eligible

  ● !42  Fix auth token expiry         @alice   2d ago   2 commits
  ● !38  Add rate limiting middleware  @bob     4d ago   5 commits
  ○ !35  Refactor user model           @carol   6d ago   11 commits

  ✗ !51  WIP: new dashboard            @dave    1h ago   1 commit    [draft]
  ✗ !47  Add oauth flow                @eve     3h ago   3 commits   [pipeline running]
  ✗ !44  DB migration v3               @frank   1d ago   2 commits   [not approved]
  ✗ !40  Update deps                   @grace   2d ago   7 commits   [conflicts]

  [Space] toggle  [a] all  [Shift+↑↓] reorder  [R] refresh  [Enter] start  [q] quit
```

---

### 4. Train run

Full-screen live log. No further interaction required unless something fails
(skip is automatic — see below).

**Header** shows overall progress: `Merging 3 of 3 MRs · !42 in progress`

**Per-MR block**:
```
  !42  Fix auth token expiry
  ├─ ✓ Rebase onto main           (2s)
  ├─ ✓ Pipeline passed            (4m 12s)
  ├─ ✓ Merged                     sha: a1b2c3
  └─ ✓ Main pipeline cancelled    (next MR pending)

  !38  Add rate limiting middleware
  ├─ ✓ Rebase onto main           (3s)
  ├─ … Pipeline running           (1m 22s elapsed)  [cancel]
```

**Keybindings during run**:

| Key | Action |
|-----|--------|
| `q` | Abort train (current MR is NOT rolled back; already-merged MRs stay merged) |

---

## Train execution logic

For each MR in FIFO (oldest-first) order:

```
1. REBASE
   PUT /projects/:id/merge_requests/:iid/rebase
   Poll GET ...?include_rebase_in_progress=true until done
   → on conflict: log error, mark MR as ⚠ SKIPPED, continue to next MR

2. WAIT FOR PIPELINE
   Poll GET /projects/:id/merge_requests/:iid (head_pipeline.status)
   until status is `success` or a terminal failure state
   → on failure: log error, mark MR as ⚠ SKIPPED, continue to next MR
   → pipeline status values treated as failure: failed, canceled, skipped

3. MERGE (with SHA guard)
   PUT /projects/:id/merge_requests/:iid/merge
     sha=<head_pipeline.sha>          ← abort if branch moved under us
     should_remove_source_branch=true
   → on 409 (SHA mismatch): rebase and retry from step 1 (once only)
   → on 405 / other error: log, mark SKIPPED, continue

4. CANCEL MAIN PIPELINE (if more MRs remain in the train)
   GET /projects/:id/pipelines?ref=main&status=running&order_by=id&sort=desc
   Take the most recently created one → POST .../cancel
   Remember the cancelled pipeline ID for possible restart in step 6.
   This prevents wasteful CI runs on intermediate main states.

5. If this was the LAST MR in the train AND it merged successfully:
   GET /projects/:id/pipelines?ref=main&status=running (or created/pending)
   Do NOT cancel — let the final main pipeline run naturally.

6. If this was the LAST MR in the train AND it was SKIPPED (own pipeline failed):
   a. If at least one prior MR was merged (a main pipeline was cancelled in
      step 4): restart the cancelled pipeline:
      POST /projects/:id/pipelines/:cancelled_pipeline_id/retry
      Log: "Last MR skipped — restarted main pipeline: <url>"
   b. If NO MR was merged in this train run (all skipped): do nothing —
      no main state changed, no pipeline was cancelled. Skip to step 7.

7. WAIT FOR MAIN PIPELINE (only if at least one MR was merged or a pipeline
   was restarted in step 6)
   Whether the main pipeline was left running naturally (last MR merged) or
   restarted (last MR skipped after a prior merge), poll and display its
   live status:
   GET /projects/:id/pipelines?ref=main&order_by=id&sort=desc (take first result)
   Poll every 10s until status is terminal (success / failed / canceled).
   Show in the train run log:
   └─ … Main pipeline running  (3m 14s elapsed)  #1234  <url>
   └─ ✓ Main pipeline passed   (5m 02s)
   or
   └─ ✗ Main pipeline failed   (4m 44s)  <url>
   If all MRs were skipped, skip this step and show:
   "All MRs skipped — nothing to merge."
```

**Polling intervals**:
- Rebase completion: every 2s, timeout 60s
- Pipeline status: every 10s, no hard timeout (user can abort with `q`)

---

## TBD / open questions

1. **Approvals on Free tier** — `GET /projects/:id/merge_requests/:iid/approvals`
   returns approval data on all tiers, but per-rule detail is Premium+. Need to
   verify what the Free-tier response looks like in practice for the eligibility
   check. Fallback: treat `approvals_left == 0` as approved.

2. **Token scopes** — confirm `api` scope is sufficient, or whether `read_api` +
   a write scope would be preferable for principle of least privilege.

---

## Config file (`~/.config/glmt/config.toml`)

```toml
[gitlab]
host = "gitlab.example.com"   # read from glab config, not duplicated here

[defaults]
repo = "myteam/myrepo"        # last-used project path

[behavior]
poll_rebase_interval_s = 2
poll_pipeline_interval_s = 10
remove_source_branch = true
```

---

## Testing

### Philosophy

- No snapshot / golden file tests — they create noise without catching real bugs
- No exhaustive `View()` testing — it's presentational and changes freely
- Test model state after `Update()`, not rendered output
- The `GitLabClient` interface is the most important seam — enforce it from day one

### Layer 1 — Config / auth reader

Plain unit tests against fixture files in `testdata/`. Copy real glab config
file format into fixtures, assert the parsed struct matches expectations. No
mocking, no HTTP. Covers credential parsing which is the fiddliest cold-start
logic.

```
internal/auth/
├── reader.go
├── reader_test.go
└── testdata/
    ├── glab_config_single_host.yml
    └── glab_config_multi_host.yml
```

### Layer 2 — GitLab API wrapper

Every method on the concrete `go-gitlab` client implementation gets a test
using `net/http/httptest.NewServer`. Return canned JSON copied from real GitLab
API responses. Assert the right endpoint was called, right HTTP verb, right
query parameters.

Key scenarios to cover:
- `ListMRs` — pagination, filtering, `detailed_merge_status` variants
- `RebaseMR` — 202 accepted, then poll loop: in-progress → done, in-progress → conflict
- `GetMR` — pipeline status variants, `blocking_discussions_resolved`
- `MergeMR` — success, 409 SHA mismatch, 405 not mergeable
- `CancelPipeline` — success, already cancelled (idempotent)
- `RetryPipeline` — success (restart a cancelled pipeline)
- `GetLatestPipeline` — running, success, failed

```
internal/gitlab/
├── client.go
├── client_test.go       ← httptest mock server per test
└── testdata/
    ├── mr_list.json
    ├── mr_detail_mergeable.json
    ├── mr_detail_conflict.json
    └── ...
```

### Layer 3 — Train execution state machine

The train loop (`internal/train/`) depends only on the `GitLabClient`
interface, never on the concrete implementation. Tests inject a mock that
records calls and returns controlled responses.

Scenarios to cover as table-driven tests:

| Scenario | Expected outcome |
|---|---|
| All MRs succeed | All merged, main pipeline awaited |
| MR #2 pipeline fails | #2 skipped, #3 proceeds, main pipeline awaited |
| Last MR skipped (prior MR merged) | Cancelled main pipeline restarted, then awaited |
| SHA mismatch on merge | Rebase retried once, then merged |
| SHA mismatch twice | MR skipped |
| Rebase conflict | MR skipped, continue |
| All MRs skipped | No merges → no cancelled pipeline → nothing to restart or await |
| Train aborted by user | Current MR not rolled back, already-merged stay merged |

Assert on: sequence of calls made to the mock client, final `TrainResult`
struct (per-MR status, skip reasons), whether `RetryPipeline` was called.

```
internal/train/
├── runner.go
├── runner_test.go    ← mock GitLabClient, table-driven
└── mock_client.go    ← hand-written mock (or use mockery)
```

### Layer 4 — TUI models

No `teatest`, no snapshots. For each screen, call `Update()` with key messages
and assert on model state fields. Spot-check `View()` only for semantically
meaningful strings.

```go
// State assertions — the important part
m := NewMRListModel(fakeMRs)
m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
assert.Equal(t, 1, len(m.Selected()))

m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
assert.Equal(t, 1, m.Cursor())

// View spot-checks — only for things that matter semantically
view := m.View()
assert.Contains(t, view, "[draft]")
assert.Contains(t, view, "2 commits")
assert.NotContains(t, view, "[Enter] start") // disabled with 0 selected
```

Scenarios per screen:

**MR list**: cursor movement, toggle selection, select-all / deselect-all,
reordering (Shift+↑↓), ineligible MR shows correct badge, start disabled when
nothing selected, `R` triggers a refetch message.

**Train run**: renders `⚠ SKIPPED` on skip, renders pipeline URL, renders
final main pipeline status, abort key emits correct message.

```
internal/tui/
├── mrlist.go
├── mrlist_test.go
├── trainrun.go
└── trainrun_test.go
```

### Layer 5 — E2E (CI only)

Spin up `gitlab/gitlab-ce` as a service container in CI. Seed a test project
with MRs via the API (approved, pipelines mocked as passed). Run the `glmt`
binary non-interactively with pre-selected MR IIDs passed as flags (a
`--non-interactive` mode flag that skips the TUI and just runs the train).
Assert exit code and stdout output.

Slow (~3-5 min for GitLab CE to boot). Runs only in CI, never locally by
default. Gives confidence that all layers compose correctly against a real
GitLab instance.

```
e2e/
├── setup_test.go     ← start GitLab CE, seed data, defer teardown
└── train_test.go     ← run binary, assert outcomes
```

### Test execution

```bash
go test ./...                    # layers 1–4, fast, always local
go test ./e2e/... -tags e2e      # layer 5, CI only, requires Docker
```

---

## Project structure

```
glmt/
├── cmd/
│   └── glmt/
│       └── main.go
├── internal/
│   ├── auth/
│   │   ├── reader.go
│   │   ├── reader_test.go
│   │   └── testdata/
│   │       ├── glab_config_single_host.yml
│   │       └── glab_config_multi_host.yml
│   ├── config/          # load/save ~/.config/glmt/config.toml
│   │   ├── config.go
│   │   └── config_test.go
│   ├── gitlab/
│   │   ├── interface.go  # GitLabClient interface — the key seam
│   │   ├── client.go     # go-gitlab concrete implementation
│   │   ├── client_test.go
│   │   └── testdata/
│   │       ├── mr_list.json
│   │       ├── mr_detail_mergeable.json
│   │       ├── mr_detail_conflict.json
│   │       └── ...
│   ├── train/
│   │   ├── runner.go
│   │   ├── runner_test.go
│   │   └── mock_client.go
│   └── tui/
│       ├── setup.go
│       ├── setup_test.go
│       ├── repopicker.go
│       ├── repopicker_test.go
│       ├── mrlist.go
│       ├── mrlist_test.go
│       ├── trainrun.go
│       └── trainrun_test.go
├── e2e/
│   ├── setup_test.go
│   └── train_test.go
├── go.mod
└── README.md
```
