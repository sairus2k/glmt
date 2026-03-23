package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goGitLab "gitlab.com/gitlab-org/api/client-go"
)

// newTestClient creates an APIClient pointing at the given test server.
func newTestClient(t *testing.T, server *httptest.Server) *APIClient {
	t.Helper()
	client, err := NewAPIClient(server.URL, "test-token", goGitLab.WithCustomRetryMax(0))
	require.NoError(t, err)
	client.rebasePollInterval = 1 * time.Millisecond
	return client
}

func TestGetMergeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v4/projects/1/merge_requests/10":
			_, _ = fmt.Fprint(w, `{
				"iid": 10,
				"title": "Add feature X",
				"author": {"username": "alice"},
				"source_branch": "feature-x",
				"target_branch": "main",
				"sha": "abc123",
				"created_at": "2025-01-15T10:00:00Z",
				"draft": false,
				"head_pipeline": {"id": 100, "status": "success", "ref": "feature-x", "sha": "abc123", "web_url": "https://gitlab.com/pipeline/100"},
				"detailed_merge_status": "mergeable",
				"blocking_discussions_resolved": true,
				"web_url": "https://gitlab.com/mr/10"
			}`)
		case "/api/v4/projects/1/merge_requests/10/commits":
			_, _ = fmt.Fprint(w, `[
				{"id": "aaa111", "title": "First commit"},
				{"id": "bbb222", "title": "Second commit"}
			]`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mr, err := client.GetMergeRequest(context.Background(), 1, 10)
	require.NoError(t, err)

	assert.Equal(t, 10, mr.IID)
	assert.Equal(t, "Add feature X", mr.Title)
	assert.Equal(t, "alice", mr.Author)
	assert.Equal(t, "feature-x", mr.SourceBranch)
	assert.Equal(t, "main", mr.TargetBranch)
	assert.Equal(t, "abc123", mr.SHA)
	assert.False(t, mr.Draft)
	assert.Equal(t, "success", mr.HeadPipelineStatus)
	assert.Equal(t, "mergeable", mr.DetailedMergeStatus)
	assert.True(t, mr.BlockingDiscussionsResolved)
	assert.Equal(t, "https://gitlab.com/mr/10", mr.WebURL)
	assert.Equal(t, 2, mr.CommitCount)
}

func TestRebaseMergeRequest_Success(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/v4/projects/1/merge_requests/10/rebase":
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprint(w, `{"rebase_in_progress": true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/1/merge_requests/10":
			callCount++
			w.Header().Set("Content-Type", "application/json")
			if callCount == 1 {
				_, _ = fmt.Fprint(w, `{
					"iid": 10, "title": "MR", "source_branch": "b", "target_branch": "main",
					"sha": "abc", "rebase_in_progress": true, "merge_error": ""
				}`)
			} else {
				_, _ = fmt.Fprint(w, `{
					"iid": 10, "title": "MR", "source_branch": "b", "target_branch": "main",
					"sha": "abc", "rebase_in_progress": false, "merge_error": ""
				}`)
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mr, err := client.RebaseMergeRequest(context.Background(), 1, 10)
	require.NoError(t, err)
	assert.Equal(t, "abc", mr.SHA)
	assert.GreaterOrEqual(t, callCount, 2)
}

func TestRebaseMergeRequest_Conflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/v4/projects/1/merge_requests/10/rebase":
			w.WriteHeader(http.StatusAccepted)
			_, _ = fmt.Fprint(w, `{"rebase_in_progress": true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/1/merge_requests/10":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{
				"iid": 10, "title": "MR", "source_branch": "b", "target_branch": "main",
				"sha": "abc", "rebase_in_progress": false,
				"merge_error": "Rebase failed: conflict in file.txt"
			}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mr, err := client.RebaseMergeRequest(context.Background(), 1, 10)
	require.Error(t, err)
	assert.Nil(t, mr)
	assert.Contains(t, err.Error(), "conflict")
}

func TestMergeMergeRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v4/projects/1/merge_requests/10/merge", r.URL.Path)

		var body map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&body)
		require.NoError(t, err)
		assert.Equal(t, "abc123", body["sha"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"iid": 10, "title": "MR", "state": "merged",
			"source_branch": "b", "target_branch": "main", "sha": "abc123",
			"merge_commit_sha": "deadbeef"
		}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mergeCommitSHA, err := client.MergeMergeRequest(context.Background(), 1, 10, "abc123")
	require.NoError(t, err)
	assert.Equal(t, "deadbeef", mergeCommitSHA)
}

func TestMergeMergeRequest_SHAMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprint(w, `{"message": "SHA does not match"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mergeCommitSHA, err := client.MergeMergeRequest(context.Background(), 1, 10, "wrong-sha")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSHAMismatch)
	assert.Empty(t, mergeCommitSHA)
}

func TestMergeMergeRequest_NotMergeable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = fmt.Fprint(w, `{"message": "Method Not Allowed"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mergeCommitSHA, err := client.MergeMergeRequest(context.Background(), 1, 10, "abc123")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotMergeable)
	assert.Empty(t, mergeCommitSHA)
}

func TestGetMergeRequestPipeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v4/projects/1/merge_requests/10", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"iid": 10, "title": "MR", "source_branch": "b", "target_branch": "main", "sha": "abc",
			"head_pipeline": {
				"id": 200, "status": "running", "ref": "feature-x",
				"sha": "abc123", "web_url": "https://gitlab.com/pipeline/200"
			}
		}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	pipeline, mergeStatus, err := client.GetMergeRequestPipeline(context.Background(), 1, 10)
	require.NoError(t, err)
	require.NotNil(t, pipeline)

	assert.Equal(t, 200, pipeline.ID)
	assert.Equal(t, "running", pipeline.Status)
	assert.Equal(t, "feature-x", pipeline.Ref)
	assert.Equal(t, "abc123", pipeline.SHA)
	assert.Equal(t, "https://gitlab.com/pipeline/200", pipeline.WebURL)
	assert.Empty(t, mergeStatus)
}

func TestListPipelines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v4/projects/1/pipelines", r.URL.Path)
		assert.Equal(t, "main", r.URL.Query().Get("ref"))
		assert.Equal(t, "id", r.URL.Query().Get("order_by"))
		assert.Equal(t, "desc", r.URL.Query().Get("sort"))

		w.Header().Set("Content-Type", "application/json")
		resp := `[
			{
				"id": 300, "status": "success", "ref": "main",
				"sha": "aaa111", "web_url": "https://gitlab.com/pipeline/300"
			},
			{
				"id": 299, "status": "failed", "ref": "main",
				"sha": "bbb222", "web_url": "https://gitlab.com/pipeline/299"
			}
		]`
		_, _ = fmt.Fprint(w, resp)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	pipelines, err := client.ListPipelines(context.Background(), 1, "main", "", "")
	require.NoError(t, err)
	require.Len(t, pipelines, 2)

	assert.Equal(t, 300, pipelines[0].ID)
	assert.Equal(t, "success", pipelines[0].Status)
	assert.Equal(t, "main", pipelines[0].Ref)
	assert.Equal(t, "aaa111", pipelines[0].SHA)

	assert.Equal(t, 299, pipelines[1].ID)
	assert.Equal(t, "failed", pipelines[1].Status)
}

