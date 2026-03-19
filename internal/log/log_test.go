package log

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	expected := filepath.Join(home, ".local", "state", "glmt")
	assert.Equal(t, expected, DefaultStateDir())
}

func TestDefaultStateDir_XDGOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-test-state")
	assert.Equal(t, "/tmp/xdg-test-state/glmt", DefaultStateDir())
}

func TestNewFileLogger_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "nested", "state")

	fl, err := NewFileLogger(subdir, "secret-token")
	require.NoError(t, err)
	defer fl.Close()

	// Directory was created
	info, err := os.Stat(subdir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// File was created with .jsonl extension
	entries, err := os.ReadDir(subdir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Name(), ".jsonl")
}

func TestScrubToken(t *testing.T) {
	assert.Equal(t, "host [REDACTED] end", scrubToken("host secret-tok end", "secret-tok"))
	assert.Equal(t, "no token here", scrubToken("no token here", "secret-tok"))
	assert.Equal(t, "empty token stays", scrubToken("empty token stays", ""))
}

func TestFileLogger_WritesJSONLines(t *testing.T) {
	dir := t.TempDir()
	fl, err := NewFileLogger(dir, "secret-tok")
	require.NoError(t, err)

	fl.LogSession()
	fl.LogMeta(123, []int{42, 38})
	fl.LogStep(42, "rebase_wait", "Rebasing merge request...")
	fl.LogStep(42, "merge", "Merging with SHA guard (sha=secret-tok)...")
	fl.LogRunEnd(1, 1, 0, "success", 5*time.Minute)
	fl.Close()

	// Read the file
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 5)

	// Session line
	var session map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &session))
	assert.Equal(t, "meta", session["level"])
	assert.Equal(t, "session started", session["msg"])

	// Meta line
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &meta))
	assert.Equal(t, "meta", meta["level"])
	assert.Equal(t, "train started", meta["msg"])
	assert.Equal(t, float64(123), meta["project_id"])

	// Step line
	var step map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[2]), &step))
	assert.Equal(t, "info", step["level"])
	assert.Equal(t, float64(42), step["mr"])
	assert.Equal(t, "rebase_wait", step["step"])

	// Token scrubbed in step
	var step2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[3]), &step2))
	assert.Contains(t, step2["msg"], "[REDACTED]")
	assert.NotContains(t, step2["msg"], "secret-tok")

	// Run end line
	var end map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[4]), &end))
	assert.Equal(t, "meta", end["level"])
	assert.Equal(t, "train finished", end["msg"])
	assert.Equal(t, float64(1), end["merged"])
	assert.Equal(t, float64(300000), end["duration_ms"])
}

func TestLogStep_WriteError_Swallowed(t *testing.T) {
	dir := t.TempDir()
	fl, err := NewFileLogger(dir, "tok")
	require.NoError(t, err)

	// Close the file to force write errors
	fl.file.Close()

	// Should not panic
	assert.NotPanics(t, func() {
		fl.LogStep(1, "test", "msg")
		fl.LogMeta(1, []int{1})
		fl.LogRunEnd(0, 0, 0, "", 0)
	})
}

func TestFileLogger_LogAPI(t *testing.T) {
	dir := t.TempDir()
	fl, err := NewFileLogger(dir, "tok")
	require.NoError(t, err)

	fl.LogAPI("RebaseMergeRequest", map[string]any{"project_id": 123, "mr_iid": 42}, true, nil, 340*time.Millisecond)
	fl.LogAPI("MergeMergeRequest", map[string]any{"project_id": 123, "mr_iid": 42}, false, fmt.Errorf("SHA mismatch"), 50*time.Millisecond)
	fl.Close()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 2)

	var entry1 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry1))
	assert.Equal(t, "debug", entry1["level"])
	assert.Equal(t, "api", entry1["step"])
	assert.Equal(t, "RebaseMergeRequest", entry1["msg"])
	api1 := entry1["api"].(map[string]any)
	assert.Equal(t, "RebaseMergeRequest", api1["method"])
	assert.Equal(t, true, api1["ok"])
	assert.Equal(t, float64(340), api1["duration_ms"])

	var entry2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &entry2))
	api2 := entry2["api"].(map[string]any)
	assert.Equal(t, false, api2["ok"])
	assert.Equal(t, "SHA mismatch", api2["error"])
}

func TestLogAPI_ScrubsTokenInErrors(t *testing.T) {
	dir := t.TempDir()
	fl, err := NewFileLogger(dir, "my-secret-token")
	require.NoError(t, err)

	fl.LogAPI("GetCurrentUser", nil, false, fmt.Errorf("401: invalid token my-secret-token"), 10*time.Millisecond)
	fl.Close()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 1)

	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
	api := entry["api"].(map[string]any)
	assert.Contains(t, api["error"], "[REDACTED]")
	assert.NotContains(t, api["error"], "my-secret-token")
}
