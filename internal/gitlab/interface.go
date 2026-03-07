package gitlab

import "context"

// MergeRequest represents a GitLab merge request with fields needed for train execution.
type MergeRequest struct {
	IID                         int
	Title                       string
	Author                      string
	SourceBranch                string
	TargetBranch                string
	SHA                         string // head SHA
	CreatedAt                   string
	CommitCount                 int
	Draft                       bool
	ApprovalsLeft               int
	HeadPipelineStatus          string // success, failed, running, pending, canceled, skipped, created
	DetailedMergeStatus         string // mergeable, checking, unchecked, etc.
	BlockingDiscussionsResolved bool
	WebURL                      string
}

// Pipeline represents a GitLab CI pipeline.
type Pipeline struct {
	ID     int
	Status string // running, success, failed, canceled, pending, created, skipped
	Ref    string
	SHA    string
	WebURL string
}

// Project represents a GitLab project.
type Project struct {
	ID                int
	PathWithNamespace string
	WebURL            string
}

// User represents a GitLab user.
type User struct {
	ID       int
	Username string
	Name     string
}

// Client defines the interface for GitLab API operations used by glmt.
// This is the key seam for testing — the train runner and TUI depend only on this interface.
type Client interface {
	// GetCurrentUser returns the authenticated user. Used to validate credentials.
	GetCurrentUser(ctx context.Context) (*User, error)

	// ListProjects returns projects accessible to the current user.
	ListProjects(ctx context.Context, search string) ([]*Project, error)

	// ListMergeRequests returns open merge requests for a project.
	ListMergeRequests(ctx context.Context, projectID int) ([]*MergeRequest, error)

	// ListMergeRequestsFull returns open merge requests with all fields (including
	// pipeline status and commit count) via a single GraphQL query.
	// projectPath is the full path (e.g. "team/project").
	ListMergeRequestsFull(ctx context.Context, projectPath string) ([]*MergeRequest, error)

	// GetMergeRequest returns a single merge request with full detail.
	GetMergeRequest(ctx context.Context, projectID, mrIID int) (*MergeRequest, error)

	// RebaseMergeRequest triggers a rebase of the MR onto its target branch.
	// Returns nil on success. Returns an error if a rebase conflict occurs.
	RebaseMergeRequest(ctx context.Context, projectID, mrIID int) error

	// MergeMergeRequest merges the MR with a SHA guard.
	// sha is the expected head SHA — the server returns 409 if it doesn't match.
	// Returns nil on success.
	MergeMergeRequest(ctx context.Context, projectID, mrIID int, sha string) error

	// GetMergeRequestPipeline returns the head pipeline for a merge request.
	GetMergeRequestPipeline(ctx context.Context, projectID, mrIID int) (*Pipeline, error)

	// ListPipelines returns pipelines for a ref, ordered by ID descending.
	ListPipelines(ctx context.Context, projectID int, ref, status string) ([]*Pipeline, error)

	// CancelPipeline cancels a running pipeline.
	CancelPipeline(ctx context.Context, projectID, pipelineID int) error

	// RetryPipeline retries (restarts) a pipeline.
	RetryPipeline(ctx context.Context, projectID, pipelineID int) (*Pipeline, error)
}
