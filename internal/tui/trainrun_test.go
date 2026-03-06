package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/emdash-ai/glmt/internal/gitlab"
	"github.com/emdash-ai/glmt/internal/train"
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

	assert.Equal(t, 3, len(m.MRSteps()))
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

	msg := trainStepMsg{mrIID: 42, step: "rebase", message: "Rebase successful"}
	result, _ := m.Update(msg)
	m = result.(TrainRunModel)

	require.Len(t, m.MRSteps()[0].Steps, 1)
	step := m.MRSteps()[0].Steps[0]
	assert.Equal(t, "Rebase onto main", step.Name)
	assert.Equal(t, StepDone, step.Status)
	assert.Equal(t, "Rebase successful", step.Message)
}

func TestTrainRun_MultipleSteps(t *testing.T) {
	m := newTestTrainModel()

	steps := []trainStepMsg{
		{mrIID: 42, step: "rebase", message: "Rebase successful"},
		{mrIID: 42, step: "pipeline_wait", message: "Waiting for pipeline..."},
		{mrIID: 42, step: "pipeline_success", message: "Pipeline passed"},
		{mrIID: 42, step: "merge", message: "sha: a1b2c3"},
		{mrIID: 42, step: "cancel_main_pipeline", message: "next MR pending"},
	}

	for _, msg := range steps {
		result, _ := m.Update(msg)
		m = result.(TrainRunModel)
	}

	require.Len(t, m.MRSteps()[0].Steps, 5)
	assert.Equal(t, "Rebase onto main", m.MRSteps()[0].Steps[0].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[0].Status)
	assert.Equal(t, "Pipeline running", m.MRSteps()[0].Steps[1].Name)
	assert.Equal(t, StepRunning, m.MRSteps()[0].Steps[1].Status)
	assert.Equal(t, "Pipeline passed", m.MRSteps()[0].Steps[2].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[2].Status)
	assert.Equal(t, "Merged", m.MRSteps()[0].Steps[3].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[3].Status)
	assert.Equal(t, "Main pipeline cancelled", m.MRSteps()[0].Steps[4].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[4].Status)

	// MR 2 and 3 should still be empty.
	assert.Empty(t, m.MRSteps()[1].Steps)
	assert.Empty(t, m.MRSteps()[2].Steps)
}

func TestTrainRun_SecondMR(t *testing.T) {
	m := newTestTrainModel()

	// Process first MR.
	steps := []trainStepMsg{
		{mrIID: 42, step: "rebase", message: "Rebase successful"},
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

func TestTrainRun_ViewShowsProgress(t *testing.T) {
	m := newTestTrainModel()

	// Add a step for the first MR.
	result, _ := m.Update(trainStepMsg{mrIID: 42, step: "rebase", message: "Rebase successful"})
	m = result.(TrainRunModel)

	view := m.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, "Fix auth token expiry")
	assert.Contains(t, viewStr, "!42")
	assert.Contains(t, viewStr, "Rebase onto main")
	assert.Contains(t, viewStr, "Add rate limiting")
	assert.Contains(t, viewStr, "Refactor user model")
}

func TestTrainRun_ViewShowsSkipped(t *testing.T) {
	m := newTestTrainModel()

	// Skip the first MR.
	result, _ := m.Update(trainStepMsg{mrIID: 42, step: "skip", message: "rebase conflict"})
	m = result.(TrainRunModel)

	view := m.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, "SKIPPED")
	assert.Contains(t, viewStr, "!42")
	assert.Contains(t, viewStr, "Fix auth token expiry")
}
