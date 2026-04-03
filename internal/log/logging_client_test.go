package log

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockClient struct {
	calls []string

	GetCurrentUserFn          func(context.Context) (*gitlab.User, error)
	ListProjectsFn            func(context.Context, string) ([]*gitlab.Project, error)
	ListMergeRequestsFullFn   func(context.Context, string) ([]*gitlab.MergeRequest, error)
	GetMergeRequestFn         func(context.Context, int, int) (*gitlab.MergeRequest, error)
	RebaseMergeRequestFn      func(context.Context, int, int) (*gitlab.MergeRequest, error)
	MergeMergeRequestFn       func(context.Context, int, int, string) (string, error)
	GetMergeRequestPipelineFn func(context.Context, int, int) (*gitlab.Pipeline, string, error)
	ListPipelinesFn           func(context.Context, int, string, string, string) ([]*gitlab.Pipeline, error)
}

func (m *mockClient) called(method string) bool {
	for _, c := range m.calls {
		if c == method {
			return true
		}
	}
	return false
}

func (m *mockClient) GetCurrentUser(ctx context.Context) (*gitlab.User, error) {
	m.calls = append(m.calls, "GetCurrentUser")
	if m.GetCurrentUserFn != nil {
		return m.GetCurrentUserFn(ctx)
	}
	return &gitlab.User{ID: 1, Username: "test"}, nil
}

func (m *mockClient) ListProjects(ctx context.Context, search string) ([]*gitlab.Project, error) {
	m.calls = append(m.calls, "ListProjects")
	if m.ListProjectsFn != nil {
		return m.ListProjectsFn(ctx, search)
	}
	return []*gitlab.Project{{ID: 1}}, nil
}

func (m *mockClient) ListMergeRequestsFull(ctx context.Context, projectPath string) ([]*gitlab.MergeRequest, error) {
	m.calls = append(m.calls, "ListMergeRequestsFull")
	if m.ListMergeRequestsFullFn != nil {
		return m.ListMergeRequestsFullFn(ctx, projectPath)
	}
	return []*gitlab.MergeRequest{{IID: 1}}, nil
}

func (m *mockClient) GetMergeRequest(ctx context.Context, projectID, mrIID int) (*gitlab.MergeRequest, error) {
	m.calls = append(m.calls, "GetMergeRequest")
	if m.GetMergeRequestFn != nil {
		return m.GetMergeRequestFn(ctx, projectID, mrIID)
	}
	return &gitlab.MergeRequest{IID: mrIID}, nil
}

func (m *mockClient) RebaseMergeRequest(ctx context.Context, projectID, mrIID int) (*gitlab.MergeRequest, error) {
	m.calls = append(m.calls, "RebaseMergeRequest")
	if m.RebaseMergeRequestFn != nil {
		return m.RebaseMergeRequestFn(ctx, projectID, mrIID)
	}
	return &gitlab.MergeRequest{IID: mrIID}, nil
}

func (m *mockClient) MergeMergeRequest(ctx context.Context, projectID, mrIID int, sha string) (string, error) {
	m.calls = append(m.calls, "MergeMergeRequest")
	if m.MergeMergeRequestFn != nil {
		return m.MergeMergeRequestFn(ctx, projectID, mrIID, sha)
	}
	return "abc123", nil
}

func (m *mockClient) GetMergeRequestPipeline(ctx context.Context, projectID, mrIID int) (*gitlab.Pipeline, string, error) {
	m.calls = append(m.calls, "GetMergeRequestPipeline")
	if m.GetMergeRequestPipelineFn != nil {
		return m.GetMergeRequestPipelineFn(ctx, projectID, mrIID)
	}
	return &gitlab.Pipeline{ID: 99, Status: "success"}, "mergeable", nil
}

func (m *mockClient) ListPipelines(ctx context.Context, projectID int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
	m.calls = append(m.calls, "ListPipelines")
	if m.ListPipelinesFn != nil {
		return m.ListPipelinesFn(ctx, projectID, ref, status, sha)
	}
	return []*gitlab.Pipeline{{ID: 1}}, nil
}

// readLogEntries closes the logger and returns all parsed JSONL entries.
func readLogEntries(t *testing.T, dir string, fl *FileLogger) []map[string]any {
	t.Helper()
	fl.Close()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	result := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		result = append(result, entry)
	}
	return result
}

