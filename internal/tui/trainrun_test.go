package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/sairus2k/glmt/internal/train"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test fixtures.
var (
	trainMR1 = &gitlab.MergeRequest{IID: 42, Title: "Fix auth token expiry", TargetBranch: "main"}
	trainMR2 = &gitlab.MergeRequest{IID: 38, Title: "Add rate limiting", TargetBranch: "main"}
	trainMR3 = &gitlab.MergeRequest{IID: 35, Title: "Refactor user model", TargetBranch: "main"}
)

func newTestTrainModel() TrainRunModel {
	return NewTrainRunModel([]*gitlab.MergeRequest{trainMR1, trainMR2, trainMR3})
}

// sendTrainKey sends a KeyPressMsg to a TrainRunModel and returns the updated model and command.
func sendTrainKey(m TrainRunModel, key string) (TrainRunModel, tea.Cmd) {
	var code rune
	var text string
	switch key {
	case "q":
		code = 'q'
		text = "q"
	default:
		if len(key) == 1 {
			code = rune(key[0])
			text = key
		}
	}
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: code, Text: text}))
	return updated.(TrainRunModel), cmd
}

func TestTrainRun_InitialState(t *testing.T) {
	m := newTestTrainModel()

	assert.Len(t, m.MRSteps(), 3)
	assert.Equal(t, 0, m.CurrentMR())
	assert.False(t, m.Done())
	assert.False(t, m.Aborted())
	assert.Nil(t, m.Result())

	// Each MR should have an empty step log.
	for _, steps := range m.MRSteps() {
		assert.Empty(t, steps.Steps)
	}
}

func TestTrainRun_StepUpdate(t *testing.T) {
	m := newTestTrainModel()

	msg := trainStepMsg{mrIID: 42, step: "rebase_wait", message: "Rebasing merge request..."}
	result, _ := m.Update(msg)
	m = result.(TrainRunModel)

	require.Len(t, m.MRSteps()[0].Steps, 1)
	step := m.MRSteps()[0].Steps[0]
	assert.Equal(t, "Rebase onto main", step.Name)
	assert.Equal(t, StepRunning, step.Status)
	assert.Equal(t, "Rebasing merge request...", step.Message)
}

func TestTrainRun_MultipleSteps(t *testing.T) {
	m := newTestTrainModel()

	steps := []trainStepMsg{
		{mrIID: 42, step: "rebase_wait", message: "Rebasing merge request..."},
		{mrIID: 42, step: "rebase", message: "Rebase successful"},
		{mrIID: 42, step: "pipeline_wait", message: "Waiting for pipeline..."},
		{mrIID: 42, step: "pipeline_success", message: "Pipeline passed"},
		{mrIID: 42, step: "merge_wait", message: "Waiting for merge readiness..."},
		{mrIID: 42, step: "merge", message: "sha: a1b2c3"},
		{mrIID: 42, step: "cancel_main_pipeline_wait", message: "Cancelling main pipeline..."},
		{mrIID: 42, step: "cancel_main_pipeline", message: "next MR pending"},
	}

	for _, msg := range steps {
		result, _ := m.Update(msg)
		m = result.(TrainRunModel)
	}

	// rebase_wait + rebase dedup into 1 entry, so 7 unique display entries
	require.Len(t, m.MRSteps()[0].Steps, 7)
	assert.Equal(t, "Rebase onto main", m.MRSteps()[0].Steps[0].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[0].Status)
	assert.Equal(t, "Pipeline running", m.MRSteps()[0].Steps[1].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[1].Status)
	assert.Equal(t, "Pipeline passed", m.MRSteps()[0].Steps[2].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[2].Status)
	assert.Equal(t, "Checking merge status", m.MRSteps()[0].Steps[3].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[3].Status)
	assert.Equal(t, "Merged", m.MRSteps()[0].Steps[4].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[4].Status)
	assert.Equal(t, "Waiting for main pipeline", m.MRSteps()[0].Steps[5].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[5].Status)
	assert.Equal(t, "Main pipeline cancelled", m.MRSteps()[0].Steps[6].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[6].Status)

	// MR 2 and 3 should still be empty.
	assert.Empty(t, m.MRSteps()[1].Steps)
	assert.Empty(t, m.MRSteps()[2].Steps)
}

