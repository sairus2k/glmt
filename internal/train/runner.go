package train

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sairus2k/glmt/internal/gitlab"
)

// MRStatus represents the outcome of processing a single MR.
type MRStatus int

const (
	MRStatusPending MRStatus = iota
	MRStatusMerged
	MRStatusSkipped
)

// MRResult holds the outcome of a single MR in the train.
type MRResult struct {
	MR         *gitlab.MergeRequest
	Status     MRStatus
	SkipReason string // empty if merged
}

// Result holds the overall train execution result.
type Result struct {
	MRResults          []MRResult
	MainPipelineURL    string
	MainPipelineStatus string
}

// Logger is a callback for the train to report progress.
type Logger func(mrIID int, step string, message string)

// ErrSHAMismatch is returned when a merge fails due to SHA mismatch (409).
var ErrSHAMismatch = errors.New("SHA mismatch")

// Runner executes the merge train.
type Runner struct {
	client    gitlab.Client
	projectID int
	logger    Logger
	// Configurable intervals for testing
	PollRebaseInterval       time.Duration
	PollPipelineInterval     time.Duration
	MaxCancelPipelineRetries int
}

// NewRunner creates a new train runner.
func NewRunner(client gitlab.Client, projectID int, logger Logger) *Runner {
	return &Runner{
		client:                   client,
		projectID:                projectID,
		logger:                   logger,
		PollRebaseInterval:       2 * time.Second,
		PollPipelineInterval:     10 * time.Second,
		MaxCancelPipelineRetries: 3,
	}
}

// Run executes the merge train for the given MRs in order.
// It can be cancelled via the context.
func (r *Runner) Run(ctx context.Context, mrs []*gitlab.MergeRequest) (*Result, error) {
	result := &Result{
		MRResults: make([]MRResult, len(mrs)),
	}
	for i, mr := range mrs {
		result.MRResults[i] = MRResult{
			MR:     mr,
			Status: MRStatusPending,
		}
	}

	var lastCancelledPipelineID int
	anyMerged := false
	targetBranch := mrs[0].TargetBranch

	for i, mr := range mrs {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		isLast := i == len(mrs)-1

		status, skipReason := r.processMR(ctx, mr, isLast, &lastCancelledPipelineID)
		result.MRResults[i].Status = status
		result.MRResults[i].SkipReason = skipReason

		if status == MRStatusMerged {
			anyMerged = true
		}

		// Check context after processing
		if err := ctx.Err(); err != nil {
			return result, err
		}

		// Step 6: If last MR was skipped
		if isLast && status == MRStatusSkipped {
			if anyMerged && lastCancelledPipelineID != 0 {
				// 6a: Prior MR merged, restart cancelled pipeline
				r.log(mr.IID, "restart_pipeline", fmt.Sprintf("Last MR skipped - restarting cancelled main pipeline #%d", lastCancelledPipelineID))
				retried, retryErr := r.client.RetryPipeline(ctx, r.projectID, lastCancelledPipelineID)
				if retryErr != nil {
					r.log(mr.IID, "restart_pipeline", fmt.Sprintf("Failed to restart pipeline: %v", retryErr))
				} else {
					r.log(mr.IID, "restart_pipeline", fmt.Sprintf("Restarted main pipeline: %s", retried.WebURL))
				}
			}
			// 6b: If no MR merged, do nothing
		}
	}

	// Step 7: Wait for main pipeline if any MR was merged or a pipeline was restarted
	if anyMerged {
		r.log(0, "main_pipeline_wait", "Waiting for main pipeline...")
		pipeline, err := r.waitForMainPipeline(ctx, targetBranch)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			r.log(0, "main_pipeline_done", fmt.Sprintf("Error waiting for main pipeline: %v", err))
		} else if pipeline != nil {
			result.MainPipelineURL = pipeline.WebURL
			result.MainPipelineStatus = pipeline.Status
			r.log(0, "main_pipeline_done", fmt.Sprintf("Main pipeline %s: %s", pipeline.Status, pipeline.WebURL))
		}
	}

	return result, nil
}

