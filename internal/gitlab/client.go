package gitlab

import (
	"context"
	"fmt"
	"strings"
	"time"

	goGitLab "gitlab.com/gitlab-org/api/client-go"
)

// APIClient implements the Client interface using the go-gitlab library.
type APIClient struct {
	client             *goGitLab.Client
	projectID          int
	rebasePollInterval time.Duration
}

// NewAPIClient creates a new GitLab API client.
// host should be a base URL like "https://gitlab.example.com" or just a hostname.
func NewAPIClient(host, token string) (*APIClient, error) {
	host = normalizeBaseURL(host)
	client, err := goGitLab.NewClient(token, goGitLab.WithBaseURL(host+"/api/v4"))
	if err != nil {
		return nil, fmt.Errorf("creating GitLab client: %w", err)
	}
	return &APIClient{client: client, rebasePollInterval: 2 * time.Second}, nil
}

// normalizeBaseURL ensures the host is a clean URL with scheme.
// Preserves http:// if explicitly provided, defaults to https://.
func normalizeBaseURL(host string) string {
	scheme := "https"
	if strings.HasPrefix(host, "http://") {
		scheme = "http"
	}
	// Strip any existing scheme variants
	for _, prefix := range []string{"https://", "http://", "https//", "http//"} {
		host = strings.TrimPrefix(host, prefix)
	}
	host = strings.TrimRight(host, "/")
	return scheme + "://" + host
}

// SetProject sets the project ID for subsequent API calls.
func (c *APIClient) SetProject(id int) {
	c.projectID = id
}

// GetCurrentUser returns the authenticated user.
func (c *APIClient) GetCurrentUser(ctx context.Context) (*User, error) {
	u, _, err := c.client.Users.CurrentUser(goGitLab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("getting current user: %w", err)
	}
	return &User{
		ID:       int(u.ID),
		Username: u.Username,
		Name:     u.Name,
	}, nil
}

// ListProjects returns projects accessible to the current user.
func (c *APIClient) ListProjects(ctx context.Context, search string) ([]*Project, error) {
	opts := &goGitLab.ListProjectsOptions{
		Membership: goGitLab.Ptr(true),
		OrderBy:    goGitLab.Ptr("name"),
		Sort:       goGitLab.Ptr("asc"),
	}
	if search != "" {
		opts.Search = goGitLab.Ptr(search)
	}

	projects, _, err := c.client.Projects.ListProjects(opts, goGitLab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}

	result := make([]*Project, len(projects))
	for i, p := range projects {
		result[i] = &Project{
			ID:                int(p.ID),
			PathWithNamespace: p.PathWithNamespace,
			WebURL:            p.WebURL,
		}
	}
	return result, nil
}

// ListMergeRequests returns open merge requests for a project.
func (c *APIClient) ListMergeRequests(ctx context.Context, projectID int) ([]*MergeRequest, error) {
	withRecheck := true
	opts := &goGitLab.ListProjectMergeRequestsOptions{
		State:                  goGitLab.Ptr("opened"),
		OrderBy:                goGitLab.Ptr("created_at"),
		Sort:                   goGitLab.Ptr("asc"),
		WithMergeStatusRecheck: &withRecheck,
	}

	basicMRs, _, err := c.client.MergeRequests.ListProjectMergeRequests(projectID, opts, goGitLab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("listing merge requests: %w", err)
	}

	result := make([]*MergeRequest, len(basicMRs))
	for i, bmr := range basicMRs {
		createdAt := ""
		if bmr.CreatedAt != nil {
			createdAt = bmr.CreatedAt.Format(time.RFC3339)
		}
		author := ""
		if bmr.Author != nil {
			author = bmr.Author.Username
		}
		result[i] = &MergeRequest{
			IID:                         int(bmr.IID),
			Title:                       bmr.Title,
			Author:                      author,
			SourceBranch:                bmr.SourceBranch,
			TargetBranch:                bmr.TargetBranch,
			SHA:                         bmr.SHA,
			CreatedAt:                   createdAt,
			Draft:                       bmr.Draft,
			DetailedMergeStatus:         bmr.DetailedMergeStatus,
			BlockingDiscussionsResolved: bmr.BlockingDiscussionsResolved,
			WebURL:                      bmr.WebURL,
		}
	}
	return result, nil
}

// GetMergeRequest returns a single merge request with full detail.
func (c *APIClient) GetMergeRequest(ctx context.Context, projectID, mrIID int) (*MergeRequest, error) {
	mr, _, err := c.client.MergeRequests.GetMergeRequest(int64(projectID), int64(mrIID), nil, goGitLab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("getting merge request %d: %w", mrIID, err)
	}
	return convertMergeRequest(mr), nil
}