func TestTrainRun_SecondMR(t *testing.T) {
	m := newTestTrainModel()

	// Process first MR.
	steps := []trainStepMsg{
		{mrIID: 42, step: "rebase_wait", message: "Rebasing merge request..."},
		{mrIID: 42, step: "rebase", message: "Rebase successful"},
		{mrIID: 42, step: "merge_wait", message: "Waiting for merge readiness..."},
		{mrIID: 42, step: "merge", message: "sha: a1b2c3"},
	}
	for _, msg := range steps {
		result, _ := m.Update(msg)
		m = result.(TrainRunModel)
	}

	assert.Equal(t, 0, m.CurrentMR())

	// Process second MR — currentMR should advance.
	msg := trainStepMsg{mrIID: 38, step: "rebase", message: "Rebase successful"}
	result, _ := m.Update(msg)
	m = result.(TrainRunModel)

	assert.Equal(t, 1, m.CurrentMR())
	require.Len(t, m.MRSteps()[1].Steps, 1)
	assert.Equal(t, "Rebase onto main", m.MRSteps()[1].Steps[0].Name)
}

func TestTrainRun_SkippedMR(t *testing.T) {
	m := newTestTrainModel()

	msg := trainStepMsg{mrIID: 42, step: "skip", message: "rebase conflict"}
	result, _ := m.Update(msg)
	m = result.(TrainRunModel)

	require.Len(t, m.MRSteps()[0].Steps, 1)
	step := m.MRSteps()[0].Steps[0]
	assert.Equal(t, "Skipped: rebase conflict", step.Name)
	assert.Equal(t, StepSkipped, step.Status)
}

func TestTrainRun_Done(t *testing.T) {
	m := newTestTrainModel()

	trainResult := &train.Result{
		MRResults: []train.MRResult{
			{MR: trainMR1, Status: train.MRStatusMerged},
			{MR: trainMR2, Status: train.MRStatusMerged},
			{MR: trainMR3, Status: train.MRStatusSkipped, SkipReason: "pipeline failed"},
		},
		MainPipelineURL:    "https://gitlab.example.com/pipeline/123",
		MainPipelineStatus: "success",
	}

	result, _ := m.Update(trainDoneMsg{result: trainResult})
	m = result.(TrainRunModel)

	assert.True(t, m.Done())
	require.NotNil(t, m.Result())
	assert.Equal(t, "success", m.Result().MainPipelineStatus)
	assert.Len(t, m.Result().MRResults, 3)
}

func TestTrainRun_Abort(t *testing.T) {
	m := newTestTrainModel()

	// Verify not aborted initially.
	assert.False(t, m.Aborted())

	// Press 'q' to abort.
	m, cmd := sendTrainKey(m, "q")

	assert.True(t, m.Aborted())

	// The command should produce a trainAbortMsg.
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(trainAbortMsg)
	assert.True(t, ok)
}

func TestTrainRun_PreviousRunningStepCompletedOnNewStep(t *testing.T) {
	m := newTestTrainModel()

	// Send pipeline_wait (running step)
	result, _ := m.Update(trainStepMsg{mrIID: 42, step: "pipeline_wait", message: "Waiting..."})
	m = result.(TrainRunModel)

	require.Len(t, m.LogEntries(), 1)
	assert.Equal(t, StepRunning, m.LogEntries()[0].Status)

	// Send pipeline_success — previous running step should become done
	result, _ = m.Update(trainStepMsg{mrIID: 42, step: "pipeline_success", message: "Pipeline passed"})
	m = result.(TrainRunModel)

	require.Len(t, m.LogEntries(), 2)
	assert.Equal(t, StepDone, m.LogEntries()[0].Status, "previous running step should be marked done")
	assert.Equal(t, StepDone, m.LogEntries()[1].Status)

	// Also verify mrSteps are updated
	require.Len(t, m.MRSteps()[0].Steps, 2)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[0].Status, "per-MR running step should be marked done")
}

