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

// Runner executes the merge train.
type Runner struct {
	client    gitlab.Client
	projectID int
	logger    Logger
	// Configurable intervals for testing
	PollRebaseInterval       time.Duration
	PollPipelineInterval     time.Duration
	MaxCancelPipelineRetries int
	MaxMergeStatusRetries    int
	MaxMainPipelineAttempts  int
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
		MaxMergeStatusRetries:    5,
		MaxMainPipelineAttempts:  30,
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
	var lastMergeCommitSHA string
	anyMerged := false
	targetBranch := mrs[0].TargetBranch

	for i, mr := range mrs {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		isLast := i == len(mrs)-1

		status, skipReason, mergeCommitSHA := r.processMR(ctx, mr, isLast, &lastCancelledPipelineID)
		result.MRResults[i].Status = status
		result.MRResults[i].SkipReason = skipReason

		if status == MRStatusMerged {
			anyMerged = true
			lastMergeCommitSHA = mergeCommitSHA
		}

		// Check context after processing
		if err := ctx.Err(); err != nil {
			return result, err
		}

		// Step 6: If last MR was skipped and a prior MR merged, restart cancelled pipeline
		if isLast && status == MRStatusSkipped && anyMerged && lastCancelledPipelineID != 0 {
			r.restartCancelledPipeline(ctx, mr.IID, &lastCancelledPipelineID)
		}
	}

	// Step 7: Wait for main pipeline if any MR was merged or a pipeline was restarted
	if anyMerged {
		r.log(0, "main_pipeline_wait", "Waiting for main pipeline...")
		pipeline, err := r.waitForMainPipeline(ctx, targetBranch, lastMergeCommitSHA)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			r.log(0, "main_pipeline_done", fmt.Sprintf("Error waiting for main pipeline: %v", err))
		} else if pipeline != nil {
			result.MainPipelineURL = pipeline.WebURL
			result.MainPipelineStatus = pipeline.Status
			r.log(0, "main_pipeline_done", pipeline.Status)
		}
	}

	return result, nil
}

func (r *Runner) processMR(ctx context.Context, mr *gitlab.MergeRequest, isLast bool, lastCancelledPipelineID *int) (MRStatus, string, string) {
	return r.processMRAttempt(ctx, mr, isLast, lastCancelledPipelineID, false)
}

