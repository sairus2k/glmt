package train

import (
	"context"
	"fmt"
	"testing"

	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper to create test MRs
func makeMR(iid int, title string) *gitlab.MergeRequest {
	return &gitlab.MergeRequest{
		IID:          iid,
		Title:        title,
		SourceBranch: fmt.Sprintf("feature-%d", iid),
		TargetBranch: "main",
		SHA:          fmt.Sprintf("sha-%d", iid),
	}
}

// newTestRunner creates a Runner with zero poll intervals for testing.
func newTestRunner(client gitlab.Client) *Runner {
	r := NewRunner(client, 1, nil)
	r.PollRebaseInterval = 0
	r.PollPipelineInterval = 0
	return r
}

func TestRunnerRun(t *testing.T) {
	tests := []struct {
		name string
		mrs  []*gitlab.MergeRequest
		// setup configures the mock client
		setup func(m *MockClient)
		// assertions on the result
		assertResult func(t *testing.T, result *Result)
		// assertions on mock calls
		assertCalls func(t *testing.T, m *MockClient)
		// expected error
		wantErr bool
		// use a cancelled context
		cancelCtx bool
	}{
		{
			name: "all MRs succeed",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
				makeMR(2, "MR 2"),
				makeMR(3, "MR 3"),
			},
			setup: func(m *MockClient) {
				// Rebase always succeeds (default)
				// Pipeline always succeeds (default)
				// GetMergeRequest returns MR with SHA
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// MergeMergeRequest succeeds (default) — returns "merge-commit-sha-{mrIID}"
				// ListPipelines for cancel: return a running pipeline when SHA is a cancel SHA
				// ListPipelines for main pipeline wait: sha == "merge-commit-sha-3" (last MR)
				pipelineID := 100
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-3" {
						// Main pipeline wait — return success
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					if ref == "main" && sha != "" {
						// Cancel step for MR 1 and MR 2
						pipelineID++
						return []*gitlab.Pipeline{
							{ID: pipelineID, Status: "running", Ref: "main", SHA: sha, WebURL: fmt.Sprintf("http://example.com/pipelines/%d", pipelineID)},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 3)
				for i, mr := range result.MRResults {
					assert.Equal(t, MRStatusMerged, mr.Status, "MR %d should be merged", i+1)
					assert.Empty(t, mr.SkipReason, "MR %d should have no skip reason", i+1)
				}
				assert.Equal(t, "success", result.MainPipelineStatus)
				assert.Equal(t, "http://example.com/pipelines/200", result.MainPipelineURL)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// Each MR: RebaseMergeRequest, GetMergeRequest, MergeMergeRequest
				// Non-last MRs: ListPipelines(sha=merge-commit), CancelPipeline
				// Final: ListPipelines(sha=merge-commit-sha-3) for main pipeline wait
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 3)
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 3)
				cancelCalls := m.CallsTo("CancelPipeline")
				assert.Len(t, cancelCalls, 2, "should cancel main pipeline after MR 1 and MR 2")
				retryCalls := m.CallsTo("RetryPipeline")
				assert.Empty(t, retryCalls, "should not retry any pipeline")
				pipelineCalls := m.CallsTo("GetMergeRequestPipeline")
				assert.Empty(t, pipelineCalls, "pipeline wait is always skipped")
			},
		},
		{
			name: "SHA mismatch on merge - rebase retried once, then merged",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
			},
			setup: func(m *MockClient) {
				mergeCallCount := 0
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d-v2", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				m.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) (string, error) {
					mergeCallCount++
					if mergeCallCount == 1 {
						return "", ErrSHAMismatch
					}
					return "merge-commit-sha", nil
				}
				// waitForMainPipeline called with sha="merge-commit-sha"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha" {
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Empty(t, result.MRResults[0].SkipReason)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// First attempt: rebase, getMR, merge(fail)
				// Retry: rebase, getMR, merge(success)
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 2, "should rebase twice")
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 2, "should try to merge twice")
			},
		},
		{
			name: "SHA mismatch twice - MR skipped",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
			},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// Always return SHA mismatch
				m.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) (string, error) {
					return "", ErrSHAMismatch
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Contains(t, result.MRResults[0].SkipReason, "SHA mismatch on retry")
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// First attempt: rebase, getMR, merge(fail)
				// Retry: rebase, getMR, merge(fail again)
				// No further retries
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 2)
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 2)
			},
		},
		{
			name: "rebase conflict - MR skipped, continue",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
				makeMR(2, "MR 2"),
			},
			setup: func(m *MockClient) {
				// MR 1 rebase fails with conflict
				m.RebaseMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					if mrIID == 1 {
						return nil, fmt.Errorf("rebase conflict")
					}
					return &gitlab.MergeRequest{IID: mrIID, SHA: fmt.Sprintf("sha-%d", mrIID)}, nil
				}
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// MR 2 is the last MR merged, default merge returns "merge-commit-sha-2"
				// waitForMainPipeline called with sha="merge-commit-sha-2"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-2" {
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 2)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Contains(t, result.MRResults[0].SkipReason, "rebase conflict")
				assert.Equal(t, MRStatusMerged, result.MRResults[1].Status)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// MR 1: rebase(fail) -> skip, no merge calls
				// MR 2: rebase, getMR, merge (last MR, no cancel)
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 2)
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 1, "only MR 2 should merge")
			},
		},
		{
			name: "all MRs skipped - nothing to merge or restart",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
				makeMR(2, "MR 2"),
			},
			setup: func(m *MockClient) {
				// All rebases fail
				m.RebaseMergeRequestFn = func(_ context.Context, _ int, _ int) (*gitlab.MergeRequest, error) {
					return nil, fmt.Errorf("rebase conflict")
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 2)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Equal(t, MRStatusSkipped, result.MRResults[1].Status)
				// No main pipeline awaited
				assert.Empty(t, result.MainPipelineStatus)
				assert.Empty(t, result.MainPipelineURL)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// Only rebase calls — no ListPipelines since no MR merged
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 2)
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Empty(t, mergeCalls)
				cancelCalls := m.CallsTo("CancelPipeline")
				assert.Empty(t, cancelCalls)
				retryCalls := m.CallsTo("RetryPipeline")
				assert.Empty(t, retryCalls)
				listCalls := m.CallsTo("ListPipelines")
				assert.Empty(t, listCalls, "no ListPipelines calls when nothing merged")
			},
		},
		{
			name:      "context cancelled - returns early",
			mrs:       []*gitlab.MergeRequest{makeMR(1, "MR 1"), makeMR(2, "MR 2")},
			cancelCtx: true,
			setup:     func(_ *MockClient) {},
			assertResult: func(t *testing.T, result *Result) {
				// At least the first MR should be pending since ctx was cancelled before run
				require.Len(t, result.MRResults, 2)
				// All should be pending since context was cancelled before any processing
				for i, mr := range result.MRResults {
					assert.Equal(t, MRStatusPending, mr.Status, "MR %d should be pending", i+1)
				}
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// No calls should be made since context was cancelled
				assert.Empty(t, m.Calls)
			},
			wantErr: true,
		},
		{
			name: "single MR succeeds - main pipeline awaited",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// Default merge returns "merge-commit-sha-1"
				// waitForMainPipeline called with sha="merge-commit-sha-1"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-1" {
						return []*gitlab.Pipeline{
							{ID: 300, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/300"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Equal(t, "success", result.MainPipelineStatus)
				assert.Equal(t, "http://example.com/pipelines/300", result.MainPipelineURL)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// Single MR (last): rebase, getMR, merge — no cancel
				cancelCalls := m.CallsTo("CancelPipeline")
				assert.Empty(t, cancelCalls, "should not cancel pipeline for last/only MR")
				retryCalls := m.CallsTo("RetryPipeline")
				assert.Empty(t, retryCalls)
			},
		},
		{
			name: "merge non-SHA error - MR skipped",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				m.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) (string, error) {
					return "", fmt.Errorf("500 internal server error")
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Contains(t, result.MRResults[0].SkipReason, "merge failed")
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 1, "should only attempt merge once for non-SHA error")
				// No retry
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 1, "should not retry rebase for non-SHA error")
			},
		},
		{
			name: "merge 405 - retried and succeeds",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				mergeCallCount := 0
				m.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) (string, error) {
					mergeCallCount++
					if mergeCallCount == 1 {
						return "", fmt.Errorf("merging MR 1: %w", gitlab.ErrNotMergeable)
					}
					return "merge-commit-sha", nil
				}
				// waitForMainPipeline called with sha="merge-commit-sha"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha" {
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Empty(t, result.MRResults[0].SkipReason)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 2, "should try merge twice (first 405, then success)")
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 1, "should not re-rebase on 405")
			},
		},
		{
			name: "merge 405 - exhausted retries, skipped",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				m.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) (string, error) {
					return "", fmt.Errorf("merging MR 1: %w", gitlab.ErrNotMergeable)
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Contains(t, result.MRResults[0].SkipReason, "405 retries exhausted")
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				mergeCalls := m.CallsTo("MergeMergeRequest")
				// 1 initial + MaxMergeStatusRetries (5) retries = 6
				assert.Len(t, mergeCalls, 6, "should exhaust all merge retries")
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 1, "should not re-rebase on 405")
			},
		},
		{
			name: "main pipeline fails - result reflects failure",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// Default merge returns "merge-commit-sha-1"
				// waitForMainPipeline called with sha="merge-commit-sha-1"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-1" {
						return []*gitlab.Pipeline{
							{ID: 200, Status: "failed", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Equal(t, "failed", result.MainPipelineStatus)
			},
			assertCalls: func(t *testing.T, _ *MockClient) {},
		},
		{
			name: "call sequence verification for three MR train",
			mrs: []*gitlab.MergeRequest{
				makeMR(10, "MR 10"),
				makeMR(20, "MR 20"),
				makeMR(30, "MR 30"),
			},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// Default merge returns "merge-commit-sha-{mrIID}"
				// Last MR is 30, so waitForMainPipeline is called with sha="merge-commit-sha-30"
				// Cancel for MR 10 uses sha="merge-commit-sha-10"
				// Cancel for MR 20 uses sha="merge-commit-sha-20"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-30" {
						// Main pipeline wait
						return []*gitlab.Pipeline{
							{ID: 600, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/600"},
						}, nil
					}
					if ref == "main" && sha != "" {
						// Cancel step for MR 10 and MR 20
						return []*gitlab.Pipeline{
							{ID: 500, Status: "running", Ref: "main", SHA: sha},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				for i := range result.MRResults {
					assert.Equal(t, MRStatusMerged, result.MRResults[i].Status)
				}
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				methods := m.MethodNames()
				expected := []string{
					// MR 10
					"RebaseMergeRequest",
					"GetMergeRequest",
					"MergeMergeRequest",
					"ListPipelines",  // sha=merge-commit-sha-10
					"CancelPipeline", // cancel main
					// MR 20
					"RebaseMergeRequest",
					"GetMergeRequest",
					"MergeMergeRequest",
					"ListPipelines",  // sha=merge-commit-sha-20
					"CancelPipeline", // cancel main
					// MR 30 (last)
					"RebaseMergeRequest",
					"GetMergeRequest",
					"MergeMergeRequest",
					// No cancel for last MR
					// Main pipeline wait
					"ListPipelines", // sha=merge-commit-sha-30
				}
				assert.Equal(t, expected, methods)
			},
		},
		{
			name: "cancels pending pipeline found by SHA",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
				makeMR(2, "MR 2"),
			},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// Default merge returns "merge-commit-sha-{mrIID}"
				// Cancel for MR 1 uses sha="merge-commit-sha-1"
				// waitForMainPipeline uses sha="merge-commit-sha-2"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-2" {
						// Main pipeline wait
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					if ref == "main" && sha != "" {
						// Cancel step — return pending pipeline
						return []*gitlab.Pipeline{
							{ID: 101, Status: "pending", Ref: "main", SHA: sha},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 2)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Equal(t, MRStatusMerged, result.MRResults[1].Status)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				cancelCalls := m.CallsTo("CancelPipeline")
				require.Len(t, cancelCalls, 1)
				assert.Equal(t, 101, cancelCalls[0].Args[1], "should cancel pending pipeline 101")
			},
		},
		{
			name: "retries once when no pipeline found immediately",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
				makeMR(2, "MR 2"),
			},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// Default merge returns "merge-commit-sha-{mrIID}"
				// Cancel for MR 1 uses sha="merge-commit-sha-1": first call returns nil, retry returns pipeline
				// waitForMainPipeline uses sha="merge-commit-sha-2"
				shaCallCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-2" {
						// Main pipeline wait
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					if ref == "main" && sha != "" {
						shaCallCount++
						// First call returns nothing (pipeline not created yet)
						// On retry, return a pipeline
						if shaCallCount > 1 {
							return []*gitlab.Pipeline{
								{ID: 102, Status: "running", Ref: "main", SHA: sha},
							}, nil
						}
						return nil, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 2)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Equal(t, MRStatusMerged, result.MRResults[1].Status)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				cancelCalls := m.CallsTo("CancelPipeline")
				require.Len(t, cancelCalls, 1)
				assert.Equal(t, 102, cancelCalls[0].Args[1], "should cancel pipeline 102 found on retry")
			},
		},
		{
			name: "single MR - skips stale pipeline",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				// Default merge returns "merge-commit-sha-1"
				// waitForMainPipeline polls with sha="merge-commit-sha-1"
				// First poll: no pipeline found (stale pipeline has different SHA)
				// Second poll: new pipeline appears
				pollCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-1" {
						pollCount++
						if pollCount == 1 {
							// No pipeline with this SHA yet
							return nil, nil
						}
						// New pipeline appears
						return []*gitlab.Pipeline{
							{ID: 15554, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/15554"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Equal(t, "success", result.MainPipelineStatus)
				assert.Equal(t, "http://example.com/pipelines/15554", result.MainPipelineURL, "should pick up new pipeline, not stale one")
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				cancelCalls := m.CallsTo("CancelPipeline")
				assert.Empty(t, cancelCalls, "should not cancel pipeline for last/only MR")
				// Should have polled ListPipelines multiple times to wait for pipeline
				listCalls := m.CallsTo("ListPipelines")
				assert.GreaterOrEqual(t, len(listCalls), 2, "should have at least 2 polls")
			},
		},
		{
			name: "not_approved MR proceeds with merge",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "not_approved",
					}, nil
				}
				// Default merge returns "merge-commit-sha-1"
				// waitForMainPipeline called with sha="merge-commit-sha-1"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-1" {
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Empty(t, result.MRResults[0].SkipReason)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 1)
			},
		},
		{
			name: "stale merge status retries then succeeds",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				getMRCallCount := 0
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					getMRCallCount++
					status := "conflict"
					if getMRCallCount >= 3 {
						status = "mergeable"
					}
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: status,
					}, nil
				}
				// Default merge returns "merge-commit-sha-1"
				// waitForMainPipeline called with sha="merge-commit-sha-1"
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-sha-1" {
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				getMRCalls := m.CallsTo("GetMergeRequest")
				assert.GreaterOrEqual(t, len(getMRCalls), 3, "should retry stale merge status")
			},
		},
		{
			name: "cancels correct pipeline by merge commit SHA",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
				makeMR(2, "MR 2"),
			},
			setup: func(m *MockClient) {
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				m.MergeMergeRequestFn = func(_ context.Context, _ int, mrIID int, _ string) (string, error) {
					return fmt.Sprintf("merge-commit-%d", mrIID), nil
				}
				// Cancel for MR 1 uses sha="merge-commit-1"
				// waitForMainPipeline uses sha="merge-commit-2" (last MR)
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && sha == "merge-commit-2" {
						// Main pipeline wait
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					// SHA-filtered query for cancel
					if ref == "main" && sha != "" {
						return []*gitlab.Pipeline{
							{ID: 150, Status: "running", Ref: "main", SHA: sha},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 2)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Equal(t, MRStatusMerged, result.MRResults[1].Status)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				cancelCalls := m.CallsTo("CancelPipeline")
				require.Len(t, cancelCalls, 1)
				assert.Equal(t, 150, cancelCalls[0].Args[1])

				// Verify ListPipelines was called with the merge commit SHA
				listCalls := m.CallsTo("ListPipelines")
				foundSHACall := false
				for _, c := range listCalls {
					if len(c.Args) >= 4 {
						if sha, ok := c.Args[3].(string); ok && sha == "merge-commit-1" {
							foundSHACall = true
							break
						}
					}
				}
				assert.True(t, foundSHACall, "ListPipelines should be called with merge commit SHA")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &MockClient{}
			tt.setup(mock)

			runner := newTestRunner(mock)

			ctx := context.Background()
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // cancel immediately
			}

			result, err := runner.Run(ctx, tt.mrs)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			tt.assertResult(t, result)
			if tt.assertCalls != nil {
				tt.assertCalls(t, mock)
			}
		})
	}
}

