package log

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// FileLogger writes JSON Lines log entries to a file.
type FileLogger struct {
	file  *os.File
	token string
}

// DefaultStateDir returns the platform-appropriate state directory for glmt.
func DefaultStateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "glmt")
	}
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "glmt")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(home, ".local", "state", "glmt")
}

// NewFileLogger creates the state directory and opens a timestamped log file.
func NewFileLogger(dir, token string) (*FileLogger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}

	now := time.Now().UTC()
	name := now.Format("2006-01-02T150405") + fmt.Sprintf("-%03d", now.Nanosecond()/1e6) + ".jsonl"
	path := filepath.Join(dir, name)

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("creating log file: %w", err)
	}

	return &FileLogger{file: f, token: token}, nil
}

// Close closes the log file.
func (fl *FileLogger) Close() {
	if fl.file != nil {
		fl.file.Close()
	}
}

func (fl *FileLogger) write(entry map[string]any) {
	entry["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(entry)
	if err != nil {
		return // best-effort
	}
	data = append(data, '\n')
	fl.file.Write(data) // best-effort: ignore write errors
}

func scrubToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[REDACTED]")
}

// LogSession writes a session start entry when the log file is first opened.
func (fl *FileLogger) LogSession() {
	fl.write(map[string]any{
		"level":        "meta",
		"msg":          "session started",
		"glmt_version": "dev",
	})
}

// LogMeta writes the run metadata header.
func (fl *FileLogger) LogMeta(projectID int, mrIIDs []int) {
	fl.write(map[string]any{
		"level":        "meta",
		"msg":          "train started",
		"project_id":   projectID,
		"mrs":          mrIIDs,
		"glmt_version": "dev",
	})
}

// LogStep writes a step or decision log entry.
func (fl *FileLogger) LogStep(mrIID int, step, message string) {
	fl.write(map[string]any{
		"level": "info",
		"mr":    mrIID,
		"step":  step,
		"msg":   scrubToken(message, fl.token),
	})
}

// LogRunEnd writes the run summary.
func (fl *FileLogger) LogRunEnd(merged, skipped, pending int, pipelineStatus string, duration time.Duration) {
	fl.write(map[string]any{
		"level":                "meta",
		"msg":                  "train finished",
		"merged":               merged,
		"skipped":              skipped,
		"pending":              pending,
		"duration_ms":          duration.Milliseconds(),
		"main_pipeline_status": pipelineStatus,
	})
}

// LogAPI writes an API call log entry.
func (fl *FileLogger) LogAPI(method string, args map[string]any, ok bool, callErr error, duration time.Duration) {
	errStr := ""
	if callErr != nil {
		errStr = scrubToken(callErr.Error(), fl.token)
	}
	fl.write(map[string]any{
		"level": "debug",
		"step":  "api",
		"msg":   method,
		"api": map[string]any{
			"method":      method,
			"args":        args,
			"ok":          ok,
			"error":       errStr,
			"duration_ms": duration.Milliseconds(),
		},
	})
}
