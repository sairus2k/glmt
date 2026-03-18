# Logging Design: Post-Run Debugging via `--log` Flag

## Problem

Once the glmt TUI exits, all execution history is lost. When a merge train fails, there is no way to review what happened after the fact. Users need a persistent record they can inspect for post-run debugging.

## Decision Summary

| Aspect | Choice |
|--------|--------|
| Trigger | `--log` CLI flag (opt-in) |
| Location | `~/.local/state/glmt/` (XDG STATE_HOME) |
| File naming | `2026-03-18T150405.jsonl` (one per run) |
| Format | JSON Lines |
| Content levels | Step logs, API logs, decision logs |
| Token handling | Scrubbed — never written to log |
| Cleanup | None — user-managed |
| Dependencies | None — stdlib only |

## Log File Location

Log files are written to `~/.local/state/glmt/` following the XDG Base Directory specification, which designates `XDG_STATE_HOME` for logs and history. This is consistent with the project's existing `~/.config/glmt/` config path and matches the `gh` CLI's approach.

A `StateDir()` helper resolves the path cross-platform:
- Linux/macOS: `$XDG_STATE_HOME/glmt` or `~/.local/state/glmt`
- Windows: `%LOCALAPPDATA%\glmt`

Each run creates a new file named with an ISO 8601 timestamp: `2026-03-18T150405.jsonl`.

## Log Format

JSON Lines — one JSON object per line. Three categories:

### Run metadata (first line)

```json
{"ts":"2026-03-18T15:04:05.000Z","level":"meta","msg":"train started","project_id":123,"mrs":[42,38,35],"glmt_version":"dev"}
```

### Step logs (level: info)

Existing `train.Logger` callback entries — the same content the TUI displays:

```json
{"ts":"2026-03-18T15:04:05.123Z","level":"info","mr":42,"step":"rebase","msg":"Rebasing merge request..."}
{"ts":"2026-03-18T15:04:07.456Z","level":"info","mr":42,"step":"merge","msg":"Merged successfully"}
```

### API logs (level: debug)

Every GitLab API call with method, path, HTTP status, and duration:

```json
{"ts":"2026-03-18T15:04:05.200Z","level":"debug","mr":0,"step":"api","msg":"API call","api":{"method":"PUT","path":"/projects/123/merge_requests/42/rebase","status":200,"duration_ms":340}}
```

### Decision logs (level: info)

Internal logic and state transitions already expressed through the existing Logger callback — entries like "SHA mismatch on retry, skipping", "merge status is 'broken_status', retrying (2/5)", "no main pipeline found after retries". These already flow through the Logger; the file logger captures them as step logs.

## Token Scrubbing

- The `Authorization` header is never logged.
- API paths are logged without query parameters containing tokens.
- The token string is passed to `FileLogger` at creation; any accidental appearance in a log message is replaced with `[REDACTED]`.

## Implementation Architecture

### New package: `internal/log/`

Single file `log.go` containing:

- **`FileLogger`** struct — wraps `*os.File`, writes JSON lines
- **`NewFileLogger(dir, token string) (*FileLogger, error)`** — creates state dir, opens timestamped file, stores token for scrubbing
- **`LogStep(mrIID int, step, message string)`** — writes step/decision entry
- **`LogAPI(method, path string, status int, duration time.Duration)`** — writes API entry
- **`LogMeta(projectID int, mrIIDs []int)`** — writes run header
- **`Close()`** — flushes and closes file
- **`DefaultStateDir() string`** — resolves `~/.local/state/glmt/` cross-platform

Internal helper: `scrubToken(s, token string) string`.

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

Each of the 12 interface methods:
1. Records start time
2. Delegates to `inner`
3. Calls `logger.LogAPI(method, path, status, duration)`

The decorator never receives the token — it only logs method, path, status, and duration.

### Wiring

**`cmd/glmt/main.go`:** Add `--log` flag. When set:
1. Create `FileLogger`
2. Wrap `gitlab.APIClient` in `LoggingClient`
3. Create composite logger
4. Pass both to runner

**`internal/tui/app.go`:** Accept optional `*log.FileLogger`. When present, `startTrain()` wraps the logger callback and the client the same way.

### Files changed

| File | Change |
|------|--------|
| `internal/log/log.go` | **New** — FileLogger, StateDir, scrubbing |
| `cmd/glmt/main.go` | Add `--log` flag, wire FileLogger + LoggingClient |
| `internal/tui/app.go` | Accept optional FileLogger, composite wiring in `startTrain()` |

### Files NOT changed

- `internal/train/runner.go` — no changes
- `internal/gitlab/client.go` — no changes
- `internal/gitlab/interface.go` — no changes
- Existing test files — no changes

## Testing Strategy

### Unit tests (`internal/log/`)

- `TestFileLogger_WritesJSONLines` — write step/API/meta entries, read file, parse each line, verify fields
- `TestScrubToken` — verify token replaced with `[REDACTED]`
- `TestNewFileLogger_CreatesDir` — verify state dir created if missing

### LoggingClient decorator test

- Use hand-written mock as inner client
- Call methods, verify mock called AND log file contains matching API entries

### No changes to existing tests

The composite logger and LoggingClient are only wired when `--log` is set. All existing test paths are unaffected.

### Manual verification

```bash
./glmt --log
# run a train, then:
cat ~/.local/state/glmt/*.jsonl | jq .
```