func TestTrainRun_ViewShowsProgress(t *testing.T) {
	m := newTestTrainModel()

	// Add a step for the first MR.
	result, _ := m.Update(trainStepMsg{mrIID: 42, step: "rebase", message: "Rebase successful"})
	m = result.(TrainRunModel)

	view := m.View()
	viewStr := view.Content

	// Legend line should show all MR titles
	assert.Contains(t, viewStr, "Fix auth token expiry")
	assert.Contains(t, viewStr, "Add rate limiting")
	assert.Contains(t, viewStr, "Refactor user model")

	// Log entry should show MR ref and step name
	assert.Contains(t, viewStr, "!42")
	assert.Contains(t, viewStr, "Rebase onto main")
}

func TestTrainRun_DeduplicateConsecutiveSameStep(t *testing.T) {
	m := newTestTrainModel()

	// rebase_wait and rebase share the same display name — should dedup
	result, _ := m.Update(trainStepMsg{mrIID: 42, step: "rebase_wait", message: "Rebasing merge request..."})
	m = result.(TrainRunModel)

	result, _ = m.Update(trainStepMsg{mrIID: 42, step: "rebase", message: "Rebase successful"})
	m = result.(TrainRunModel)

	// Should have only one entry with the updated message (dedup by display name)
	require.Len(t, m.LogEntries(), 1, "rebase_wait + rebase should dedup into one entry")
	assert.Equal(t, "Rebase successful", m.LogEntries()[0].Message)
	assert.Equal(t, "Rebase onto main", m.LogEntries()[0].Name)

	// Per-MR steps should also have only one entry
	require.Len(t, m.MRSteps()[0].Steps, 1, "per-MR steps should also deduplicate")
	assert.Equal(t, "Rebase successful", m.MRSteps()[0].Steps[0].Message)
}

func TestTrainRun_DedupedRunningStepKeepsSpinner(t *testing.T) {
	m := newTestTrainModel()

	// First cancel_main_pipeline_wait — should be running with spinner
	result, _ := m.Update(trainStepMsg{mrIID: 42, step: "cancel_main_pipeline_wait", message: "Cancelling main pipeline..."})
	m = result.(TrainRunModel)

	require.Len(t, m.LogEntries(), 1)
	assert.Equal(t, StepRunning, m.LogEntries()[0].Status, "initial cancel_main_pipeline_wait should be running")

	// Second cancel_main_pipeline_wait (retry) — same display name, triggers dedup
	result, _ = m.Update(trainStepMsg{mrIID: 42, step: "cancel_main_pipeline_wait", message: "No main pipeline found, retrying (1/3)..."})
	m = result.(TrainRunModel)

	require.Len(t, m.LogEntries(), 1, "should dedup into one entry")
	assert.Equal(t, StepRunning, m.LogEntries()[0].Status, "deduped cancel_main_pipeline_wait must stay running (spinner visible)")
	assert.Equal(t, "No main pipeline found, retrying (1/3)...", m.LogEntries()[0].Message)

	// Also verify per-MR steps
	require.Len(t, m.MRSteps()[0].Steps, 1, "per-MR steps should also dedup")
	assert.Equal(t, StepRunning, m.MRSteps()[0].Steps[0].Status, "per-MR deduped step must stay running")
}

func TestTrainRun_ViewShowsMainPipelineURL(t *testing.T) {
	m := newTestTrainModel()

	// Initial main_pipeline_wait without URL
	result, _ := m.Update(trainStepMsg{mrIID: 0, step: "main_pipeline_wait", message: "Waiting for main pipeline..."})
	m = result.(TrainRunModel)

	view := m.View()
	assert.Contains(t, view.Content, "Main pipeline running")
	assert.NotContains(t, view.Content, "https://gitlab.example.com/pipeline/99")

	// Update with URL (dedup updates the message)
	result, _ = m.Update(trainStepMsg{mrIID: 0, step: "main_pipeline_wait", message: "https://gitlab.example.com/pipeline/99"})
	m = result.(TrainRunModel)

	require.Len(t, m.LogEntries(), 1, "should dedup into one entry")
	view = m.View()
	assert.Contains(t, view.Content, "Main pipeline running")
	assert.Contains(t, view.Content, "https://gitlab.example.com/pipeline/99")
}

