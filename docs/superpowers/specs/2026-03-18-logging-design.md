# Logging Design: Post-Run Debugging via `--log` Flag

## Problem

Once the glmt TUI exits, all execution history is lost. When a merge train fails, there is no way to review what happened after the fact. Users need a persistent record they can inspect for post-run debugging.

## Decision Summary

| Aspect | Choice |
|--------|--------|
| Trigger | `--log` CLI flag (opt-in) |
| Location | `~/.local/state/glmt/` (XDG STATE_HOME) |
| File naming | `2026-03-18T150405-123.jsonl` (one per run, ms precision) |
| Format | JSON Lines |
| Content levels | Step logs, API logs, decision logs |
| Token handling | Scrubbed — never written to log |
| Cleanup | None — user-managed |
| Dependencies | None — stdlib only |
| Error handling | Best-effort — write failures silently swallowed |

## Log File Location

Log files are written to `~/.local/state/glmt/` following the XDG Base Directory specification, which designates `XDG_STATE_HOME` for logs and history. Note: this is more XDG-compliant than the existing config path (`~/.config/glmt/`), which hardcodes the path without checking `$XDG_CONFIG_HOME`. Fixing config path compliance is out of scope.

A `StateDir()` helper resolves the path cross-platform:
- Linux/macOS: `$XDG_STATE_HOME/glmt` or `~/.local/state/glmt`
- Windows: `%LOCALAPPDATA%\glmt`

Each run creates a new file named with an ISO 8601 timestamp at millisecond precision to avoid collisions from concurrent runs: `2026-03-18T150405-123.jsonl` (hyphen before milliseconds to avoid double-dot ambiguity with the `.jsonl` extension). Collisions at millisecond granularity are accepted as practically impossible.

## Log Format

JSON Lines — one JSON object per line. Three categories:

### Run metadata (first line)

```json
{"ts":"2026-03-18T15:04:05.123Z","level":"meta","msg":"train started","project_id":123,"mrs":[42,38,35],"glmt_version":"dev"}
```

The `glmt_version` field is hardcoded to `"dev"` initially. A build-time `ldflags` variable can be added later.

### Step logs (level: info)

Existing `train.Logger` callback entries — the same content the TUI displays:

```json
{"ts":"2026-03-18T15:04:05.123Z","level":"info","mr":42,"step":"rebase","msg":"Rebasing merge request..."}
{"ts":"2026-03-18T15:04:07.456Z","level":"info","mr":42,"step":"merge","msg":"Merged successfully"}
```

### API logs (level: debug)

Every GitLab API call with Go method name, arguments, success/error, and duration. The decorator wraps the `gitlab.Client` interface, so it logs at the method level — not the HTTP level. Real HTTP paths and status codes are not available through this interface.

```json
{"ts":"2026-03-18T15:04:05.200Z","level":"debug","step":"api","msg":"RebaseMergeRequest","api":{"method":"RebaseMergeRequest","args":{"project_id":123,"mr_iid":42},"ok":true,"error":"","duration_ms":340}}
```

### Decision logs (level: info)

Internal logic and state transitions already expressed through the existing Logger callback — entries like "SHA mismatch on retry, skipping", "merge status is 'broken_status', retrying (2/5)", "no main pipeline found after retries". These already flow through the Logger; the file logger captures them as step logs.

### Run end (last line)

```json
{"ts":"2026-03-18T15:12:30.000Z","level":"meta","msg":"train finished","merged":2,"skipped":1,"pending":0,"duration_ms":450000,"main_pipeline_status":"success"}
```

## Token Scrubbing

- The token string is passed to `FileLogger` at creation; any accidental appearance in a log message is replaced with `[REDACTED]`.
- The `LoggingClient` decorator never receives the token — it only logs method names, arguments (project ID, MR IID), and outcomes.

## Error Handling

Write errors in `LogStep`, `LogAPI`, and other log methods are **silently swallowed**. The file logger must never propagate errors to the train runner or TUI. Logging is best-effort — a disk-full condition should not break a merge train that's already in progress.

If the log file cannot be created at startup (e.g., permission denied on state dir), `NewFileLogger` returns an error and glmt prints a warning to stderr but continues without logging.

## Implementation Architecture

### New package: `internal/log/`

Single file `log.go` containing:

