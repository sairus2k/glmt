package tui

import (
	"strings"
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
	var msg tea.KeyPressMsg
	switch key {
	case "esc":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	case "enter":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "ctrl+c":
		msg = tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})
	case "up":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyUp})
	case "down":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	case "pgup":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp})
	case "pgdown":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown})
	case "home":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyHome})
	case "end":
		msg = tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd})
	default:
		if len(key) == 1 {
			msg = tea.KeyPressMsg(tea.Key{Code: rune(key[0]), Text: key})
		}
	}
	updated, cmd := m.Update(msg)
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
		{mrIID: 42, step: "merge_attempt", message: "Merging with SHA guard..."},
		{mrIID: 42, step: "merge", message: "a1b2c3"},
	}

	for _, msg := range steps {
		result, _ := m.Update(msg)
		m = result.(TrainRunModel)
	}

	// rebase_wait + rebase dedup into 1 entry, so 6 unique display entries
	require.Len(t, m.MRSteps()[0].Steps, 6)
	assert.Equal(t, "Rebase onto main", m.MRSteps()[0].Steps[0].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[0].Status)
	assert.Equal(t, "Pipeline running", m.MRSteps()[0].Steps[1].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[1].Status)
	assert.Equal(t, "Pipeline passed", m.MRSteps()[0].Steps[2].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[2].Status)
	assert.Equal(t, "Checking merge status", m.MRSteps()[0].Steps[3].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[3].Status)
	assert.Equal(t, "Merging", m.MRSteps()[0].Steps[4].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[4].Status)
	assert.Equal(t, "Merged", m.MRSteps()[0].Steps[5].Name)
	assert.Equal(t, StepDone, m.MRSteps()[0].Steps[5].Status)

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
		{mrIID: 42, step: "merge_attempt", message: "Merging with SHA guard..."},
		{mrIID: 42, step: "merge", message: "a1b2c3"},
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