func TestTrainRun_MainPipelineStepsTracked(t *testing.T) {
	m := newTestTrainModel()

	result, _ := m.Update(trainStepMsg{mrIID: 0, step: "main_pipeline_wait", message: "Waiting..."})
	m = result.(TrainRunModel)

	require.Len(t, m.MainPipelineSteps(), 1)
	assert.Equal(t, "Main pipeline running", m.MainPipelineSteps()[0].Name)
	assert.Equal(t, StepRunning, m.MainPipelineSteps()[0].Status)
}

func TestTrainRun_MainPipelineStepsDedup(t *testing.T) {
	m := newTestTrainModel()

	result, _ := m.Update(trainStepMsg{mrIID: 0, step: "main_pipeline_wait", message: "Waiting..."})
	m = result.(TrainRunModel)
	result, _ = m.Update(trainStepMsg{mrIID: 0, step: "main_pipeline_wait", message: "https://gitlab.example.com/pipeline/99"})
	m = result.(TrainRunModel)

	require.Len(t, m.MainPipelineSteps(), 1, "should dedup main pipeline steps")
	assert.Equal(t, "https://gitlab.example.com/pipeline/99", m.MainPipelineSteps()[0].Message)
}

func TestTrainRun_MainPipelineStepsCompletedOnNewStep(t *testing.T) {
	m := newTestTrainModel()

	// Send main_pipeline_wait (running)
	result, _ := m.Update(trainStepMsg{mrIID: 0, step: "main_pipeline_wait", message: "Waiting..."})
	m = result.(TrainRunModel)
	assert.Equal(t, StepRunning, m.MainPipelineSteps()[0].Status)

	// Send main_pipeline_done — previous running step should be marked done
	result, _ = m.Update(trainStepMsg{mrIID: 0, step: "main_pipeline_done", message: "success"})
	m = result.(TrainRunModel)

	require.Len(t, m.MainPipelineSteps(), 2)
	assert.Equal(t, StepDone, m.MainPipelineSteps()[0].Status, "previous running main pipeline step should be done")
	assert.Equal(t, StepDone, m.MainPipelineSteps()[1].Status)
}

func TestTrainRun_ViewHierarchicalLayout(t *testing.T) {
	m := NewTrainRunModel([]*gitlab.MergeRequest{trainMR1, trainMR2})

	// Complete first MR
	steps := []trainStepMsg{
		{mrIID: 42, step: "rebase_wait", message: "Rebasing..."},
		{mrIID: 42, step: "rebase", message: "OK"},
		{mrIID: 42, step: "merge_wait", message: "Waiting..."},
		{mrIID: 42, step: "merge", message: "sha: abc123"},
	}
	for _, msg := range steps {
		result, _ := m.Update(msg)
		m = result.(TrainRunModel)
	}
	// Start second MR
	result, _ := m.Update(trainStepMsg{mrIID: 38, step: "rebase_wait", message: "Rebasing..."})
	m = result.(TrainRunModel)

	view := m.View()
	v := view.Content

	// MR headers present
	assert.Contains(t, v, "!42")
	assert.Contains(t, v, "Fix auth token expiry")
	assert.Contains(t, v, "!38")
	assert.Contains(t, v, "Add rate limiting")

	// Tree characters present for expanded MR steps
	assert.Contains(t, v, "├─")
	assert.Contains(t, v, "└─")

	// No legend line (the old " · " joined format)
	assert.NotContains(t, v, " · ")
}

func TestTrainRun_ViewShowsSkipped(t *testing.T) {
	m := newTestTrainModel()

	// Skip the first MR.
	result, _ := m.Update(trainStepMsg{mrIID: 42, step: "skip", message: "rebase conflict"})
	m = result.(TrainRunModel)

	view := m.View()
	viewStr := view.Content

	// Log entry should show the skip step with MR ref
	assert.Contains(t, viewStr, "!42")
	assert.Contains(t, viewStr, "Skipped: rebase conflict")
}
