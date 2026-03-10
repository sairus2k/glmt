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
				// MergeMergeRequest succeeds (default)
				// ListPipelines for cancel: return a running pipeline for non-last MRs
				pipelineID := 100
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "running" {
						pipelineID++
						return []*gitlab.Pipeline{
							{ID: pipelineID, Status: "running", Ref: "main", WebURL: fmt.Sprintf("http://example.com/pipelines/%d", pipelineID)},
						}, nil
					}
					// For pre-train baseline and final main pipeline wait (status="")
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
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
				// Each MR: RebaseMergeRequest, GetMergeRequestPipeline, GetMergeRequest, MergeMergeRequest
				// Non-last MRs: ListPipelines(running), CancelPipeline
				// Final: ListPipelines("") for main pipeline wait
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 3)
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 3)
				cancelCalls := m.CallsTo("CancelPipeline")
				assert.Len(t, cancelCalls, 2, "should cancel main pipeline after MR 1 and MR 2")
				retryCalls := m.CallsTo("RetryPipeline")
				assert.Empty(t, retryCalls, "should not retry any pipeline")
			},
		},
		{
			name: "MR 2 pipeline fails, MR 3 proceeds",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
				makeMR(2, "MR 2"),
				makeMR(3, "MR 3"),
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
				// MR 2 pipeline fails
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					if mrIID == 2 {
						return &gitlab.Pipeline{Status: "failed", SHA: fmt.Sprintf("sha-%d", mrIID)}, "", nil
					}
					return &gitlab.Pipeline{Status: "success", SHA: fmt.Sprintf("sha-%d", mrIID)}, "", nil
				}
				pipelineID := 100
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "running" {
						pipelineID++
						return []*gitlab.Pipeline{
							{ID: pipelineID, Status: "running", Ref: "main"},
						}, nil
					}
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					return nil, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 3)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Equal(t, MRStatusSkipped, result.MRResults[1].Status)
				assert.Contains(t, result.MRResults[1].SkipReason, "pipeline failed")
				assert.Equal(t, MRStatusMerged, result.MRResults[2].Status)
				assert.Equal(t, "success", result.MainPipelineStatus)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// MR 1: rebase, pipeline, getMR, merge, listPipelines(running), cancel
				// MR 2: rebase, pipeline(failed) -> skip, no merge
				// MR 3: rebase, pipeline, getMR, merge (last, no cancel)
				// Final: listPipelines("") for main pipeline
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 3)
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 2, "only MR 1 and MR 3 should merge")
				// MR 2 never gets to GetMergeRequest or Merge
				retryCalls := m.CallsTo("RetryPipeline")
				assert.Empty(t, retryCalls)
			},
		},
		{
			name: "last MR skipped, prior MR merged - cancelled pipeline restarted",
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
				// MR 2 pipeline fails
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					if mrIID == 2 {
						return &gitlab.Pipeline{Status: "failed", SHA: fmt.Sprintf("sha-%d", mrIID)}, "", nil
					}
					return &gitlab.Pipeline{Status: "success", SHA: fmt.Sprintf("sha-%d", mrIID)}, "", nil
				}
				// ListPipelines for cancel returns pipeline 101
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "running" {
						return []*gitlab.Pipeline{
							{ID: 101, Status: "running", Ref: "main", WebURL: "http://example.com/pipelines/101"},
						}, nil
					}
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
						return []*gitlab.Pipeline{
							{ID: 101, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/101"},
						}, nil
					}
					return nil, nil
				}
				m.RetryPipelineFn = func(_ context.Context, _ int, pipelineID int) (*gitlab.Pipeline, error) {
					return &gitlab.Pipeline{
						ID:     pipelineID,
						Status: "running",
						Ref:    "main",
						WebURL: fmt.Sprintf("http://example.com/pipelines/%d", pipelineID),
					}, nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 2)
				assert.Equal(t, MRStatusMerged, result.MRResults[0].Status)
				assert.Equal(t, MRStatusSkipped, result.MRResults[1].Status)
				assert.Contains(t, result.MRResults[1].SkipReason, "pipeline failed")
				// Main pipeline should still be awaited because MR 1 merged
				assert.Equal(t, "success", result.MainPipelineStatus)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// MR 1: rebase, pipeline, getMR, merge, listPipelines(running), cancel
				// MR 2: rebase, pipeline(failed) -> skip
				// RetryPipeline should be called to restart the cancelled pipeline
				retryCalls := m.CallsTo("RetryPipeline")
				require.Len(t, retryCalls, 1)
				assert.Equal(t, 101, retryCalls[0].Args[1], "should retry pipeline 101")
				cancelCalls := m.CallsTo("CancelPipeline")
				require.Len(t, cancelCalls, 1)
				assert.Equal(t, 101, cancelCalls[0].Args[1])
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
				m.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) error {
					mergeCallCount++
					if mergeCallCount == 1 {
						return ErrSHAMismatch
					}
					return nil
				}
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
				// First attempt: rebase, pipeline, getMR, merge(fail)
				// Retry: rebase, pipeline, getMR, merge(success)
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 2, "should rebase twice")
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Len(t, mergeCalls, 2, "should try to merge twice")
				pipelineCalls := m.CallsTo("GetMergeRequestPipeline")
				assert.Len(t, pipelineCalls, 2, "should wait for pipeline twice")
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
				m.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) error {
					return ErrSHAMismatch
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Contains(t, result.MRResults[0].SkipReason, "SHA mismatch on retry")
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				// First attempt: rebase, pipeline, getMR, merge(fail)
				// Retry: rebase, pipeline, getMR, merge(fail again)
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
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
				// MR 1: rebase(fail) -> skip, no pipeline/merge calls
				// MR 2: rebase, pipeline, getMR, merge (last MR, no cancel)
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
				// Only rebase calls + pre-train baseline
				rebaseCalls := m.CallsTo("RebaseMergeRequest")
				assert.Len(t, rebaseCalls, 2)
				mergeCalls := m.CallsTo("MergeMergeRequest")
				assert.Empty(t, mergeCalls)
				cancelCalls := m.CallsTo("CancelPipeline")
				assert.Empty(t, cancelCalls)
				retryCalls := m.CallsTo("RetryPipeline")
				assert.Empty(t, retryCalls)
				listCalls := m.CallsTo("ListPipelines")
				assert.Len(t, listCalls, 1, "only pre-train baseline listing")
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
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
				// Single MR (last): rebase, pipeline, getMR, merge — no cancel
				cancelCalls := m.CallsTo("CancelPipeline")
				assert.Empty(t, cancelCalls, "should not cancel pipeline for last/only MR")
				retryCalls := m.CallsTo("RetryPipeline")
				assert.Empty(t, retryCalls)
			},
		},
		{
			name: "pipeline polling - pipeline initially running then succeeds",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				pipelinePollCount := 0
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					pipelinePollCount++
					if pipelinePollCount < 3 {
						return &gitlab.Pipeline{Status: "running"}, "", nil
					}
					return &gitlab.Pipeline{Status: "success", SHA: fmt.Sprintf("sha-%d", mrIID)}, "", nil
				}
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 fmt.Sprintf("sha-%d", mrIID),
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
				pipelineCalls := m.CallsTo("GetMergeRequestPipeline")
				assert.Len(t, pipelineCalls, 3, "should poll pipeline 3 times")
			},
		},
		{
			name: "pipeline canceled status - MR skipped",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					return &gitlab.Pipeline{Status: "canceled", SHA: fmt.Sprintf("sha-%d", mrIID)}, "", nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Contains(t, result.MRResults[0].SkipReason, "pipeline canceled")
			},
			assertCalls: func(t *testing.T, _ *MockClient) {},
		},
		{
			name: "pipeline skipped status - MR skipped",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					return &gitlab.Pipeline{Status: "skipped", SHA: fmt.Sprintf("sha-%d", mrIID)}, "", nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 1)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Contains(t, result.MRResults[0].SkipReason, "pipeline skipped")
			},
			assertCalls: func(t *testing.T, _ *MockClient) {},
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
				m.MergeMergeRequestFn = func(_ context.Context, _ int, _ int, _ string) error {
					return fmt.Errorf("405 not mergeable")
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
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
			name: "all MRs skipped via pipeline failure - no retry, no main pipeline wait",
			mrs: []*gitlab.MergeRequest{
				makeMR(1, "MR 1"),
				makeMR(2, "MR 2"),
			},
			setup: func(m *MockClient) {
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					return &gitlab.Pipeline{Status: "failed", SHA: fmt.Sprintf("sha-%d", mrIID)}, "", nil
				}
			},
			assertResult: func(t *testing.T, result *Result) {
				require.Len(t, result.MRResults, 2)
				assert.Equal(t, MRStatusSkipped, result.MRResults[0].Status)
				assert.Equal(t, MRStatusSkipped, result.MRResults[1].Status)
				assert.Empty(t, result.MainPipelineStatus)
				assert.Empty(t, result.MainPipelineURL)
			},
			assertCalls: func(t *testing.T, m *MockClient) {
				retryCalls := m.CallsTo("RetryPipeline")
				assert.Empty(t, retryCalls, "should not retry when nothing was merged")
				listCalls := m.CallsTo("ListPipelines")
				assert.Len(t, listCalls, 1, "only pre-train baseline listing")
			},
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
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "running" {
						return []*gitlab.Pipeline{
							{ID: 500, Status: "running", Ref: "main"},
						}, nil
					}
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
						return []*gitlab.Pipeline{
							{ID: 600, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/600"},
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
					// Pre-train baseline
					"ListPipelines", // status=""
					// MR 10
					"RebaseMergeRequest",
					"GetMergeRequestPipeline",
					"GetMergeRequest",
					"MergeMergeRequest",
					"ListPipelines",  // running
					"CancelPipeline", // cancel main
					// MR 20
					"RebaseMergeRequest",
					"GetMergeRequestPipeline",
					"GetMergeRequest",
					"MergeMergeRequest",
					"ListPipelines",  // running
					"CancelPipeline", // cancel main
					// MR 30 (last)
					"RebaseMergeRequest",
					"GetMergeRequestPipeline",
					"GetMergeRequest",
					"MergeMergeRequest",
					// No cancel for last MR
					// Main pipeline wait
					"ListPipelines", // status=""
				}
				assert.Equal(t, expected, methods)
			},
		},
		{
			name: "cancels pending pipeline when no running pipeline",
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
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "running" {
						return nil, nil // no running pipeline
					}
					if ref == "main" && status == "pending" {
						return []*gitlab.Pipeline{
							{ID: 101, Status: "pending", Ref: "main"},
						}, nil
					}
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
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
				listCallCount := 0
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
						return []*gitlab.Pipeline{
							{ID: 200, Status: "success", Ref: "main", WebURL: "http://example.com/pipelines/200"},
						}, nil
					}
					if ref == "main" && (status == "running" || status == "pending" || status == "created") {
						listCallCount++
						// First 3 calls (running, pending, created) return nothing
						// On retry, the 4th call (running) returns a pipeline
						if listCallCount > 3 {
							return []*gitlab.Pipeline{
								{ID: 102, Status: "running", Ref: "main"},
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
				// Should have 3 (first round) + at least 1 (retry round) ListPipelines calls for cancel
				// plus 1 for main pipeline wait = varies, but cancel should have been called
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
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							// Pre-train baseline: stale pipeline
							return []*gitlab.Pipeline{
								{ID: 15553, Status: "canceled", Ref: "main", WebURL: "http://example.com/pipelines/15553"},
							}, nil
						}
						if listEmptyCount == 2 {
							// First poll: still the stale pipeline
							return []*gitlab.Pipeline{
								{ID: 15553, Status: "canceled", Ref: "main", WebURL: "http://example.com/pipelines/15553"},
							}, nil
						}
						// Second poll: new pipeline appears
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
				// Should have polled ListPipelines multiple times to skip the stale pipeline
				listCalls := m.CallsTo("ListPipelines")
				assert.GreaterOrEqual(t, len(listCalls), 3, "should have baseline + at least 2 polls")
			},
		},
		{
			name: "stale pipeline after rebase is ignored",
			mrs:  []*gitlab.MergeRequest{makeMR(1, "MR 1")},
			setup: func(m *MockClient) {
				// Rebase returns a new SHA
				m.RebaseMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{IID: mrIID, SHA: "new-sha-after-rebase"}, nil
				}
				m.GetMergeRequestFn = func(_ context.Context, _ int, mrIID int) (*gitlab.MergeRequest, error) {
					return &gitlab.MergeRequest{
						IID:                 mrIID,
						SHA:                 "new-sha-after-rebase",
						TargetBranch:        "main",
						DetailedMergeStatus: "mergeable",
					}, nil
				}
				pipelinePollCount := 0
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					pipelinePollCount++
					if pipelinePollCount == 1 {
						// First poll: stale pipeline from before rebase
						return &gitlab.Pipeline{Status: "success", SHA: "old-sha-before-rebase"}, "", nil
					}
					// Second poll: correct pipeline with new SHA
					return &gitlab.Pipeline{Status: "success", SHA: "new-sha-after-rebase"}, "", nil
				}
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
				// Should poll pipeline twice: first returns stale SHA, second returns correct SHA
				pipelineCalls := m.CallsTo("GetMergeRequestPipeline")
				assert.Len(t, pipelineCalls, 2, "should poll pipeline twice due to stale SHA")
			},
		},
		{
			name: "stale pipeline with ci_still_running keeps polling",
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
				pipelinePollCount := 0
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					pipelinePollCount++
					if pipelinePollCount <= 2 {
						// First 2 polls: pipeline shows success but GitLab says CI still running (stale)
						return &gitlab.Pipeline{Status: "success", SHA: fmt.Sprintf("sha-%d", mrIID)}, "ci_still_running", nil
					}
					// Third poll: GitLab acknowledges pipeline is valid
					return &gitlab.Pipeline{Status: "success", SHA: fmt.Sprintf("sha-%d", mrIID)}, "mergeable", nil
				}
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
				// Should poll pipeline 3 times: first 2 return ci_still_running, third returns mergeable
				pipelineCalls := m.CallsTo("GetMergeRequestPipeline")
				assert.Len(t, pipelineCalls, 3, "should poll pipeline 3 times due to ci_still_running")
			},
		},
		{
			name: "stale pipeline with checking status keeps polling",
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
				pipelinePollCount := 0
				m.GetMergeRequestPipelineFn = func(_ context.Context, _ int, mrIID int) (*gitlab.Pipeline, string, error) {
					pipelinePollCount++
					if pipelinePollCount == 1 {
						// Old pipeline shows success but GitLab is still checking (post-rebase)
						return &gitlab.Pipeline{Status: "success", SHA: fmt.Sprintf("sha-%d", mrIID)}, "checking", nil
					}
					if pipelinePollCount == 2 {
						// Still unchecked
						return &gitlab.Pipeline{Status: "success", SHA: fmt.Sprintf("sha-%d", mrIID)}, "unchecked", nil
					}
					// Now the new pipeline result is confirmed
					return &gitlab.Pipeline{Status: "success", SHA: fmt.Sprintf("sha-%d", mrIID)}, "mergeable", nil
				}
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
				pipelineCalls := m.CallsTo("GetMergeRequestPipeline")
				assert.Len(t, pipelineCalls, 3, "should poll 3 times: checking, unchecked, then mergeable")
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
				listEmptyCount := 0
				m.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
					if ref == "main" && status == "" {
						listEmptyCount++
						if listEmptyCount == 1 {
							return []*gitlab.Pipeline{
								{ID: 50, Status: "success", Ref: "main"},
							}, nil
						}
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
	listEmptyCount := 0
	mock.ListPipelinesFn = func(_ context.Context, _ int, ref, status string) ([]*gitlab.Pipeline, error) {
		if ref == "main" && status == "" {
			listEmptyCount++
			if listEmptyCount == 1 {
				return []*gitlab.Pipeline{
					{ID: 50, Status: "success", Ref: "main"},
				}, nil
			}
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

	// Should have log entries for rebase, pipeline, merge steps
	steps := make(map[string]bool)
	for _, l := range logs {
		steps[l.step] = true
	}
	assert.True(t, steps["rebase"], "should log rebase step")
	assert.True(t, steps["pipeline_wait"], "should log pipeline_wait step")
	assert.True(t, steps["pipeline_success"], "should log pipeline_success step")
	assert.True(t, steps["merge"], "should log merge step")
	assert.True(t, steps["main_pipeline_wait"], "should log main_pipeline_wait step")
	assert.True(t, steps["main_pipeline_done"], "should log main_pipeline_done step")
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