func TestTrainRun_MergeAttemptStep(t *testing.T) {
	m := newTestTrainModel()

	msg := trainStepMsg{mrIID: 42, step: "merge_attempt", message: "Merging with SHA guard..."}
	result, _ := m.Update(msg)
	m = result.(TrainRunModel)

	require.Len(t, m.MRSteps()[0].Steps, 1)
	step := m.MRSteps()[0].Steps[0]
	assert.Equal(t, "Merging", step.Name)
	assert.Equal(t, StepRunning, step.Status)
	assert.Equal(t, "Merging with SHA guard...", step.Message)
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
		{mrIID: 42, step: "merge_attempt", message: "Merging..."},
		{mrIID: 42, step: "merge", message: "abc123"},
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

// --- Key handling tests for done/aborted/running states ---

func makeDoneModel() TrainRunModel {
	m := newTestTrainModel()
	result, _ := m.Update(trainDoneMsg{result: &train.Result{}})
	return result.(TrainRunModel)
}

func makeAbortedModel() TrainRunModel {
	m := newTestTrainModel()
	m, _ = sendTrainKey(m, "q") // abort while running
	return m
}

func TestTrainRun_QuitWhenDone(t *testing.T) {
	m := makeDoneModel()
	_, cmd := sendTrainKey(m, "q")

	require.NotNil(t, cmd)
	msg := cmd()
	assert.IsType(t, tea.QuitMsg{}, msg)
}

func TestTrainRun_CtrlCWhenDone(t *testing.T) {
	m := makeDoneModel()
	_, cmd := sendTrainKey(m, "ctrl+c")

	require.NotNil(t, cmd)
	msg := cmd()
	assert.IsType(t, tea.QuitMsg{}, msg)
}

func TestTrainRun_BackWhenDone(t *testing.T) {
	m := makeDoneModel()
	_, cmd := sendTrainKey(m, "esc")

	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(trainBackMsg)
	assert.True(t, ok)
}

func TestTrainRun_EnterWhenDone(t *testing.T) {
	m := makeDoneModel()
	_, cmd := sendTrainKey(m, "enter")

	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(trainBackMsg)
	assert.True(t, ok)
}

func TestTrainRun_RandomKeyWhenDone(t *testing.T) {
	m := makeDoneModel()
	_, cmd := sendTrainKey(m, "x")

	assert.Nil(t, cmd)
}

func TestTrainRun_EscAbortsWhenRunning(t *testing.T) {
	m := newTestTrainModel()
	m, cmd := sendTrainKey(m, "esc")

	assert.True(t, m.Aborted())
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(trainAbortMsg)
	assert.True(t, ok)
}

func TestTrainRun_CtrlCAbortsWhenRunning(t *testing.T) {
	m := newTestTrainModel()
	m, cmd := sendTrainKey(m, "ctrl+c")

	assert.True(t, m.Aborted())
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(trainAbortMsg)
	assert.True(t, ok)
}

func TestTrainRun_RandomKeyWhenRunning(t *testing.T) {
	m := newTestTrainModel()
	m, cmd := sendTrainKey(m, "x")

	assert.False(t, m.Aborted())
	assert.Nil(t, cmd)
}

func TestTrainRun_BackWhenAborted(t *testing.T) {
	m := makeAbortedModel()
	_, cmd := sendTrainKey(m, "enter")

	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(trainBackMsg)
	assert.True(t, ok)
}

func TestTrainRun_MapStepStatus_MergeSHAMismatch(t *testing.T) {
	assert.Equal(t, StepRunning, mapStepStatus("merge_sha_mismatch"))
}

func TestTrainRun_MapStepStatus_UnknownStep(t *testing.T) {
	assert.Equal(t, StepPending, mapStepStatus("some_unknown_step"))
}

// --- Viewport windowing and open-pipeline-URL key ---

// manyStepTrainModel builds a TrainRunModel with enough steps to overflow any
// reasonable contentHeight, including a main pipeline step with a URL.
func manyStepTrainModel(t *testing.T, height int) TrainRunModel {
	t.Helper()
	m := newTestTrainModel()
	for _, mr := range []*gitlab.MergeRequest{trainMR1, trainMR2, trainMR3} {
		steps := []trainStepMsg{
			{mrIID: mr.IID, step: "rebase_wait", message: "Rebasing..."},
			{mrIID: mr.IID, step: "rebase", message: "OK"},
			{mrIID: mr.IID, step: "pipeline_wait", message: "Waiting for pipeline..."},
			{mrIID: mr.IID, step: "pipeline_success", message: "Pipeline passed"},
			{mrIID: mr.IID, step: "merge_wait", message: "Waiting for merge readiness..."},
			{mrIID: mr.IID, step: "merge_attempt", message: "Merging with SHA guard..."},
			{mrIID: mr.IID, step: "merge", message: "abc123"},
		}
		for _, s := range steps {
			updated, _ := m.Update(s)
			m = updated.(TrainRunModel)
		}
	}
	// Main pipeline with URL.
	for _, s := range []trainStepMsg{
		{mrIID: 0, step: "main_pipeline_wait", message: "Waiting for main pipeline..."},
		{mrIID: 0, step: "main_pipeline_wait", message: "https://gitlab.example.com/pipeline/777"},
	} {
		updated, _ := m.Update(s)
		m = updated.(TrainRunModel)
	}
	m.contentHeight = height
	return m
}

func TestTrainRun_ViewFitsInContentHeight(t *testing.T) {
	m := manyStepTrainModel(t, 10)
	view := m.View()

	rendered := strings.Split(view.Content, "\n")
	assert.LessOrEqual(t, len(rendered), m.contentHeight, "view must not exceed contentHeight")
}

func TestTrainRun_AutoFollowKeepsLatestVisible(t *testing.T) {
	m := manyStepTrainModel(t, 8)
	// Latest emitted step carries the URL — must appear in the rendered view.
	view := m.View()
	assert.Contains(t, view.Content, "https://gitlab.example.com/pipeline/777")
}

func TestTrainRun_ScrollUpDisengagesAutoFollow(t *testing.T) {
	m := manyStepTrainModel(t, 8)
	assert.True(t, m.autoFollow)

	m, _ = sendTrainKey(m, "up")
	assert.False(t, m.autoFollow)
	assert.Positive(t, m.scrollOffset)
}

func TestTrainRun_EndReengagesAutoFollow(t *testing.T) {
	m := manyStepTrainModel(t, 8)

	// Scroll up a few times to disengage auto-follow.
	for range 3 {
		m, _ = sendTrainKey(m, "up")
	}
	assert.False(t, m.autoFollow)

	m, _ = sendTrainKey(m, "end")
	assert.True(t, m.autoFollow)

	view := m.View()
	assert.Contains(t, view.Content, "https://gitlab.example.com/pipeline/777")
}

func TestTrainRun_ScrollIndicators(t *testing.T) {
	m := manyStepTrainModel(t, 8)

	// Auto-follow at bottom → only ↑ shown.
	view := m.View()
	assert.Contains(t, view.Content, "↑ more above")
	assert.NotContains(t, view.Content, "↓ more below")

	// Scroll to top → only ↓ shown.
	m, _ = sendTrainKey(m, "home")
	view = m.View()
	assert.NotContains(t, view.Content, "↑ more above")
	assert.Contains(t, view.Content, "↓ more below")

	// Scroll down by one from top → both shown.
	m, _ = sendTrainKey(m, "down")
	view = m.View()
	assert.Contains(t, view.Content, "↑ more above")
	assert.Contains(t, view.Content, "↓ more below")
}

func TestTrainRun_OpenKeyNoopWithoutURL(t *testing.T) {
	captured := ""
	prev := openBrowser
	openBrowser = func(url string) error {
		captured = url
		return nil
	}
	defer func() { openBrowser = prev }()

	m := newTestTrainModel()
	before := m
	m, cmd := sendTrainKey(m, "o")

	assert.Nil(t, cmd)
	assert.Empty(t, captured, "openBrowser must not be invoked without a URL")
	assert.Equal(t, before.scrollOffset, m.scrollOffset)
	assert.Equal(t, before.autoFollow, m.autoFollow)
	assert.False(t, m.aborted)
}

func TestTrainRun_OpenKeyInvokesBrowserWithURL(t *testing.T) {
	captured := ""
	prev := openBrowser
	openBrowser = func(url string) error {
		captured = url
		return nil
	}
	defer func() { openBrowser = prev }()

	m := newTestTrainModel()
	updated, _ := m.Update(trainStepMsg{
		mrIID:   0,
		step:    "main_pipeline_wait",
		message: "https://gitlab.example.com/pipeline/42",
	})
	m = updated.(TrainRunModel)

	_, cmd := sendTrainKey(m, "o")
	assert.Nil(t, cmd)
	assert.Equal(t, "https://gitlab.example.com/pipeline/42", captured)
}

// TestTrainRun_ViewRendersAtLeastOneRowAutoFollow guards against a bug where
// a tight contentHeight combined with autoFollow=true produced an empty view
// because scrollOffset was clamped past the last renderable position.
func TestTrainRun_ViewRendersAtLeastOneRowAutoFollow(t *testing.T) {
	for _, ch := range []int{1, 2, 3} {
		m := manyStepTrainModel(t, ch)
		assert.True(t, m.autoFollow, "ch=%d precondition: autoFollow should be true", ch)
		view := m.View()
		assert.NotEmptyf(t, view.Content, "ch=%d: autoFollow view must not be empty", ch)
	}
}

// TestTrainRun_ViewNeverExceedsTightContentHeight exercises the corner case
// where both scroll indicators must appear within a tight contentHeight —
// previously the two-pass algorithm could emit contentHeight+1 rows.
func TestTrainRun_ViewNeverExceedsTightContentHeight(t *testing.T) {
	for _, ch := range []int{1, 2, 3, 4, 5, 6, 8, 12} {
		m := manyStepTrainModel(t, ch)
		// Scroll into the middle so both indicators are candidates.
		m, _ = sendTrainKey(m, "home")
		for range max(ch/2, 1) {
			m, _ = sendTrainKey(m, "down")
		}
		view := m.View()
		rendered := strings.Split(view.Content, "\n")
		assert.LessOrEqualf(t, len(rendered), ch,
			"contentHeight=%d: rendered %d rows", ch, len(rendered))
	}
}

func TestTrainRun_ScrollKeyNoOpWhenContentHeightUnset(t *testing.T) {
	m := manyStepTrainModel(t, 8)
	m.contentHeight = 0 // simulate layout not yet computed

	before := m
	m, _ = sendTrainKey(m, "up")
	assert.Equal(t, before.scrollOffset, m.scrollOffset)
	assert.Equal(t, before.autoFollow, m.autoFollow)
}

func TestTrainRun_ScrollKeyNoOpWhenContentFits(t *testing.T) {
	// Tall viewport, short content → maxOff <= 0, scroll keys must be no-ops.
	m := newTestTrainModel()
	m.contentHeight = 500

	before := m
	m, _ = sendTrainKey(m, "up")
	assert.Equal(t, before.scrollOffset, m.scrollOffset)
	assert.Equal(t, before.autoFollow, m.autoFollow)
}

func TestTrainRun_PgUpPgDown(t *testing.T) {
	m := manyStepTrainModel(t, 8)

	// pgup disengages auto-follow and scrolls up by visible rows.
	m, _ = sendTrainKey(m, "pgup")
	assert.False(t, m.autoFollow)
	pgupOffset := m.scrollOffset
	assert.Less(t, pgupOffset, m.scrollOffset+1, "pgup must leave offset below bottom")

	// pgdown eventually re-engages auto-follow at the bottom.
	m, _ = sendTrainKey(m, "pgdown")
	m, _ = sendTrainKey(m, "pgdown")
	assert.True(t, m.autoFollow, "pgdown past maxOff should re-engage auto-follow")
}

func TestTrainRun_DownAtBottomReengagesAutoFollow(t *testing.T) {
	m := manyStepTrainModel(t, 8)

	// Scroll up once to disengage.
	m, _ = sendTrainKey(m, "up")
	require.False(t, m.autoFollow)

	// Step down until we hit the bottom. With visible=1 content row per press,
	// one "down" is enough to reach maxOff from one-up-from-bottom.
	m, _ = sendTrainKey(m, "down")
	assert.True(t, m.autoFollow, "reaching maxOff via down should re-engage auto-follow")
}

func TestTrainRun_ViewWhenDone(t *testing.T) {
	m := newTestTrainModel()
	m.contentHeight = 100 // all content fits, no scrolling
	result, _ := m.Update(trainDoneMsg{result: &train.Result{}})
	m = result.(TrainRunModel)

	view := m.View()
	assert.Contains(t, view.Content, "Finished processing")
}

func TestTrainRun_ViewWhenAborted(t *testing.T) {
	m := newTestTrainModel()
	m.contentHeight = 100
	m, _ = sendTrainKey(m, "esc")

	view := m.View()
	assert.Contains(t, view.Content, "Aborted")
}

func TestTrainRun_KeyHintsWhenDone(t *testing.T) {
	m := makeDoneModel()
	hints := m.KeyHints()

	var keys []string
	for _, h := range hints {
		keys = append(keys, h.Key)
	}
	assert.Contains(t, keys, "[Enter]")
	assert.Contains(t, keys, "[q]")
	assert.NotContains(t, keys, "[Esc]")
}

func TestTrainRun_KeyHintsWhenRunning(t *testing.T) {
	m := newTestTrainModel()
	hints := m.KeyHints()

	var keys []string
	for _, h := range hints {
		keys = append(keys, h.Key)
	}
	assert.Contains(t, keys, "[Esc]")
	assert.NotContains(t, keys, "[Enter]")
}

func TestTrainRun_KeyHintsIncludeOpenOnlyWhenURLPresent(t *testing.T) {
	m := newTestTrainModel()

	// No URL yet — "[o]" hint absent.
	hints := m.KeyHints()
	for _, h := range hints {
		assert.NotEqual(t, "[o]", h.Key, "[o] hint must be absent before main pipeline URL arrives")
	}

	// After a main pipeline URL arrives, "[o]" hint present.
	updated, _ := m.Update(trainStepMsg{
		mrIID:   0,
		step:    "main_pipeline_wait",
		message: "https://gitlab.example.com/pipeline/42",
	})
	m = updated.(TrainRunModel)

	hints = m.KeyHints()
	found := false
	for _, h := range hints {
		if h.Key == "[o]" {
			found = true
			break
		}
	}
	assert.True(t, found, "[o] hint must appear once a main pipeline URL is known")
}
