package train

import (
	"context"

	"github.com/sairus2k/glmt/internal/gitlab"
)

// MockCall records a single method call made to the MockClient.
type MockCall struct {
	Method string
	Args   []interface{}
}

// MockClient is a hand-written mock for gitlab.Client used in tests.
type MockClient struct {
	Calls []MockCall // records all calls made

	// Configurable responses per method
	GetCurrentUserFn          func(ctx context.Context) (*gitlab.User, error)
	ListProjectsFn            func(ctx context.Context, search string) ([]*gitlab.Project, error)
	ListMergeRequestsFullFn   func(ctx context.Context, projectPath string) ([]*gitlab.MergeRequest, error)
	GetMergeRequestFn         func(ctx context.Context, projectID, mrIID int) (*gitlab.MergeRequest, error)
	RebaseMergeRequestFn      func(ctx context.Context, projectID, mrIID int) error
	MergeMergeRequestFn       func(ctx context.Context, projectID, mrIID int, sha string) error
	GetMergeRequestPipelineFn func(ctx context.Context, projectID, mrIID int) (*gitlab.Pipeline, error)
	ListPipelinesFn           func(ctx context.Context, projectID int, ref, status string) ([]*gitlab.Pipeline, error)
	CancelPipelineFn          func(ctx context.Context, projectID, pipelineID int) error
	RetryPipelineFn           func(ctx context.Context, projectID, pipelineID int) (*gitlab.Pipeline, error)
}

func (m *MockClient) record(method string, args ...interface{}) {
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
}

func (m *MockClient) GetCurrentUser(ctx context.Context) (*gitlab.User, error) {
	m.record("GetCurrentUser")
	if m.GetCurrentUserFn != nil {
		return m.GetCurrentUserFn(ctx)
	}
	return &gitlab.User{}, nil
}

func (m *MockClient) ListProjects(ctx context.Context, search string) ([]*gitlab.Project, error) {
	m.record("ListProjects", search)
	if m.ListProjectsFn != nil {
		return m.ListProjectsFn(ctx, search)
	}
	return nil, nil
}

func (m *MockClient) ListMergeRequestsFull(ctx context.Context, projectPath string) ([]*gitlab.MergeRequest, error) {
	m.record("ListMergeRequestsFull", projectPath)
	if m.ListMergeRequestsFullFn != nil {
		return m.ListMergeRequestsFullFn(ctx, projectPath)
	}
	return nil, nil
}

func (m *MockClient) GetMergeRequest(ctx context.Context, projectID, mrIID int) (*gitlab.MergeRequest, error) {
	m.record("GetMergeRequest", projectID, mrIID)
	if m.GetMergeRequestFn != nil {
		return m.GetMergeRequestFn(ctx, projectID, mrIID)
	}
	return &gitlab.MergeRequest{IID: mrIID, DetailedMergeStatus: "mergeable"}, nil
}

func (m *MockClient) RebaseMergeRequest(ctx context.Context, projectID, mrIID int) error {
	m.record("RebaseMergeRequest", projectID, mrIID)
	if m.RebaseMergeRequestFn != nil {
		return m.RebaseMergeRequestFn(ctx, projectID, mrIID)
	}
	return nil
}

func (m *MockClient) MergeMergeRequest(ctx context.Context, projectID, mrIID int, sha string) error {
	m.record("MergeMergeRequest", projectID, mrIID, sha)
	if m.MergeMergeRequestFn != nil {
		return m.MergeMergeRequestFn(ctx, projectID, mrIID, sha)
	}
	return nil
}

func (m *MockClient) GetMergeRequestPipeline(ctx context.Context, projectID, mrIID int) (*gitlab.Pipeline, error) {
	m.record("GetMergeRequestPipeline", projectID, mrIID)
	if m.GetMergeRequestPipelineFn != nil {
		return m.GetMergeRequestPipelineFn(ctx, projectID, mrIID)
	}
	return &gitlab.Pipeline{Status: "success"}, nil
}

func (m *MockClient) ListPipelines(ctx context.Context, projectID int, ref, status string) ([]*gitlab.Pipeline, error) {
	m.record("ListPipelines", projectID, ref, status)
	if m.ListPipelinesFn != nil {
		return m.ListPipelinesFn(ctx, projectID, ref, status)
	}
	return nil, nil
}

func (m *MockClient) CancelPipeline(ctx context.Context, projectID, pipelineID int) error {
	m.record("CancelPipeline", projectID, pipelineID)
	if m.CancelPipelineFn != nil {
		return m.CancelPipelineFn(ctx, projectID, pipelineID)
	}
	return nil
}

func (m *MockClient) RetryPipeline(ctx context.Context, projectID, pipelineID int) (*gitlab.Pipeline, error) {
	m.record("RetryPipeline", projectID, pipelineID)
	if m.RetryPipelineFn != nil {
		return m.RetryPipelineFn(ctx, projectID, pipelineID)
	}
	return &gitlab.Pipeline{}, nil
}

// CallsTo returns all calls to the given method name.
func (m *MockClient) CallsTo(method string) []MockCall {
	var result []MockCall
	for _, c := range m.Calls {
		if c.Method == method {
			result = append(result, c)
		}
	}
	return result
}

// MethodNames returns the ordered list of method names called.
func (m *MockClient) MethodNames() []string {
	names := make([]string, len(m.Calls))
	for i, c := range m.Calls {
		names[i] = c.Method
	}
	return names
}