func (r *Runner) processMRAttempt(ctx context.Context, mr *gitlab.MergeRequest, isLast bool, lastCancelledPipelineID *int, isRetry bool) (MRStatus, string, string) {
	// Step 1: REBASE
	r.log(mr.IID, "rebase_wait", "Rebasing merge request...")
	_, err := r.client.RebaseMergeRequest(ctx, r.projectID, mr.IID)
	if err != nil {
		if ctx.Err() != nil {
			return MRStatusPending, "", ""
		}
		r.log(mr.IID, "skip", fmt.Sprintf("Rebase conflict: %v", err))
		return MRStatusSkipped, fmt.Sprintf("rebase conflict: %v", err), ""
	}
	r.log(mr.IID, "rebase", "Rebase successful")

	// Step 2: Branch pipeline is skipped after rebase to avoid redundant CI
	r.log(mr.IID, "pipeline_skip", "Branch pipeline skipped after rebase")

	// Step 3: MERGE (with SHA guard)
	// Wait for GitLab to finish its internal merge status check
	r.log(mr.IID, "merge_wait", "Waiting for merge readiness...")
	currentMR, err := r.waitForMergeReady(ctx, mr.IID)
	if err != nil {
		if ctx.Err() != nil {
			return MRStatusPending, "", ""
		}
		r.log(mr.IID, "skip", fmt.Sprintf("Not mergeable: %v", err))
		return MRStatusSkipped, fmt.Sprintf("not mergeable: %v", err), ""
	}

	r.log(mr.IID, "merge", fmt.Sprintf("Merging with SHA guard (sha=%s)...", currentMR.SHA))
	mergeCommitSHA, mergeErr := r.client.MergeMergeRequest(ctx, r.projectID, mr.IID, currentMR.SHA)
	if mergeErr != nil {
		if ctx.Err() != nil {
			return MRStatusPending, "", ""
		}
		if errors.Is(mergeErr, gitlab.ErrSHAMismatch) {
			if isRetry {
				// Second SHA mismatch — skip
				r.log(mr.IID, "skip", "SHA mismatch on retry, skipping")
				return MRStatusSkipped, "SHA mismatch on retry", ""
			}
			// First SHA mismatch — retry from step 1
			r.log(mr.IID, "merge_sha_mismatch", "SHA mismatch, retrying from rebase...")
			return r.processMRAttempt(ctx, mr, isLast, lastCancelledPipelineID, true)
		}
		if errors.Is(mergeErr, gitlab.ErrNotMergeable) {
			sha, status, skipReason := r.retryMergeOn405(ctx, mr.IID)
			if status != MRStatusMerged {
				return status, skipReason, ""
			}
			mergeCommitSHA = sha
		} else {
			r.log(mr.IID, "skip", fmt.Sprintf("Merge failed: %v", mergeErr))
			return MRStatusSkipped, fmt.Sprintf("merge failed: %v", mergeErr), ""
		}
	}
	r.log(mr.IID, "merge", "Merged successfully")
	if mergeCommitSHA == "" {
		r.log(mr.IID, "merge", "Warning: GitLab did not return merge commit SHA, pipeline lookup may be imprecise")
	}

	// Step 4: CANCEL MAIN PIPELINE (if more MRs remain)
	if !isLast {
		r.cancelIntermediatePipeline(ctx, mr, mergeCommitSHA, lastCancelledPipelineID)
	}
	// Step 5: If last MR and it merged, let main pipeline run naturally (done implicitly)

	return MRStatusMerged, "", mergeCommitSHA
}

// retryMergeOn405 handles the 405 race condition where GitLab reports "mergeable"
// but the merge API isn't ready. Retries waitForMergeReady + merge up to MaxMergeStatusRetries times.
func (r *Runner) retryMergeOn405(ctx context.Context, mrIID int) (string, MRStatus, string) {
	for attempt := 1; attempt <= r.MaxMergeStatusRetries; attempt++ {
		r.log(mrIID, "merge", fmt.Sprintf("Got 405, retrying after merge readiness check (%d/%d)...", attempt, r.MaxMergeStatusRetries))
		currentMR, err := r.waitForMergeReady(ctx, mrIID)
		if err != nil {
			if ctx.Err() != nil {
				return "", MRStatusPending, ""
			}
			r.log(mrIID, "skip", fmt.Sprintf("Not mergeable on 405 retry: %v", err))
			return "", MRStatusSkipped, fmt.Sprintf("not mergeable on 405 retry: %v", err)
		}
		mergeCommitSHA, mergeErr := r.client.MergeMergeRequest(ctx, r.projectID, mrIID, currentMR.SHA)
		if mergeErr == nil {
			return mergeCommitSHA, MRStatusMerged, ""
		}
		if ctx.Err() != nil {
			return "", MRStatusPending, ""
		}
		if !errors.Is(mergeErr, gitlab.ErrNotMergeable) {
			r.log(mrIID, "skip", fmt.Sprintf("Merge failed on 405 retry: %v", mergeErr))
			return "", MRStatusSkipped, fmt.Sprintf("merge failed: %v", mergeErr)
		}
	}
	r.log(mrIID, "skip", "Merge 405 retries exhausted, skipping")
	return "", MRStatusSkipped, "merge 405 retries exhausted"
}