- **`FileLogger`** struct — wraps `*os.File`, writes JSON lines
- **`NewFileLogger(dir, token string) (*FileLogger, error)`** — creates state dir, opens timestamped file, stores token for scrubbing
- **`LogStep(mrIID int, step, message string)`** — writes step/decision entry
- **`LogAPI(method string, args map[string]any, ok bool, err error, duration time.Duration)`** — writes API entry
- **`LogMeta(projectID int, mrIIDs []int)`** — writes run header
- **`LogRunEnd(merged, skipped, pending int, pipelineStatus string, duration time.Duration)`** — writes run summary
- **`Close()`** — flushes and closes file
- **`DefaultStateDir() string`** — resolves `~/.local/state/glmt/` cross-platform

Internal helper: `scrubToken(s, token string) string`.

Note: to avoid a circular dependency (`log` importing `train` for `Result`), `LogRunEnd` accepts primitive fields (merged, skipped, pending counts, pipeline status, duration) rather than `*train.Result` directly.

### Composite logger

The existing `train.Logger` callback is wrapped to fan out to both the original destination (TUI channel or stdout) and the file logger:

```go
originalLogger := func(mrIID int, step, msg string) { /* TUI or stdout */ }
compositeLogger := func(mrIID int, step, msg string) {
    originalLogger(mrIID, step, msg)
    fileLogger.LogStep(mrIID, step, msg)
}
runner := train.NewRunner(client, projectID, compositeLogger)
```

### API logging via `LoggingClient` decorator

A thin wrapper implementing `gitlab.Client` that delegates every method to an inner client and logs each call:

```go
type LoggingClient struct {
    inner  gitlab.Client
    logger *log.FileLogger
}
```

Each of the 10 interface methods:
1. Records start time
2. Delegates to `inner`
3. Calls `logger.LogAPI(methodName, args, ok, err, duration)`

Each of the 10 `LoggingClient` methods manually constructs its `args` map with the relevant parameter names and values. No reflection is used. The decorator logs Go method names and arguments (project ID, MR IID, ref, status filter) — not HTTP paths or status codes, which are not accessible through the `gitlab.Client` interface.

### Wiring

**`cmd/glmt/main.go` (non-interactive):** Add `--log` flag (ignored for the `logout` subcommand). When set:
1. Create `FileLogger` with token for scrubbing
2. Wrap `gitlab.APIClient` in `LoggingClient`
3. Create composite logger
4. Call `LogMeta` before `runner.Run()`
5. Call `LogRunEnd` after `runner.Run()`

**`internal/tui/app.go` (TUI mode):** Add a public `FileLogger *log.FileLogger` field on `AppModel`. Callers set it after construction (`model.FileLogger = fileLogger`) rather than via a constructor parameter — this avoids changing the `NewAppModel` signature and breaking existing call sites. The `--log` flag is only supported when credentials are already available (from config or CLI flags). If the user enters credentials through the Setup screen, logging is not available for that run — this avoids complexity of late-binding the token. A warning is printed if `--log` is used without pre-existing credentials.

When `FileLogger` is present, `startTrain()` wires logging inside the goroutine:
1. Wrap `m.client` in `LoggingClient` locally (do not mutate `m.client`, which is shared for non-train API calls like `fetchMRs`)
2. Call `LogMeta` before `runner.Run()`
3. Call `LogRunEnd` after `runner.Run()` returns
4. `defer fileLogger.Close()` inside the goroutine

The composite logger wraps the TUI channel logger the same way as non-interactive mode.

### Files changed

| File | Change |
|------|--------|
| `internal/log/log.go` | **New** — FileLogger, StateDir, scrubbing |
| `internal/log/logging_client.go` | **New** — LoggingClient decorator |
| `cmd/glmt/main.go` | Add `--log` flag, wire FileLogger + LoggingClient |
| `internal/tui/app.go` | Accept optional FileLogger, composite wiring in `startTrain()` |

### Files NOT changed

- `internal/train/runner.go` — no changes
- `internal/gitlab/client.go` — no changes
- `internal/gitlab/interface.go` — no changes
- Existing test files — no changes

## Testing Strategy

### Unit tests (`internal/log/`)

- `TestFileLogger_WritesJSONLines` — write step/API/meta/run-end entries, read file, parse each line, verify fields
- `TestScrubToken` — verify token replaced with `[REDACTED]`
- `TestNewFileLogger_CreatesDir` — verify state dir created if missing
- `TestLogStep_WriteError_Swallowed` — close file before writing, verify no panic

### LoggingClient decorator test

- Use hand-written mock as inner client
- Call methods, verify mock called AND log file contains matching API entries with correct method names and args

### No changes to existing tests

The composite logger and LoggingClient are only wired when `--log` is set. All existing test paths are unaffected.

### Manual verification

```bash
./glmt --log
# run a train, then:
cat ~/.local/state/glmt/*.jsonl | jq .
```
