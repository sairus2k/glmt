package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
func NewAPIClient(host, token string, opts ...goGitLab.ClientOptionFunc) (*APIClient, error) {
	host = normalizeBaseURL(host)
	opts = append([]goGitLab.ClientOptionFunc{goGitLab.WithBaseURL(host + "/api/v4")}, opts...)
	client, err := goGitLab.NewClient(token, opts...)
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

// GetMergeRequest returns a single merge request with full detail.
func (c *APIClient) GetMergeRequest(ctx context.Context, projectID, mrIID int) (*MergeRequest, error) {
	mr, _, err := c.client.MergeRequests.GetMergeRequest(int64(projectID), int64(mrIID), nil, goGitLab.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("getting merge request %d: %w", mrIID, err)
	}
	result := convertMergeRequest(mr)

	commits, _, err := c.client.MergeRequests.GetMergeRequestCommits(int64(projectID), int64(mrIID), nil, goGitLab.WithContext(ctx))
	if err == nil {
		result.CommitCount = len(commits)
	}

	return result, nil
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

// graphQLStringInt is an int that GraphQL encodes as a JSON string (e.g. IID).
type graphQLStringInt int

func (g *graphQLStringInt) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("parsing %q as int: %w", s, err)
	}
	*g = graphQLStringInt(i)
	return nil
}

// ListMergeRequestsFull fetches open merge requests with all fields via GraphQL.
func (c *APIClient) ListMergeRequestsFull(ctx context.Context, projectPath string) ([]*MergeRequest, error) {
	const query = `query($projectPath: ID!, $after: String) {
		project(fullPath: $projectPath) {
			mergeRequests(state: opened, sort: CREATED_ASC, first: 100, after: $after) {
				pageInfo { endCursor hasNextPage }
				nodes {
					iid title draft commitCount
					author { username }
					sourceBranch targetBranch diffHeadSha createdAt
					approvedBy { nodes { username } }
					headPipeline { status }
					detailedMergeStatus
					webUrl
				}
			}
		}
	}`

	type graphQLNode struct {
		IID                 graphQLStringInt `json:"iid"`
		Title               string           `json:"title"`
		Draft               bool             `json:"draft"`
		CommitCount         int              `json:"commitCount"`
		Author              *struct {
			Username string `json:"username"`
		} `json:"author"`
		SourceBranch        string `json:"sourceBranch"`
		TargetBranch        string `json:"targetBranch"`
		DiffHeadSha         string `json:"diffHeadSha"`
		CreatedAt           string `json:"createdAt"`
		ApprovedBy struct {
			Nodes []struct {
				Username string `json:"username"`
			} `json:"nodes"`
		} `json:"approvedBy"`
		HeadPipeline *struct {
			Status string `json:"status"`
		} `json:"headPipeline"`
		DetailedMergeStatus string `json:"detailedMergeStatus"`
		WebURL              string `json:"webUrl"`
	}

	type graphQLResponse struct {
		Data *struct {
			Project *struct {
				MergeRequests struct {
					PageInfo goGitLab.PageInfo `json:"pageInfo"`
					Nodes    []graphQLNode     `json:"nodes"`
				} `json:"mergeRequests"`
			} `json:"project"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	var all []*MergeRequest
	var after *string

	for {
		vars := map[string]any{"projectPath": projectPath}
		if after != nil {
			vars["after"] = *after
		}

		var resp graphQLResponse

		_, err := c.client.GraphQL.Do(goGitLab.GraphQLQuery{
			Query:     query,
			Variables: vars,
		}, &resp, goGitLab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("graphql ListMergeRequestsFull: %w", err)
		}

		if len(resp.Errors) > 0 {
			msgs := make([]string, len(resp.Errors))
			for i, e := range resp.Errors {
				msgs[i] = e.Message
			}
			return nil, fmt.Errorf("graphql ListMergeRequestsFull: %s", strings.Join(msgs, "; "))
		}

		if resp.Data == nil || resp.Data.Project == nil {
			return nil, fmt.Errorf("graphql ListMergeRequestsFull: project %q not found", projectPath)
		}

		conn := resp.Data.Project.MergeRequests
		for _, n := range conn.Nodes {
			mr := &MergeRequest{
				IID:                         int(n.IID),
				Title:                       n.Title,
				Draft:                       n.Draft,
				CommitCount:                 n.CommitCount,
				SourceBranch:                n.SourceBranch,
				TargetBranch:                n.TargetBranch,
				SHA:                         n.DiffHeadSha,
				CreatedAt:                   n.CreatedAt,
				DetailedMergeStatus:         strings.ToLower(n.DetailedMergeStatus),
				BlockingDiscussionsResolved: true, // not available in GraphQL; safe default
				WebURL:                      n.WebURL,
			}
			if n.Author != nil {
				mr.Author = n.Author.Username
			}
			mr.ApprovalCount = len(n.ApprovedBy.Nodes)
			if n.HeadPipeline != nil {
				mr.HeadPipelineStatus = strings.ToLower(n.HeadPipeline.Status)
			}
			all = append(all, mr)
		}

		if !conn.PageInfo.HasNextPage {
			break
		}
		after = &conn.PageInfo.EndCursor
	}

	return all, nil
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