// cancelIntermediatePipeline finds and cancels the main pipeline triggered by a merge,
// retrying up to MaxCancelPipelineRetries times if the pipeline hasn't appeared yet.
func (r *Runner) cancelIntermediatePipeline(ctx context.Context, mr *gitlab.MergeRequest, mergeCommitSHA string, lastCancelledPipelineID *int) {
	r.log(mr.IID, "cancel_main_pipeline_wait", "Cancelling main pipeline...")

	pipeline, err := r.findCancellablePipeline(ctx, mr.TargetBranch, mergeCommitSHA)
	if err != nil {
		r.log(mr.IID, "cancel_main_pipeline", fmt.Sprintf("Failed to list pipelines: %v", err))
	}

	for attempt := 0; pipeline == nil && err == nil && attempt < r.MaxCancelPipelineRetries; attempt++ {
		r.log(mr.IID, "cancel_main_pipeline_wait", fmt.Sprintf("No main pipeline found, retrying (%d/%d)...", attempt+1, r.MaxCancelPipelineRetries))
		select {
		case <-ctx.Done():
		case <-time.After(r.PollPipelineInterval):
			pipeline, err = r.findCancellablePipeline(ctx, mr.TargetBranch, mergeCommitSHA)
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

// restartCancelledPipeline restarts a previously cancelled main pipeline.
func (r *Runner) restartCancelledPipeline(ctx context.Context, mrIID int, lastCancelledPipelineID *int) {
	r.log(mrIID, "restart_pipeline", fmt.Sprintf("Last MR skipped - restarting cancelled main pipeline #%d", *lastCancelledPipelineID))
	retried, retryErr := r.client.RetryPipeline(ctx, r.projectID, *lastCancelledPipelineID)
	if retryErr != nil {
		r.log(mrIID, "restart_pipeline", fmt.Sprintf("Failed to restart pipeline: %v", retryErr))
	} else {
		r.log(mrIID, "restart_pipeline", fmt.Sprintf("Restarted main pipeline: %s", retried.WebURL))
		*lastCancelledPipelineID = 0
	}
}

func (r *Runner) findCancellablePipeline(ctx context.Context, ref, sha string) (*gitlab.Pipeline, error) {
	pipelines, err := r.client.ListPipelines(ctx, r.projectID, ref, "", sha)
	if err != nil {
		return nil, err
	}
	if len(pipelines) > 0 {
		switch pipelines[0].Status {
		case "running", "pending", "created":
			return pipelines[0], nil
		}
	}
	return nil, nil //nolint:nilnil // nil pipeline with no error means "not found"
}

func (r *Runner) waitForMergeReady(ctx context.Context, mrIID int) (*gitlab.MergeRequest, error) {
	staleRetries := 0
	for {
		mr, err := r.client.GetMergeRequest(ctx, r.projectID, mrIID)
		if err != nil {
			return nil, err
		}

		switch mr.DetailedMergeStatus {
		case "mergeable", "not_approved":
			return mr, nil
		case "checking", "unchecked":
			staleRetries = 0 // reset — GitLab is actively recalculating
		default:
			staleRetries++
			if staleRetries > r.MaxMergeStatusRetries {
				return mr, fmt.Errorf("merge status: %s", mr.DetailedMergeStatus)
			}
			r.log(mrIID, "merge", fmt.Sprintf("Merge status is '%s', retrying (%d/%d)...", mr.DetailedMergeStatus, staleRetries, r.MaxMergeStatusRetries))
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.PollRebaseInterval):
		}
	}
}

func (r *Runner) waitForMainPipeline(ctx context.Context, targetBranch, sha string) (*gitlab.Pipeline, error) {
	pipelineFound := false
	for range r.MaxMainPipelineAttempts {
		pipelines, err := r.client.ListPipelines(ctx, r.projectID, targetBranch, "", sha)
		if err != nil {
			return nil, err
		}
		if len(pipelines) == 0 {
			// No pipeline yet — poll again
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(r.PollPipelineInterval):
			}
			continue
		}

		pipeline := pipelines[0]
		if !pipelineFound {
			pipelineFound = true
			r.log(0, "main_pipeline_wait", pipeline.WebURL)
		}
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
	return nil, fmt.Errorf("timed out waiting for main pipeline after %d attempts", r.MaxMainPipelineAttempts)
}

func (r *Runner) log(mrIID int, step, message string) {
	if r.logger != nil {
		r.logger(mrIID, step, message)
	}
}
