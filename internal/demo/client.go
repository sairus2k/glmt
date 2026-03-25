package demo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sairus2k/glmt/internal/gitlab"
)

const rebasedSHAFmt = "rebased-sha-%d"

// Client implements gitlab.Client with canned data and artificial delays
// for recording demo GIFs with VHS.
type Client struct {
	mu                sync.Mutex
	getMRCalls        map[int]int // per-MR call count for GetMergeRequest
	listPipelineCalls int         // tracks calls to ListPipelines for stateful progression
}

// NewClient creates a new demo client.
func NewClient() *Client {
	return &Client{
		getMRCalls: make(map[int]int),
	}
}

func (c *Client) GetCurrentUser(_ context.Context) (*gitlab.User, error) {
	return &gitlab.User{
		ID:       1,
		Username: "alexchen",
		Name:     "Alex Chen",
	}, nil
}

func (c *Client) ListProjects(_ context.Context, _ string) ([]*gitlab.Project, error) {
	return []*gitlab.Project{
		{ID: 42, PathWithNamespace: "team/backend-api", WebURL: "https://gitlab.example.com/team/backend-api"},
		{ID: 43, PathWithNamespace: "team/frontend-app", WebURL: "https://gitlab.example.com/team/frontend-app"},
		{ID: 44, PathWithNamespace: "team/mobile-app", WebURL: "https://gitlab.example.com/team/mobile-app"},
		{ID: 45, PathWithNamespace: "team/infra", WebURL: "https://gitlab.example.com/team/infra"},
		{ID: 46, PathWithNamespace: "team/docs", WebURL: "https://gitlab.example.com/team/docs"},
	}, nil
}

func (c *Client) ListMergeRequestsFull(_ context.Context, _ string) ([]*gitlab.MergeRequest, error) {
	return []*gitlab.MergeRequest{
		{
			IID:                         147,
			State:                       "opened",
			Title:                       "Fix rate limiter edge case on burst traffic",
			Author:                      "alexchen",
			SourceBranch:                "fix/rate-limiter-burst",
			TargetBranch:                "main",
			SHA:                         "a1b2c3d",
			CommitCount:                 2,
			ApprovalCount:               2,
			HeadPipelineStatus:          "success",
			DetailedMergeStatus:         "mergeable",
			BlockingDiscussionsResolved: true,
			WebURL:                      "https://gitlab.example.com/team/backend-api/-/merge_requests/147",
		},
		{
			IID:                         145,
			State:                       "opened",
			Title:                       "Add pagination to /users endpoint",
			Author:                      "mwong",
			SourceBranch:                "feat/users-pagination",
			TargetBranch:                "main",
			SHA:                         "d4e5f6a",
			CommitCount:                 1,
			ApprovalCount:               1,
			HeadPipelineStatus:          "success",
			DetailedMergeStatus:         "mergeable",
			BlockingDiscussionsResolved: true,
			WebURL:                      "https://gitlab.example.com/team/backend-api/-/merge_requests/145",
		},
		{
			IID:                         142,
			State:                       "opened",
			Title:                       "Refactor auth middleware for JWT rotation",
			Author:                      "jgarcia",
			SourceBranch:                "refactor/jwt-rotation",
			TargetBranch:                "main",
			SHA:                         "b7c8d9e",
			CommitCount:                 3,
			ApprovalCount:               2,
			HeadPipelineStatus:          "success",
			DetailedMergeStatus:         "mergeable",
			BlockingDiscussionsResolved: true,
			WebURL:                      "https://gitlab.example.com/team/backend-api/-/merge_requests/142",
		},
		{
			IID:                         149,
			State:                       "opened",
			Title:                       "WIP: Add metrics endpoint",
			Author:                      "alexchen",
			SourceBranch:                "feat/metrics",
			TargetBranch:                "main",
			SHA:                         "e0f1a2b",
			CommitCount:                 1,
			Draft:                       true,
			HeadPipelineStatus:          "success",
			DetailedMergeStatus:         "draft",
			BlockingDiscussionsResolved: true,
			WebURL:                      "https://gitlab.example.com/team/backend-api/-/merge_requests/149",
		},
		{
			IID:                         144,
			State:                       "opened",
			Title:                       "Update deployment scripts",
			Author:                      "psingh",
			SourceBranch:                "chore/deploy-scripts",
			TargetBranch:                "main",
			SHA:                         "c3d4e5f",
			CommitCount:                 1,
			HeadPipelineStatus:          "running",
			DetailedMergeStatus:         "not_approved",
			BlockingDiscussionsResolved: true,
			WebURL:                      "https://gitlab.example.com/team/backend-api/-/merge_requests/144",
		},
	}, nil
}

func (c *Client) GetMergeRequest(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
	c.mu.Lock()
	c.getMRCalls[mrIID]++
	call := c.getMRCalls[mrIID]
	c.mu.Unlock()

	time.Sleep(300 * time.Millisecond)

	// First call per MR returns "checking" to show the merge readiness polling step.
	// Second call returns "mergeable" so the merge proceeds.
	status := "mergeable"
	if call == 1 {
		status = "checking"
	}

	return &gitlab.MergeRequest{
		IID:                 mrIID,
		State:               "opened",
		SHA:                 fmt.Sprintf(rebasedSHAFmt, mrIID),
		DetailedMergeStatus: status,
		TargetBranch:        "main",
	}, nil
}

func (c *Client) RebaseMergeRequest(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
	time.Sleep(1200 * time.Millisecond)
	return &gitlab.MergeRequest{
		IID:          mrIID,
		SHA:          fmt.Sprintf(rebasedSHAFmt, mrIID),
		TargetBranch: "main",
	}, nil
}

func (c *Client) MergeMergeRequest(_ context.Context, _ int, _ int, _ string) (string, error) {
	time.Sleep(1 * time.Second)
	return fmt.Sprintf("merge-commit-%x", time.Now().UnixNano()&0xffffff), nil
}

func (c *Client) GetMergeRequestPipeline(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
	return &gitlab.Pipeline{
		ID:     mrIID*10 + 1,
		Status: "success",
		SHA:    fmt.Sprintf(rebasedSHAFmt, mrIID),
		WebURL: fmt.Sprintf("https://gitlab.example.com/team/backend-api/-/pipelines/%d", mrIID*10+1),
	}, "mergeable", nil
}

func (c *Client) ListPipelines(_ context.Context, _ int, _ string, _ string, _ string) ([]*gitlab.Pipeline, error) {
	c.mu.Lock()
	c.listPipelineCalls++
	call := c.listPipelineCalls
	c.mu.Unlock()

	// Show "running" for a few poll cycles so the pipeline spinner is visible.
	status := "running"
	if call >= 3 {
		status = "success"
	}

	return []*gitlab.Pipeline{
		{
			ID:     9001,
			Status: status,
			Ref:    "main",
			SHA:    "final-sha",
			WebURL: "https://gitlab.example.com/team/backend-api/-/pipelines/9001",
		},
	}, nil
}

func (c *Client) CancelPipeline(_ context.Context, _ int, _ int) error {
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (c *Client) RetryPipeline(_ context.Context, _ int, _ int) (*gitlab.Pipeline, error) {
	time.Sleep(100 * time.Millisecond)
	return &gitlab.Pipeline{ID: 9002, Status: "running", Ref: "main", WebURL: "https://gitlab.example.com/team/backend-api/-/pipelines/9002"}, nil
}
