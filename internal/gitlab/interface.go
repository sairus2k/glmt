package gitlab

import (
	"context"
	"errors"
)

// ErrSHAMismatch is returned when a merge fails due to SHA mismatch (HTTP 409).
var ErrSHAMismatch = errors.New("SHA mismatch")

// MergeRequest represents a GitLab merge request with fields needed for train execution.
type MergeRequest struct {
	IID                         int
	State                       string // opened, merged, closed
	Title                       string
	Author                      string
	SourceBranch                string
	TargetBranch                string
	SHA                         string // head SHA
	CreatedAt                   string
	CommitCount                 int
	Draft                       bool
	ApprovalCount               int
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

	// ListMergeRequestsFull returns open merge requests with all fields (including
	// pipeline status and commit count) via a single GraphQL query.
	// projectPath is the full path (e.g. "team/project").
	ListMergeRequestsFull(ctx context.Context, projectPath string) ([]*MergeRequest, error)

	// GetMergeRequest returns a single merge request with full detail.
	GetMergeRequest(ctx context.Context, projectID, mrIID int) (*MergeRequest, error)

	// RebaseMergeRequest triggers a rebase of the MR onto its target branch.
	// Returns the updated MR (with post-rebase SHA) on success.
	// Returns an error if a rebase conflict occurs.
	// When skipCI is true, the branch pipeline after rebase is skipped.
	RebaseMergeRequest(ctx context.Context, projectID, mrIID int, skipCI bool) (*MergeRequest, error)

	// MergeMergeRequest merges the MR with a SHA guard.
	// sha is the expected head SHA — the server returns 409 if it doesn't match.
	// Returns nil on success.
	MergeMergeRequest(ctx context.Context, projectID, mrIID int, sha string) error

	// GetMergeRequestPipeline returns the head pipeline for a merge request,
	// along with the MR's DetailedMergeStatus.
	GetMergeRequestPipeline(ctx context.Context, projectID, mrIID int) (*Pipeline, string, error)

	// ListPipelines returns pipelines for a ref, ordered by ID descending.
	ListPipelines(ctx context.Context, projectID int, ref, status string) ([]*Pipeline, error)

	// CancelPipeline cancels a running pipeline.
	CancelPipeline(ctx context.Context, projectID, pipelineID int) error

	// RetryPipeline retries (restarts) a pipeline.
	RetryPipeline(ctx context.Context, projectID, pipelineID int) (*Pipeline, error)
}