// RebaseMergeRequest triggers a rebase of the MR onto its target branch.
func (c *APIClient) RebaseMergeRequest(ctx context.Context, projectID, mrIID int) error {
	_, err := c.client.MergeRequests.RebaseMergeRequest(int64(projectID), int64(mrIID), nil, goGitLab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("triggering rebase for MR %d: %w", mrIID, err)
	}

	// Poll until rebase completes.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.rebasePollInterval):
		}

		opts := &goGitLab.GetMergeRequestsOptions{
			IncludeRebaseInProgress: goGitLab.Ptr(true),
		}
		mr, _, err := c.client.MergeRequests.GetMergeRequest(int64(projectID), int64(mrIID), opts, goGitLab.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("polling rebase status for MR %d: %w", mrIID, err)
		}

		if mr.MergeError != "" && strings.Contains(mr.MergeError, "conflict") {
			return fmt.Errorf("rebase conflict for MR %d: %s", mrIID, mr.MergeError)
		}

		if !mr.RebaseInProgress {
			return nil
		}
	}
}

// MergeMergeRequest merges the MR with a SHA guard.
func (c *APIClient) MergeMergeRequest(ctx context.Context, projectID, mrIID int, sha string) error {
	opts := &goGitLab.AcceptMergeRequestOptions{
		SHA:                      goGitLab.Ptr(sha),
		ShouldRemoveSourceBranch: goGitLab.Ptr(true),
	}

	_, _, err := c.client.MergeRequests.AcceptMergeRequest(int64(projectID), int64(mrIID), opts, goGitLab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("merging MR %d: %w", mrIID, err)
	}
	return nil
}

// GetMergeRequestPipeline returns the head pipeline for a merge request.
func (c *APIClient) GetMergeRequestPipeline(ctx context.Context, projectID, mrIID int) (*Pipeline, error) {
	mr, _, err := c.client.MergeRequests.GetMergeRequest(int64(projectID), int64(mrIID), nil, goGitLab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("getting MR %d for pipeline: %w", mrIID, err)
	}

	if mr.HeadPipeline == nil {
		return nil, nil
	}

	return &Pipeline{
		ID:     int(mr.HeadPipeline.ID),
		Status: mr.HeadPipeline.Status,
		Ref:    mr.HeadPipeline.Ref,
		SHA:    mr.HeadPipeline.SHA,
		WebURL: mr.HeadPipeline.WebURL,
	}, nil
}

// ListPipelines returns pipelines for a ref, ordered by ID descending.
func (c *APIClient) ListPipelines(ctx context.Context, projectID int, ref, status string) ([]*Pipeline, error) {
	opts := &goGitLab.ListProjectPipelinesOptions{
		Ref:     goGitLab.Ptr(ref),
		OrderBy: goGitLab.Ptr("id"),
		Sort:    goGitLab.Ptr("desc"),
	}
	if status != "" {
		pipelineStatus := goGitLab.BuildStateValue(status)
		opts.Status = &pipelineStatus
	}

	pipelines, _, err := c.client.Pipelines.ListProjectPipelines(projectID, opts, goGitLab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("listing pipelines: %w", err)
	}

	result := make([]*Pipeline, len(pipelines))
	for i, p := range pipelines {
		result[i] = &Pipeline{
			ID:     int(p.ID),
			Status: p.Status,
			Ref:    p.Ref,
			SHA:    p.SHA,
			WebURL: p.WebURL,
		}
	}
	return result, nil
}

// CancelPipeline cancels a running pipeline.
func (c *APIClient) CancelPipeline(ctx context.Context, projectID, pipelineID int) error {
	_, _, err := c.client.Pipelines.CancelPipelineBuild(int64(projectID), int64(pipelineID), goGitLab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("canceling pipeline %d: %w", pipelineID, err)
	}
	return nil
}

// RetryPipeline retries (restarts) a pipeline.
func (c *APIClient) RetryPipeline(ctx context.Context, projectID, pipelineID int) (*Pipeline, error) {
	p, _, err := c.client.Pipelines.RetryPipelineBuild(int64(projectID), int64(pipelineID), goGitLab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("retrying pipeline %d: %w", pipelineID, err)
	}
	return &Pipeline{
		ID:     int(p.ID),
		Status: p.Status,
		Ref:    p.Ref,
		SHA:    p.SHA,
		WebURL: p.WebURL,
	}, nil
}

func convertMergeRequest(mr *goGitLab.MergeRequest) *MergeRequest {
	author := ""
	if mr.Author != nil {
		author = mr.Author.Username
	}

	headPipelineStatus := ""
	if mr.HeadPipeline != nil {
		headPipelineStatus = mr.HeadPipeline.Status
	}

	createdAt := ""
	if mr.CreatedAt != nil {
		createdAt = mr.CreatedAt.Format(time.RFC3339)
	}

	return &MergeRequest{
		IID:                         int(mr.IID),
		Title:                       mr.Title,
		Author:                      author,
		SourceBranch:                mr.SourceBranch,
		TargetBranch:                mr.TargetBranch,
		SHA:                         mr.SHA,
		CreatedAt:                   createdAt,
		Draft:                       mr.Draft,
		HeadPipelineStatus:          headPipelineStatus,
		DetailedMergeStatus:         mr.DetailedMergeStatus,
		BlockingDiscussionsResolved: mr.BlockingDiscussionsResolved,
		WebURL:                      mr.WebURL,
	}
}