func TestListPipelines_WithSHAFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "main", r.URL.Query().Get("ref"))
		assert.Equal(t, "abc123", r.URL.Query().Get("sha"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"id": 400, "status": "running", "ref": "main", "sha": "abc123", "web_url": "https://gitlab.com/pipeline/400"}]`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	pipelines, err := client.ListPipelines(context.Background(), 1, "main", "", "abc123")
	require.NoError(t, err)
	require.Len(t, pipelines, 1)
	assert.Equal(t, 400, pipelines[0].ID)
	assert.Equal(t, "abc123", pipelines[0].SHA)
}

func TestCancelPipeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/projects/1/pipelines/300/cancel", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id": 300, "status": "canceled", "ref": "main", "sha": "aaa111"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	err := client.CancelPipeline(context.Background(), 1, 300)
	require.NoError(t, err)
}

func TestRetryPipeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/projects/1/pipelines/300/retry", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"id": 301, "status": "pending", "ref": "main",
			"sha": "aaa111", "web_url": "https://gitlab.com/pipeline/301"
		}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	pipeline, err := client.RetryPipeline(context.Background(), 1, 300)
	require.NoError(t, err)
	require.NotNil(t, pipeline)

	assert.Equal(t, 301, pipeline.ID)
	assert.Equal(t, "pending", pipeline.Status)
	assert.Equal(t, "main", pipeline.Ref)
	assert.Equal(t, "aaa111", pipeline.SHA)
	assert.Equal(t, "https://gitlab.com/pipeline/301", pipeline.WebURL)
}

func TestGetCurrentUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v4/user", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id": 42, "username": "testuser", "name": "Test User"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	user, err := client.GetCurrentUser(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 42, user.ID)
	assert.Equal(t, "testuser", user.Username)
	assert.Equal(t, "Test User", user.Name)
}

func TestListMergeRequestsFull(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/graphql", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"data": {
				"project": {
					"mergeRequests": {
						"pageInfo": {"endCursor": "", "hasNextPage": false},
						"nodes": [
							{
								"iid": "10",
								"title": "Add feature X",
								"draft": false,
								"commitCount": 3,
								"author": {"username": "alice"},
								"sourceBranch": "feature-x",
								"targetBranch": "main",
								"diffHeadSha": "abc123",
								"createdAt": "2025-01-15T10:00:00Z",
								"headPipeline": {"status": "SUCCESS"},
								"detailedMergeStatus": "MERGEABLE",
								"webUrl": "https://gitlab.com/mr/10"
							},
							{
								"iid": "11",
								"title": "Fix bug Y",
								"draft": true,
								"commitCount": 1,
								"author": {"username": "bob"},
								"sourceBranch": "fix-y",
								"targetBranch": "main",
								"diffHeadSha": "def456",
								"createdAt": "2025-01-16T12:00:00Z",
								"headPipeline": null,
								"detailedMergeStatus": "CHECKING",
								"webUrl": "https://gitlab.com/mr/11"
							}
						]
					}
				}
			}
		}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mrs, err := client.ListMergeRequestsFull(context.Background(), "team/project")
	require.NoError(t, err)
	require.Len(t, mrs, 2)

	// Verify field mapping and enum lowercasing
	assert.Equal(t, 10, mrs[0].IID)
	assert.Equal(t, "Add feature X", mrs[0].Title)
	assert.Equal(t, "alice", mrs[0].Author)
	assert.Equal(t, "feature-x", mrs[0].SourceBranch)
	assert.Equal(t, "main", mrs[0].TargetBranch)
	assert.Equal(t, "abc123", mrs[0].SHA)
	assert.Equal(t, 3, mrs[0].CommitCount)
	assert.False(t, mrs[0].Draft)
	assert.Equal(t, "success", mrs[0].HeadPipelineStatus)    // lowercased
	assert.Equal(t, "mergeable", mrs[0].DetailedMergeStatus) // lowercased
	assert.True(t, mrs[0].BlockingDiscussionsResolved)       // default true
	assert.Equal(t, "https://gitlab.com/mr/10", mrs[0].WebURL)

	assert.Equal(t, 11, mrs[1].IID)
	assert.Equal(t, "bob", mrs[1].Author)
	assert.True(t, mrs[1].Draft)
	assert.Equal(t, 1, mrs[1].CommitCount)
	assert.Equal(t, "", mrs[1].HeadPipelineStatus) // null pipeline
	assert.Equal(t, "checking", mrs[1].DetailedMergeStatus)
}

func TestListMergeRequestsFull_Pagination(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		variables, _ := body["variables"].(map[string]any)

		if variables["after"] == nil || variables["after"] == "" {
			// First page
			_, _ = fmt.Fprint(w, `{
				"data": {
					"project": {
						"mergeRequests": {
							"pageInfo": {"endCursor": "cursor1", "hasNextPage": true},
							"nodes": [
								{
									"iid": "1", "title": "MR 1", "draft": false, "commitCount": 1,
									"author": {"username": "a"},
									"sourceBranch": "b1", "targetBranch": "main",
									"diffHeadSha": "sha1", "createdAt": "2025-01-01T00:00:00Z",
									"headPipeline": {"status": "SUCCESS"},
									"detailedMergeStatus": "MERGEABLE", "webUrl": "https://gitlab.com/mr/1"
								}
							]
						}
					}
				}
			}`)
		} else {
			// Second page
			_, _ = fmt.Fprint(w, `{
				"data": {
					"project": {
						"mergeRequests": {
							"pageInfo": {"endCursor": "", "hasNextPage": false},
							"nodes": [
								{
									"iid": "2", "title": "MR 2", "draft": false, "commitCount": 2,
									"author": {"username": "b"},
									"sourceBranch": "b2", "targetBranch": "main",
									"diffHeadSha": "sha2", "createdAt": "2025-01-02T00:00:00Z",
									"headPipeline": null,
									"detailedMergeStatus": "MERGEABLE", "webUrl": "https://gitlab.com/mr/2"
								}
							]
						}
					}
				}
			}`)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mrs, err := client.ListMergeRequestsFull(context.Background(), "team/project")
	require.NoError(t, err)
	require.Len(t, mrs, 2)
	assert.Equal(t, 2, callCount)
	assert.Equal(t, 1, mrs[0].IID)
	assert.Equal(t, 2, mrs[1].IID)
}

func TestListMergeRequestsFull_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"errors": [{"message": "Something went wrong"}]}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mrs, err := client.ListMergeRequestsFull(context.Background(), "team/project")
	require.Error(t, err)
	assert.Nil(t, mrs)
}

func TestListMergeRequestsFull_GraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// HTTP 200 but with GraphQL errors — e.g. invalid field
		_, _ = fmt.Fprint(w, `{
			"data": null,
			"errors": [{"message": "Field 'foo' doesn't exist on type 'MergeRequest'"}]
		}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mrs, err := client.ListMergeRequestsFull(context.Background(), "team/project")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Field 'foo'")
	assert.Nil(t, mrs)
}

func TestListMergeRequestsFull_ProjectNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data": {"project": null}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	mrs, err := client.ListMergeRequestsFull(context.Background(), "nonexistent/project")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Nil(t, mrs)
}