func TestRunnerLogger(t *testing.T) {
	mock := &MockClient{}
	mock.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
		return &gitlab.MergeRequest{
			IID:                 mrIID,
			SHA:                 fmt.Sprintf("sha-%d", mrIID),
			TargetBranch:        "main",
			DetailedMergeStatus: "mergeable",
		}, nil
	}
	// Default merge returns "merge-commit-sha-42"
	// waitForMainPipeline called with sha="merge-commit-sha-42"
	mock.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
		if ref == "main" && sha == "merge-commit-sha-42" {
			return []*gitlab.Pipeline{
				{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
			}, nil
		}
		return nil, nil
	}

	type logEntry struct {
		mrIID   int
		step    string
		message string
	}
	var logs []logEntry

	runner := NewRunner(mock, 1, func(mrIID int, step, message string) {
		logs = append(logs, logEntry{mrIID: mrIID, step: step, message: message})
	})
	runner.PollPipelineInterval = 0
	runner.PollRebaseInterval = 0

	mrs := []*gitlab.MergeRequest{makeMR(42, "Test MR")}
	_, err := runner.Run(context.Background(), mrs)
	require.NoError(t, err)

	// Verify that logger was called with expected steps
	require.NotEmpty(t, logs)

	// Should have log entries for rebase, pipeline_skip, merge steps
	steps := make(map[string]bool)
	for _, l := range logs {
		steps[l.step] = true
	}
	assert.True(t, steps["rebase_wait"], "should log rebase_wait step")
	assert.True(t, steps["rebase"], "should log rebase step")
	assert.True(t, steps["pipeline_skip"], "should log pipeline_skip step")
	assert.True(t, steps["merge_wait"], "should log merge_wait step")
	assert.True(t, steps["merge"], "should log merge step")
	assert.True(t, steps["main_pipeline_wait"], "should log main_pipeline_wait step")
	assert.True(t, steps["main_pipeline_done"], "should log main_pipeline_done step")
}

