package log

import (
	"context"
	"time"

	"github.com/sairus2k/glmt/internal/gitlab"
)

// Compile-time check that LoggingClient implements gitlab.Client.
var _ gitlab.Client = (*LoggingClient)(nil)

// LoggingClient wraps a gitlab.Client and logs every API call.
type LoggingClient struct {
	inner  gitlab.Client
	logger *FileLogger
}

// NewLoggingClient creates a new LoggingClient decorator.
func NewLoggingClient(inner gitlab.Client, logger *FileLogger) *LoggingClient {
	return &LoggingClient{inner: inner, logger: logger}
}

func (c *LoggingClient) logCall(method string, args map[string]any, err error, start time.Time) {
	c.logger.LogAPI(method, args, err == nil, err, time.Since(start))
}

func (c *LoggingClient) GetCurrentUser(ctx context.Context) (*gitlab.User, error) {
	start := time.Now()
	result, err := c.inner.GetCurrentUser(ctx)
	c.logCall("GetCurrentUser", nil, err, start)
	return result, err
}

func (c *LoggingClient) ListProjects(ctx context.Context, search string) ([]*gitlab.Project, error) {
	start := time.Now()
	result, err := c.inner.ListProjects(ctx, search)
	c.logCall("ListProjects", map[string]any{"search": search}, err, start)
	return result, err
}

func (c *LoggingClient) ListMergeRequestsFull(ctx context.Context, projectPath string) ([]*gitlab.MergeRequest, error) {
	start := time.Now()
	result, err := c.inner.ListMergeRequestsFull(ctx, projectPath)
	c.logCall("ListMergeRequestsFull", map[string]any{"project_path": projectPath}, err, start)
	return result, err
}

func (c *LoggingClient) GetMergeRequest(ctx context.Context, projectID, mrIID int) (*gitlab.MergeRequest, error) {
	start := time.Now()
	result, err := c.inner.GetMergeRequest(ctx, projectID, mrIID)
	c.logCall("GetMergeRequest", map[string]any{"project_id": projectID, "mr_iid": mrIID}, err, start)
	return result, err
}

func (c *LoggingClient) RebaseMergeRequest(ctx context.Context, projectID, mrIID int) (*gitlab.MergeRequest, error) {
	start := time.Now()
	result, err := c.inner.RebaseMergeRequest(ctx, projectID, mrIID)
	c.logCall("RebaseMergeRequest", map[string]any{"project_id": projectID, "mr_iid": mrIID}, err, start)
	return result, err
}

func (c *LoggingClient) MergeMergeRequest(ctx context.Context, projectID, mrIID int, sha string) (string, error) {
	start := time.Now()
	mergeCommitSHA, err := c.inner.MergeMergeRequest(ctx, projectID, mrIID, sha)
	c.logCall("MergeMergeRequest", map[string]any{"project_id": projectID, "mr_iid": mrIID}, err, start)
	return mergeCommitSHA, err
}

func (c *LoggingClient) GetMergeRequestPipeline(ctx context.Context, projectID, mrIID int) (*gitlab.Pipeline, string, error) {
	start := time.Now()
	p, status, err := c.inner.GetMergeRequestPipeline(ctx, projectID, mrIID)
	c.logCall("GetMergeRequestPipeline", map[string]any{"project_id": projectID, "mr_iid": mrIID}, err, start)
	return p, status, err
}

func (c *LoggingClient) ListPipelines(ctx context.Context, projectID int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
	start := time.Now()
	result, err := c.inner.ListPipelines(ctx, projectID, ref, status, sha)
	c.logCall("ListPipelines", map[string]any{"project_id": projectID, "ref": ref, "status": status, "sha": sha}, err, start)
	return result, err
}
