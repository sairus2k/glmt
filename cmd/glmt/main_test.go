package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sairus2k/glmt/internal/config"
	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/sairus2k/glmt/internal/train"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- logout tests (existing) ---

func TestLogout_RemovesConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(cfgPath, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use a non-existent glab dir so glab note is not printed.
	err := logout(cfgPath, filepath.Join(dir, "no-glab"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Error("config file should have been removed")
	}
}

func TestLogout_NoErrorWhenConfigMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "does-not-exist.toml")

	err := logout(cfgPath, filepath.Join(dir, "no-glab"))
	if err != nil {
		t.Fatalf("expected no error for missing config, got: %v", err)
	}
}

func TestLogout_PrintsGlabNote_WhenCredsExist(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// Create a fake glab config with credentials.
	glabDir := filepath.Join(dir, "glab-cli")
	if err := os.MkdirAll(glabDir, 0o755); err != nil {
		t.Fatal(err)
	}
	glabConfig := `hosts:
  gitlab.example.com:
    token: glpat-test
    api_protocol: https
`
	if err := os.WriteFile(filepath.Join(glabDir, "config.yml"), []byte(glabConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture stdout to verify the glab note.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := logout(cfgPath, glabDir)

	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if expected := "Note: glab CLI credentials still exist"; !contains(output, expected) {
		t.Errorf("expected output to contain %q, got: %s", expected, output)
	}
}

func TestLogout_NoGlabNote_WhenNoGlabCreds(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := logout(cfgPath, filepath.Join(dir, "no-glab"))

	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if contains(output, "glab CLI credentials") {
		t.Errorf("should not mention glab when no creds exist, got: %s", output)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- parseMRIIDs tests ---

func TestParseMRIIDs(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []int
		wantErr string
	}{
		{name: "single", input: "42", want: []int{42}},
		{name: "multiple", input: "42,38,35", want: []int{42, 38, 35}},
		{name: "spaces around", input: " 42 , 38 , 35 ", want: []int{42, 38, 35}},
		{name: "trailing comma", input: "42,38,", want: []int{42, 38}},
		{name: "empty string", input: "", wantErr: "no valid MR IIDs"},
		{name: "only commas", input: ",,,", wantErr: "no valid MR IIDs"},
		{name: "invalid number", input: "42,abc", wantErr: `invalid MR IID "abc"`},
		{name: "negative number", input: "-1", wantErr: "invalid MR IID"},
		{name: "zero", input: "0", wantErr: "invalid MR IID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMRIIDs(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- countResults tests ---

func TestCountResults(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		merged, skipped, pending := countResults(nil)
		assert.Equal(t, 0, merged)
		assert.Equal(t, 0, skipped)
		assert.Equal(t, 0, pending)
	})

	t.Run("mixed statuses", func(t *testing.T) {
		result := &train.Result{
			MRResults: []train.MRResult{
				{Status: train.MRStatusMerged},
				{Status: train.MRStatusSkipped},
				{Status: train.MRStatusMerged},
				{Status: train.MRStatusPending},
			},
		}
		merged, skipped, pending := countResults(result)
		assert.Equal(t, 2, merged)
		assert.Equal(t, 1, skipped)
		assert.Equal(t, 1, pending)
	})

	t.Run("all merged", func(t *testing.T) {
		result := &train.Result{
			MRResults: []train.MRResult{
				{Status: train.MRStatusMerged},
				{Status: train.MRStatusMerged},
			},
		}
		merged, skipped, pending := countResults(result)
		assert.Equal(t, 2, merged)
		assert.Equal(t, 0, skipped)
		assert.Equal(t, 0, pending)
	})
}

// --- printTrainResults tests ---

// captureStdout runs fn while capturing stdout, returns the output.
// NOT safe for use with t.Parallel() — mutates global os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	fn()

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

func TestPrintTrainResults(t *testing.T) {
	t.Run("nil result returns error", func(t *testing.T) {
		err := printTrainResults(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no results to display")
	})

	t.Run("all merged returns nil", func(t *testing.T) {
		result := &train.Result{
			MRResults: []train.MRResult{
				{MR: &gitlab.MergeRequest{IID: 1}, Status: train.MRStatusMerged},
				{MR: &gitlab.MergeRequest{IID: 2}, Status: train.MRStatusMerged},
			},
		}
		output := captureStdout(t, func() {
			err := printTrainResults(result)
			require.NoError(t, err)
		})
		assert.Contains(t, output, "MR !1: merged")
		assert.Contains(t, output, "MR !2: merged")
	})

	t.Run("skipped MR returns error", func(t *testing.T) {
		result := &train.Result{
			MRResults: []train.MRResult{
				{MR: &gitlab.MergeRequest{IID: 1}, Status: train.MRStatusMerged},
				{MR: &gitlab.MergeRequest{IID: 2}, Status: train.MRStatusSkipped, SkipReason: "pipeline failed"},
			},
		}
		output := captureStdout(t, func() {
			err := printTrainResults(result)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not all MRs were merged")
		})
		assert.Contains(t, output, "MR !2: skipped (pipeline failed)")
	})

	t.Run("pending MR returns error", func(t *testing.T) {
		result := &train.Result{
			MRResults: []train.MRResult{
				{MR: &gitlab.MergeRequest{IID: 5}, Status: train.MRStatusPending},
			},
		}
		captureStdout(t, func() {
			err := printTrainResults(result)
			require.Error(t, err)
		})
	})

	t.Run("includes pipeline status", func(t *testing.T) {
		result := &train.Result{
			MRResults: []train.MRResult{
				{MR: &gitlab.MergeRequest{IID: 1}, Status: train.MRStatusMerged},
			},
			MainPipelineStatus: "success",
			MainPipelineURL:    "http://example.com/pipelines/99",
		}
		output := captureStdout(t, func() {
			err := printTrainResults(result)
			require.NoError(t, err)
		})
		assert.Contains(t, output, "Main pipeline: success (http://example.com/pipelines/99)")
	})
}

// --- configPath tests ---

func TestConfigPath(t *testing.T) {
	t.Run("uses GLMT_CONFIG when set", func(t *testing.T) {
		t.Setenv("GLMT_CONFIG", "/tmp/custom.toml")
		assert.Equal(t, "/tmp/custom.toml", configPath())
	})

	t.Run("falls back to default when unset", func(t *testing.T) {
		t.Setenv("GLMT_CONFIG", "")
		assert.Equal(t, config.DefaultPath(), configPath())
	})
}

// --- buildLogFunc tests ---

func TestBuildLogFunc(t *testing.T) {
	t.Run("without file logger", func(t *testing.T) {
		logger := buildLogFunc(nil)
		output := captureStdout(t, func() {
			logger(42, "rebase", "done")
		})
		assert.Contains(t, output, "[MR !42] [rebase] done")
	})

	t.Run("zero MR IID omits MR prefix", func(t *testing.T) {
		logger := buildLogFunc(nil)
		output := captureStdout(t, func() {
			logger(0, "pipeline", "waiting")
		})
		assert.Contains(t, output, "[pipeline] waiting")
		assert.NotContains(t, output, "MR")
	})
}

// --- prepareNonInteractive tests ---

func TestPrepareNonInteractive(t *testing.T) {
	t.Run("missing all flags", func(t *testing.T) {
		t.Setenv("GLMT_CONFIG", filepath.Join(t.TempDir(), "nonexistent.toml"))
		_, _, _, _, err := prepareNonInteractive("", "", 0, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--host")
		assert.Contains(t, err.Error(), "--token")
		assert.Contains(t, err.Error(), "--project-id")
		assert.Contains(t, err.Error(), "--mrs")
	})

	t.Run("all flags provided", func(t *testing.T) {
		t.Setenv("GLMT_CONFIG", filepath.Join(t.TempDir(), "nonexistent.toml"))
		cfg, host, token, mrIIDs, err := prepareNonInteractive("gitlab.example.com", "tok-123", 42, "1,2,3")
		require.NoError(t, err)
		assert.Equal(t, "gitlab.example.com", host)
		assert.Equal(t, "tok-123", token)
		assert.Equal(t, []int{1, 2, 3}, mrIIDs)
		assert.NotNil(t, cfg)
	})

	t.Run("config fallback for host and token", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")
		cfgContent := `[gitlab]
host = "saved.example.com"
token = "saved-token"
`
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0o644))
		t.Setenv("GLMT_CONFIG", cfgPath)

		cfg, host, token, mrIIDs, err := prepareNonInteractive("", "", 99, "10,20")
		require.NoError(t, err)
		assert.Equal(t, "saved.example.com", host)
		assert.Equal(t, "saved-token", token)
		assert.Equal(t, []int{10, 20}, mrIIDs)
		assert.NotNil(t, cfg)
	})

	t.Run("flags override config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.toml")
		cfgContent := `[gitlab]
host = "saved.example.com"
token = "saved-token"
`
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfgContent), 0o644))
		t.Setenv("GLMT_CONFIG", cfgPath)

		_, host, token, _, err := prepareNonInteractive("flag.example.com", "flag-token", 1, "5")
		require.NoError(t, err)
		assert.Equal(t, "flag.example.com", host)
		assert.Equal(t, "flag-token", token)
	})

	t.Run("invalid MR IIDs", func(t *testing.T) {
		t.Setenv("GLMT_CONFIG", filepath.Join(t.TempDir(), "nonexistent.toml"))
		_, _, _, _, err := prepareNonInteractive("h", "t", 1, "abc")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid MR IID")
	})
}