func TestRunnerWarnsOnEmptyMergeCommitSHA(t *testing.T) {
	mock := &MockClient{}
	mock.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
		return &gitlab.MergeRequest{
			IID:                 mrIID,
			SHA:                 fmt.Sprintf("sha-%d", mrIID),
			TargetBranch:        "main",
			DetailedMergeStatus: "mergeable",
		}, nil
	}
	// Return empty merge commit SHA
	mock.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) (string, error) {
		return "", nil
	}
	listCallCount := 0
	mock.ListPipelinesFn = func(_ context.Context, _ int, ref, status, sha string) ([]*gitlab.Pipeline, error) {
		if ref == "main" && sha == "" {
			listCallCount++
			if listCallCount == 1 {
				// Cancel pipeline lookup: return a running pipeline
				return []*gitlab.Pipeline{
					{ID: 101, Status: "running", Ref: "main"},
				}, nil
			}
			// Main pipeline wait: return a finished pipeline
			return []*gitlab.Pipeline{
				{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
			}, nil
		}
		return nil, nil
	}

	type logEntry struct {
		mrIID   int
		step    string
		message string
	}
	var logs []logEntry

	runner := NewRunner(mock, 1, func(mrIID int, step, message string) {
		logs = append(logs, logEntry{mrIID: mrIID, step: step, message: message})
	})
	runner.PollPipelineInterval = 0
	runner.PollRebaseInterval = 0

	mrs := []*gitlab.MergeRequest{makeMR(1, "MR 1"), makeMR(2, "MR 2")}
	_, err := runner.Run(context.Background(), mrs)
	require.NoError(t, err)

	// Verify warning was logged
	foundWarning := false
	for _, l := range logs {
		if l.step == "merge" && l.mrIID == 1 {
			if l.message == "Warning: GitLab did not return merge commit SHA, pipeline lookup may be imprecise" {
				foundWarning = true
				break
			}
		}
	}
	assert.True(t, foundWarning, "should warn when merge commit SHA is empty")

	// Cancel still works via unfiltered fallback
	cancelCalls := mock.CallsTo("CancelPipeline")
	assert.Len(t, cancelCalls, 1, "should still cancel pipeline via fallback")
}

func TestNewRunner(t *testing.T) {
	mock := &MockClient{}
	logger := func(_ int, _, _ string) {}
	r := NewRunner(mock, 42, logger)

	assert.Equal(t, 42, r.projectID)
	assert.NotNil(t, r.client)
	assert.NotNil(t, r.logger)
	assert.NotZero(t, r.PollRebaseInterval)
	assert.NotZero(t, r.PollPipelineInterval)
}