func TestLoggingClient_DelegatesAndLogs(t *testing.T) {
	dir := t.TempDir()
	fl, err := NewFileLogger(dir, "tok")
	require.NoError(t, err)

	mock := &mockClient{}
	lc := NewLoggingClient(mock, fl)

	ctx := context.Background()
	_, err = lc.GetCurrentUser(ctx)
	require.NoError(t, err)
	assert.True(t, mock.called("GetCurrentUser"))

	_, err = lc.RebaseMergeRequest(ctx, 123, 42)
	require.NoError(t, err)
	assert.True(t, mock.called("RebaseMergeRequest"))

	logEntries := readLogEntries(t, dir, fl)
	require.Len(t, logEntries, 2)

	assert.Equal(t, "GetCurrentUser", logEntries[0]["msg"])

	assert.Equal(t, "RebaseMergeRequest", logEntries[1]["msg"])
	api := logEntries[1]["api"].(map[string]any)
	args := api["args"].(map[string]any)
	assert.InDelta(t, float64(123), args["project_id"], 0)
	assert.InDelta(t, float64(42), args["mr_iid"], 0)
}

func TestLoggingClient_AllMethods(t *testing.T) {
	tests := []struct {
		name         string
		call         func(lc *LoggingClient)
		expectedArgs map[string]any
	}{
		{
			name: "ListProjects",
			call: func(lc *LoggingClient) {
				_, _ = lc.ListProjects(context.Background(), "foo")
			},
			expectedArgs: map[string]any{"search": "foo"},
		},
		{
			name: "ListMergeRequestsFull",
			call: func(lc *LoggingClient) {
				_, _ = lc.ListMergeRequestsFull(context.Background(), "g/p")
			},
			expectedArgs: map[string]any{"project_path": "g/p"},
		},
		{
			name: "GetMergeRequest",
			call: func(lc *LoggingClient) {
				_, _ = lc.GetMergeRequest(context.Background(), 10, 5)
			},
			expectedArgs: map[string]any{"project_id": float64(10), "mr_iid": float64(5)},
		},
		{
			name: "MergeMergeRequest",
			call: func(lc *LoggingClient) {
				_, _ = lc.MergeMergeRequest(context.Background(), 10, 5, "secret-sha")
			},
			expectedArgs: map[string]any{"project_id": float64(10), "mr_iid": float64(5)},
		},
		{
			name: "GetMergeRequestPipeline",
			call: func(lc *LoggingClient) {
				_, _, _ = lc.GetMergeRequestPipeline(context.Background(), 10, 5)
			},
			expectedArgs: map[string]any{"project_id": float64(10), "mr_iid": float64(5)},
		},
		{
			name: "ListPipelines",
			call: func(lc *LoggingClient) {
				_, _ = lc.ListPipelines(context.Background(), 10, "main", "success", "abc")
			},
			expectedArgs: map[string]any{
				"project_id": float64(10),
				"ref":        "main",
				"status":     "success",
				"sha":        "abc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			fl, err := NewFileLogger(dir, "tok")
			require.NoError(t, err)

			mock := &mockClient{}
			lc := NewLoggingClient(mock, fl)

			tt.call(lc)

			assert.True(t, mock.called(tt.name), "expected %s to be called", tt.name)

			logEntries := readLogEntries(t, dir, fl)
			require.Len(t, logEntries, 1)

			entry := logEntries[0]
			assert.Equal(t, tt.name, entry["msg"])

			api, ok := entry["api"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, true, api["ok"])
			assert.Equal(t, tt.name, api["method"])

			args, ok := api["args"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tt.expectedArgs, args)
		})
	}
}

func TestLoggingClient_ErrorPropagationAndScrubbing(t *testing.T) {
	const token = "glpat-secret"
	dir := t.TempDir()
	fl, err := NewFileLogger(dir, token)
	require.NoError(t, err)

	injectedErr := errors.New("401: token glpat-secret is invalid")

	mock := &mockClient{
		GetMergeRequestFn: func(_ context.Context, _, _ int) (*gitlab.MergeRequest, error) {
			return nil, injectedErr
		},
	}
	lc := NewLoggingClient(mock, fl)

	_, err = lc.GetMergeRequest(context.Background(), 10, 5)

	// Error is propagated, not swallowed
	require.Error(t, err)
	assert.Equal(t, injectedErr, err)

	assert.True(t, mock.called("GetMergeRequest"))

	logEntries := readLogEntries(t, dir, fl)
	require.Len(t, logEntries, 1)

	api, ok := logEntries[0]["api"].(map[string]any)
	require.True(t, ok)

	// Log records failure
	assert.Equal(t, false, api["ok"])

	// Token is scrubbed from logged error
	loggedErr, ok := api["error"].(string)
	require.True(t, ok)
	assert.NotContains(t, loggedErr, token)
	assert.Contains(t, loggedErr, "[REDACTED]")
	assert.Equal(t, "401: token [REDACTED] is invalid", loggedErr)
}