// --- runTrain integration tests ---

func TestRunTrain(t *testing.T) {
	t.Run("merges all MRs", func(t *testing.T) {
		mock := &train.MockClient{}
		mock.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
			return &gitlab.MergeRequest{
				IID:                 mrIID,
				SHA:                 fmt.Sprintf("sha-%d", mrIID),
				TargetBranch:        "main",
				DetailedMergeStatus: "mergeable",
			}, nil
		}
		mock.ListPipelinesFn = func(_ context.Context, _ int, ref, _, sha string) ([]*gitlab.Pipeline, error) {
			if ref == "main" {
				return []*gitlab.Pipeline{
					{ID: 100, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/100"},
				}, nil
			}
			return nil, nil
		}

		cfg := config.DefaultConfig()
		cfg.Behavior.PollRebaseIntervalS = 0
		cfg.Behavior.PollPipelineIntervalS = 1
		cfg.Behavior.MainPipelineTimeoutM = 1

		output := captureStdout(t, func() {
			err := runTrain(mock, 1, []int{10, 20}, cfg, false, "")
			require.NoError(t, err)
		})

		assert.Contains(t, output, "MR !10: merged")
		assert.Contains(t, output, "MR !20: merged")

		// Verify GetMergeRequest was called for each IID
		// (2 calls from runTrain fetch + 2 from train runner re-fetching during processing)
		getMRCalls := mock.CallsTo("GetMergeRequest")
		assert.GreaterOrEqual(t, len(getMRCalls), 2)
	})

	t.Run("fetch MR error", func(t *testing.T) {
		mock := &train.MockClient{}
		mock.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
			return nil, fmt.Errorf("not found")
		}

		cfg := config.DefaultConfig()
		cfg.Behavior.PollPipelineIntervalS = 1
		cfg.Behavior.MainPipelineTimeoutM = 1

		captureStdout(t, func() {
			err := runTrain(mock, 1, []int{99}, cfg, false, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "fetching MR !99")
		})
	})

	t.Run("partial merge returns error", func(t *testing.T) {
		mock := &train.MockClient{}
		callCount := 0
		mock.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
			return &gitlab.MergeRequest{
				IID:                 mrIID,
				SHA:                 fmt.Sprintf("sha-%d", mrIID),
				TargetBranch:        "main",
				DetailedMergeStatus: "mergeable",
			}, nil
		}
		mock.RebaseMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
			callCount++
			if callCount > 1 {
				return nil, fmt.Errorf("rebase conflict")
			}
			return &gitlab.MergeRequest{IID: mrIID, SHA: fmt.Sprintf("sha-%d", mrIID)}, nil
		}
		mock.ListPipelinesFn = func(_ context.Context, _ int, ref, _, _ string) ([]*gitlab.Pipeline, error) {
			if ref == "main" {
				return []*gitlab.Pipeline{
					{ID: 100, Status: "success", Ref: "main"},
				}, nil
			}
			return nil, nil
		}

		cfg := config.DefaultConfig()
		cfg.Behavior.PollRebaseIntervalS = 0
		cfg.Behavior.PollPipelineIntervalS = 1
		cfg.Behavior.MainPipelineTimeoutM = 1

		captureStdout(t, func() {
			err := runTrain(mock, 1, []int{10, 20}, cfg, false, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not all MRs were merged")
		})
	})

	t.Run("with file logging", func(t *testing.T) {
		mock := &train.MockClient{}
		mock.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
			return &gitlab.MergeRequest{
				IID:                 mrIID,
				SHA:                 fmt.Sprintf("sha-%d", mrIID),
				TargetBranch:        "main",
				DetailedMergeStatus: "mergeable",
			}, nil
		}
		mock.ListPipelinesFn = func(_ context.Context, _ int, ref, _, _ string) ([]*gitlab.Pipeline, error) {
			if ref == "main" {
				return []*gitlab.Pipeline{
					{ID: 100, Status: "success", Ref: "main"},
				}, nil
			}
			return nil, nil
		}

		cfg := config.DefaultConfig()
		cfg.Behavior.PollRebaseIntervalS = 0
		cfg.Behavior.PollPipelineIntervalS = 1
		cfg.Behavior.MainPipelineTimeoutM = 1

		logDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", logDir)

		captureStdout(t, func() {
			err := runTrain(mock, 1, []int{10}, cfg, true, "test-token")
			require.NoError(t, err)
		})

		// Verify a log file was created
		entries, err := os.ReadDir(filepath.Join(logDir, "glmt"))
		require.NoError(t, err)
		assert.NotEmpty(t, entries, "expected log file in state dir")
	})
}
