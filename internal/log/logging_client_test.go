package log

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockClient struct {
	getCalled    bool
	rebaseCalled bool
}

func (m *mockClient) GetCurrentUser(_ context.Context) (*gitlab.User, error) {
	m.getCalled = true
	return &gitlab.User{ID: 1, Username: "test"}, nil
}
func (m *mockClient) ListProjects(_ context.Context, _ string) ([]*gitlab.Project, error) {
	return nil, nil
}
func (m *mockClient) ListMergeRequestsFull(_ context.Context, _ string) ([]*gitlab.MergeRequest, error) {
	return nil, nil
}
func (m *mockClient) GetMergeRequest(_ context.Context, _, mrIID int) (*gitlab.MergeRequest, error) {
	return &gitlab.MergeRequest{IID: mrIID}, nil
}
func (m *mockClient) RebaseMergeRequest(_ context.Context, _, mrIID int) (*gitlab.MergeRequest, error) {
	m.rebaseCalled = true
	return &gitlab.MergeRequest{IID: mrIID}, nil
}
func (m *mockClient) MergeMergeRequest(_ context.Context, _, _ int, _ string) (string, error) {
	return "", nil
}
func (m *mockClient) GetMergeRequestPipeline(_ context.Context, _, _ int) (*gitlab.Pipeline, string, error) {
	return nil, "", nil
}
func (m *mockClient) ListPipelines(_ context.Context, _ int, _, _, _ string) ([]*gitlab.Pipeline, error) {
	return nil, nil
}
func TestLoggingClient_DelegatesAndLogs(t *testing.T) {
	dir := t.TempDir()
	fl, err := NewFileLogger(dir, "tok")
	require.NoError(t, err)
	defer fl.Close()

	mock := &mockClient{}
	lc := NewLoggingClient(mock, fl)

	ctx := context.Background()
	_, err = lc.GetCurrentUser(ctx)
	require.NoError(t, err)
	assert.True(t, mock.getCalled)

	_, err = lc.RebaseMergeRequest(ctx, 123, 42)
	require.NoError(t, err)
	assert.True(t, mock.rebaseCalled)

	fl.Close()

	// Verify log file has API entries
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 2)

	var entry1 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry1))
	assert.Equal(t, "GetCurrentUser", entry1["msg"])

	var entry2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &entry2))
	assert.Equal(t, "RebaseMergeRequest", entry2["msg"])
	api := entry2["api"].(map[string]any)
	args := api["args"].(map[string]any)
	assert.InDelta(t, float64(123), args["project_id"], 0)
	assert.InDelta(t, float64(42), args["mr_iid"], 0)
}