func (r *Runner) processMR(ctx context.Context, mr *gitlab.MergeRequest, isLast bool, lastCancelledPipelineID *int) (MRStatus, string) {
	return r.processMRAttempt(ctx, mr, isLast, lastCancelledPipelineID, false)
}

func (r *Runner) processMRAttempt(ctx context.Context, mr *gitlab.MergeRequest, isLast bool, lastCancelledPipelineID *int, isRetry bool) (MRStatus, string) {
	// Step 1: REBASE
	r.log(mr.IID, "rebase", "Rebasing merge request...")
	if err := r.client.RebaseMergeRequest(ctx, r.projectID, mr.IID); err != nil {
		if ctx.Err() != nil {
			return MRStatusPending, ""
		}
		r.log(mr.IID, "skip", fmt.Sprintf("Rebase conflict: %v", err))
		return MRStatusSkipped, fmt.Sprintf("rebase conflict: %v", err)
	}
	r.log(mr.IID, "rebase", "Rebase successful")

	// Step 2: WAIT FOR PIPELINE
	r.log(mr.IID, "pipeline_wait", "Waiting for pipeline...")
	pipeline, err := r.waitForMRPipeline(ctx, mr.IID)
	if err != nil {
		if ctx.Err() != nil {
			return MRStatusPending, ""
		}
		r.log(mr.IID, "skip", fmt.Sprintf("Pipeline error: %v", err))
		return MRStatusSkipped, fmt.Sprintf("pipeline error: %v", err)
	}
	if pipeline.Status != "success" {
		r.log(mr.IID, "pipeline_failed", fmt.Sprintf("Pipeline %s", pipeline.Status))
		return MRStatusSkipped, fmt.Sprintf("pipeline %s", pipeline.Status)
	}
	r.log(mr.IID, "pipeline_success", "Pipeline passed")

	// Step 3: MERGE (with SHA guard)
	// Wait for GitLab to finish its internal merge status check
	r.log(mr.IID, "merge", "Waiting for merge readiness...")
	currentMR, err := r.waitForMergeReady(ctx, mr.IID)
	if err != nil {
		if ctx.Err() != nil {
			return MRStatusPending, ""
		}
		r.log(mr.IID, "skip", fmt.Sprintf("Not mergeable: %v", err))
		return MRStatusSkipped, fmt.Sprintf("not mergeable: %v", err)
	}

	r.log(mr.IID, "merge", fmt.Sprintf("Merging with SHA guard (sha=%s)...", currentMR.SHA))
	mergeErr := r.client.MergeMergeRequest(ctx, r.projectID, mr.IID, currentMR.SHA)
	if mergeErr != nil {
		if ctx.Err() != nil {
			return MRStatusPending, ""
		}
		if errors.Is(mergeErr, ErrSHAMismatch) {
			if isRetry {
				// Second SHA mismatch — skip
				r.log(mr.IID, "skip", "SHA mismatch on retry, skipping")
				return MRStatusSkipped, "SHA mismatch on retry"
			}
			// First SHA mismatch — retry from step 1
			r.log(mr.IID, "merge_sha_mismatch", "SHA mismatch, retrying from rebase...")
			return r.processMRAttempt(ctx, mr, isLast, lastCancelledPipelineID, true)
		}
		r.log(mr.IID, "skip", fmt.Sprintf("Merge failed: %v", mergeErr))
		return MRStatusSkipped, fmt.Sprintf("merge failed: %v", mergeErr)
	}
	r.log(mr.IID, "merge", "Merged successfully")

	// Step 4: CANCEL MAIN PIPELINE (if more MRs remain)
	if !isLast {
		r.log(mr.IID, "cancel_main_pipeline", "Cancelling main pipeline...")
		var pipeline *gitlab.Pipeline
		var err error

		pipeline, err = r.findCancellablePipeline(ctx, mr.TargetBranch)
		if err != nil {
			r.log(mr.IID, "cancel_main_pipeline", fmt.Sprintf("Failed to list pipelines: %v", err))
		}

		for attempt := 0; pipeline == nil && err == nil && attempt < r.MaxCancelPipelineRetries; attempt++ {
			r.log(mr.IID, "cancel_main_pipeline", fmt.Sprintf("No main pipeline found, retrying (%d/%d)...", attempt+1, r.MaxCancelPipelineRetries))
			select {
			case <-ctx.Done():
			case <-time.After(r.PollPipelineInterval):
				pipeline, err = r.findCancellablePipeline(ctx, mr.TargetBranch)
				if err != nil {
					r.log(mr.IID, "cancel_main_pipeline", fmt.Sprintf("Failed to list pipelines on retry: %v", err))
				}
			}
			if ctx.Err() != nil {
				break
			}
		}

		if pipeline != nil {
			*lastCancelledPipelineID = pipeline.ID
			if cancelErr := r.client.CancelPipeline(ctx, r.projectID, pipeline.ID); cancelErr != nil {
				r.log(mr.IID, "cancel_main_pipeline", fmt.Sprintf("Failed to cancel pipeline: %v", cancelErr))
			} else {
				r.log(mr.IID, "cancel_main_pipeline", fmt.Sprintf("Cancelled main pipeline #%d", pipeline.ID))
			}
		} else if err == nil {
			r.log(mr.IID, "cancel_main_pipeline", "No main pipeline found after retries")
		}
	}
	// Step 5: If last MR and it merged, let main pipeline run naturally (done implicitly)

	return MRStatusMerged, ""
}

func (r *Runner) findCancellablePipeline(ctx context.Context, ref string) (*gitlab.Pipeline, error) {
	for _, status := range []string{"running", "pending", "created"} {
		pipelines, err := r.client.ListPipelines(ctx, r.projectID, ref, status)
		if err != nil {
			return nil, err
		}
		if len(pipelines) > 0 {
			return pipelines[0], nil
		}
	}
	return nil, nil
}

func (r *Runner) waitForMergeReady(ctx context.Context, mrIID int) (*gitlab.MergeRequest, error) {
	for {
		mr, err := r.client.GetMergeRequest(ctx, r.projectID, mrIID)
		if err != nil {
			return nil, err
		}

		switch mr.DetailedMergeStatus {
		case "mergeable":
			return mr, nil
		case "checking", "unchecked":
			// Still checking, poll again
		default:
			return mr, fmt.Errorf("merge status: %s", mr.DetailedMergeStatus)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.PollRebaseInterval):
		}
	}
}

func (r *Runner) waitForMRPipeline(ctx context.Context, mrIID int) (*gitlab.Pipeline, error) {
	for {
		pipeline, err := r.client.GetMergeRequestPipeline(ctx, r.projectID, mrIID)
		if err != nil {
			return nil, err
		}

		if pipeline != nil {
			switch pipeline.Status {
			case "success", "failed", "canceled", "skipped":
				return pipeline, nil
			}
		}

		// Wait before polling again
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.PollPipelineInterval):
		}
	}
}

func (r *Runner) waitForMainPipeline(ctx context.Context, targetBranch string) (*gitlab.Pipeline, error) {
	for {
		pipelines, err := r.client.ListPipelines(ctx, r.projectID, targetBranch, "")
		if err != nil {
			return nil, err
		}
		if len(pipelines) == 0 {
			return nil, nil
		}

		pipeline := pipelines[0]
		switch pipeline.Status {
		case "success", "failed", "canceled":
			return pipeline, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.PollPipelineInterval):
		}
	}
}

func (r *Runner) log(mrIID int, step, message string) {
	if r.logger != nil {
		r.logger(mrIID, step, message)
	}
}
