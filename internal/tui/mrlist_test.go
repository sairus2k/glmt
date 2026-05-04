package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test fixtures.
var (
	eligibleMR1 = &gitlab.MergeRequest{
		IID: 42, Title: "Fix auth token expiry", Author: "alice",
		HeadPipelineStatus: "success", DetailedMergeStatus: "mergeable",
		BlockingDiscussionsResolved: true, CreatedAt: "2025-01-10T00:00:00Z",
		WebURL: "https://gitlab.com/myteam/myrepo/-/merge_requests/42",
	}
	eligibleMR2 = &gitlab.MergeRequest{
		IID: 38, Title: "Add rate limiting", Author: "bob",
		HeadPipelineStatus: "success", DetailedMergeStatus: "mergeable",
		BlockingDiscussionsResolved: true, CreatedAt: "2025-01-11T00:00:00Z",
		WebURL: "https://gitlab.com/myteam/myrepo/-/merge_requests/38",
	}
	draftMR = &gitlab.MergeRequest{
		IID: 51, Title: "WIP: new dashboard", Author: "dave",
		Draft: true, HeadPipelineStatus: "success", DetailedMergeStatus: "mergeable",
		BlockingDiscussionsResolved: true,
		WebURL:                      "https://gitlab.com/myteam/myrepo/-/merge_requests/51",
	}
	runningMR = &gitlab.MergeRequest{
		IID: 47, Title: "Add oauth flow", Author: "eve",
		HeadPipelineStatus: "running", DetailedMergeStatus: "mergeable",
		BlockingDiscussionsResolved: true,
		WebURL:                      "https://gitlab.com/myteam/myrepo/-/merge_requests/47",
	}
	conflictMR = &gitlab.MergeRequest{
		IID: 40, Title: "Update deps", Author: "grace",
		HeadPipelineStatus: "success", DetailedMergeStatus: "broken_status",
		BlockingDiscussionsResolved: true,
		WebURL:                      "https://gitlab.com/myteam/myrepo/-/merge_requests/40",
	}
	unresolvedMR = &gitlab.MergeRequest{
		IID: 44, Title: "DB migration", Author: "frank",
		HeadPipelineStatus: "success", DetailedMergeStatus: "mergeable",
		BlockingDiscussionsResolved: false,
		WebURL:                      "https://gitlab.com/myteam/myrepo/-/merge_requests/44",
	}
	unresolvedStatusMR = &gitlab.MergeRequest{
		IID: 45, Title: "Refactor logging", Author: "heidi",
		HeadPipelineStatus: "success", DetailedMergeStatus: "discussions_not_resolved",
		BlockingDiscussionsResolved: true,
		WebURL:                      "https://gitlab.com/myteam/myrepo/-/merge_requests/45",
	}
	needRebaseMR = &gitlab.MergeRequest{
		IID: 46, Title: "Upgrade Go version", Author: "ivan",
		HeadPipelineStatus: "success", DetailedMergeStatus: "need_rebase",
		BlockingDiscussionsResolved: true, CreatedAt: "2025-01-12T00:00:00Z",
		WebURL: "https://gitlab.com/myteam/myrepo/-/merge_requests/46",
	}
	uncheckedMR = &gitlab.MergeRequest{
		IID: 60, Title: "Add caching layer", Author: "judy",
		HeadPipelineStatus: "success", DetailedMergeStatus: "unchecked",
		BlockingDiscussionsResolved: true, CreatedAt: "2025-01-13T00:00:00Z",
		WebURL: "https://gitlab.com/myteam/myrepo/-/merge_requests/60",
	}
	checkingMR = &gitlab.MergeRequest{
		IID: 61, Title: "Fix race condition", Author: "karl",
		HeadPipelineStatus: "success", DetailedMergeStatus: "checking",
		BlockingDiscussionsResolved: true, CreatedAt: "2025-01-14T00:00:00Z",
		WebURL: "https://gitlab.com/myteam/myrepo/-/merge_requests/61",
	}
	notApprovedMR = &gitlab.MergeRequest{
		IID: 62, Title: "Add feature flag", Author: "lisa",
		HeadPipelineStatus: "success", DetailedMergeStatus: "not_approved",
		BlockingDiscussionsResolved: true, CreatedAt: "2025-01-15T00:00:00Z",
		WebURL: "https://gitlab.com/myteam/myrepo/-/merge_requests/62",
	}
)

// allFixtureMRs returns a fresh slice of all test fixtures.
func allFixtureMRs() []*gitlab.MergeRequest {
	return []*gitlab.MergeRequest{
		eligibleMR1, eligibleMR2, draftMR, runningMR, conflictMR, unresolvedMR,
		unresolvedStatusMR, needRebaseMR, notApprovedMR,
	}
}

// loadModel creates an MRListModel and sends mrsLoadedMsg with the given MRs.
func loadModel(mrs []*gitlab.MergeRequest) MRListModel {
	m := NewMRListModel("myteam/myrepo")
	updated, _ := m.Update(mrsLoadedMsg{mrs: mrs})
	return updated.(MRListModel)
}

// mrListKey creates a KeyPressMsg for MR list tests.
func mrListKey(key string) tea.KeyPressMsg {
	switch key {
	case "up":
		return specialKeyPress(tea.KeyUp)
	case "down":
		return specialKeyPress(tea.KeyDown)
	case "enter":
		return specialKeyPress(tea.KeyEnter)
	case "esc":
		return specialKeyPress(tea.KeyEscape)
	case "f1":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyF1})
	case "shift+up":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyUp, Mod: tea.ModShift})
	case "shift+down":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyDown, Mod: tea.ModShift})
	case " ":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeySpace, Text: " "})
	default:
		// For printable characters, use keyPress from setup_test.go.
		return keyPress(key)
	}
}

// sendKey sends a KeyPressMsg and returns the updated model.
func sendKey(m MRListModel, key string) MRListModel {
	updated, _ := m.Update(mrListKey(key))
	return updated.(MRListModel)
}

// sendKeyCmd sends a KeyPressMsg and returns both the updated model and the command.
func sendKeyCmd(m MRListModel, key string) (MRListModel, tea.Cmd) { //nolint:unparam // cmd useful for future tests
	updated, cmd := m.Update(mrListKey(key))
	return updated.(MRListModel), cmd
}

func TestMRList_Classification(t *testing.T) {
	m := loadModel(allFixtureMRs())

	assert.Len(t, m.Eligible(), 4)
	assert.Len(t, m.Ineligible(), 5)

	// Check eligible MRs are the right ones (sorted by CreatedAt asc).
	assert.Equal(t, 42, m.Eligible()[0].IID)
	assert.Equal(t, 38, m.Eligible()[1].IID)
	assert.Equal(t, 46, m.Eligible()[2].IID) // need_rebase is eligible
	assert.Equal(t, 62, m.Eligible()[3].IID) // not_approved is eligible

	// Check ineligible reasons.
	reasons := make(map[int]string)
	for _, imr := range m.Ineligible() {
		reasons[imr.MR.IID] = imr.Reason
	}
	assert.Equal(t, "draft", reasons[51])
	assert.Equal(t, "pipeline running", reasons[47])
	assert.Equal(t, "broken_status", reasons[40])
	assert.Equal(t, "unresolved threads", reasons[44])
	assert.Equal(t, "unresolved threads", reasons[45])
}

func TestMRList_CursorMovement(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Cursor starts at 0.
	assert.Equal(t, 0, m.Cursor())

	// Move down.
	m = sendKey(m, "down")
	assert.Equal(t, 1, m.Cursor())

	// Move down again (into ineligible section).
	m = sendKey(m, "down")
	assert.Equal(t, 2, m.Cursor())

	// Move up.
	m = sendKey(m, "up")
	assert.Equal(t, 1, m.Cursor())

	// Move up to top.
	m = sendKey(m, "up")
	assert.Equal(t, 0, m.Cursor())

	// Move up at top does nothing.
	m = sendKey(m, "up")
	assert.Equal(t, 0, m.Cursor())

	// Test k/j alternative keys.
	m = sendKey(m, "j")
	assert.Equal(t, 1, m.Cursor())

	m = sendKey(m, "k")
	assert.Equal(t, 0, m.Cursor())
}

func TestMRList_ToggleSelection(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Select first eligible MR.
	m = sendKey(m, " ")
	assert.Equal(t, []int{42}, m.Selected())

	// Move down and select second.
	m = sendKey(m, "down")
	m = sendKey(m, " ")
	assert.Equal(t, []int{38, 42}, m.Selected())

	// Deselect second.
	m = sendKey(m, " ")
	assert.Equal(t, []int{42}, m.Selected())
}

func TestMRList_SelectAll(t *testing.T) {
	m := loadModel(allFixtureMRs())

	m = sendKey(m, "a")
	assert.Equal(t, 4, m.SelectedCount())
	assert.Equal(t, []int{38, 42, 46, 62}, m.Selected())
}

func TestMRList_DeselectAll(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Select all, then deselect all.
	m = sendKey(m, "a")
	assert.Equal(t, 4, m.SelectedCount())

	m = sendKey(m, "A")
	assert.Equal(t, 0, m.SelectedCount())
	assert.Empty(t, m.Selected())
}

func TestMRList_CannotSelectIneligible(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Move cursor to ineligible section (past 4 eligible MRs).
	m = sendKey(m, "down")
	m = sendKey(m, "down")
	m = sendKey(m, "down")
	m = sendKey(m, "down")
	assert.Equal(t, 4, m.Cursor()) // First ineligible index.

	// Try to toggle selection — should do nothing.
	m = sendKey(m, " ")
	assert.Equal(t, 0, m.SelectedCount())
}

func TestMRList_Reorder(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Initial order: MR 42, MR 38.
	assert.Equal(t, 42, m.Eligible()[0].IID)
	assert.Equal(t, 38, m.Eligible()[1].IID)

	// shift+down moves MR 42 down, swapping with MR 38.
	m = sendKey(m, "shift+down")
	assert.Equal(t, 38, m.Eligible()[0].IID)
	assert.Equal(t, 42, m.Eligible()[1].IID)
	assert.Equal(t, 1, m.Cursor())
}

func TestMRList_ReorderUpAtTop(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Cursor at top, shift+up should do nothing.
	m = sendKey(m, "shift+up")
	assert.Equal(t, 0, m.Cursor())
	assert.Equal(t, 42, m.Eligible()[0].IID)
}

func TestMRList_StartTrain(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Select both MRs.
	m = sendKey(m, " ")    // Select MR 42.
	m = sendKey(m, "down") // Move to MR 38.
	m = sendKey(m, " ")    // Select MR 38.

	// Press Enter.
	_, cmd := sendKeyCmd(m, "enter")
	require.NotNil(t, cmd)

	// Execute the command to get the message.
	msg := cmd()
	stm, ok := msg.(startTrainMsg)
	require.True(t, ok)

	// MRs should be in eligible list order.
	require.Len(t, stm.mrs, 2)
	assert.Equal(t, 42, stm.mrs[0].IID)
	assert.Equal(t, 38, stm.mrs[1].IID)
}

func TestMRList_StartDisabledEmpty(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Press Enter with nothing selected.
	_, cmd := sendKeyCmd(m, "enter")
	assert.Nil(t, cmd, "Enter with 0 selected should produce no command")
}

func TestMRList_Refetch(t *testing.T) {
	m := loadModel(allFixtureMRs())

	_, cmd := sendKeyCmd(m, "R")
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(refetchMRsMsg)
	assert.True(t, ok)
}

func TestMRList_ChangeRepo(t *testing.T) {
	m := loadModel(allFixtureMRs())

	_, cmd := sendKeyCmd(m, "r")
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(changeRepoMsg)
	assert.True(t, ok)
}

func TestMRList_ViewShowsStatusIcons(t *testing.T) {
	m := loadModel(allFixtureMRs())

	view := m.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, "\u270E")         // draft icon
	assert.Contains(t, viewStr, "\u25C7")         // unresolved threads icon
	assert.Contains(t, viewStr, "\u2717")         // failed/unknown icon
	assert.Contains(t, viewStr, spinnerFrames[0]) // running pipeline spinner
}

func TestMRList_CurrentMRURL(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Cursor on first eligible MR.
	assert.Equal(t, eligibleMR1.WebURL, m.currentMRURL())

	// Move to second eligible MR.
	m = sendKey(m, "j")
	assert.Equal(t, eligibleMR2.WebURL, m.currentMRURL())

	// Move to third eligible MR (need_rebase).
	m = sendKey(m, "j")
	assert.Equal(t, needRebaseMR.WebURL, m.currentMRURL())

	// Move to fourth eligible MR (not_approved).
	m = sendKey(m, "j")
	assert.Equal(t, notApprovedMR.WebURL, m.currentMRURL())

	// Move into ineligible section (first ineligible is draftMR after sorting).
	m = sendKey(m, "j")
	assert.Equal(t, m.Ineligible()[0].MR.WebURL, m.currentMRURL())

	// Move cursor past all items — should return empty.
	for range 10 {
		m = sendKey(m, "j")
	}
	// Cursor is clamped to last item, so URL should still be valid.
	assert.NotEmpty(t, m.currentMRURL())
}

func TestMRList_CurrentMRURL_Empty(t *testing.T) {
	m := loadModel(nil)
	assert.Empty(t, m.currentMRURL())
}

func TestMRList_ViewShowsSelectionCount(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Select both eligible MRs.
	m = sendKey(m, "a")

	view := m.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, "4 selected")
}

func TestMRList_UncheckedSetsRefreshing(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1, uncheckedMR}
	m := loadModel(mrs)

	assert.True(t, m.Refreshing(), "refreshing should be true when unchecked MRs present")
}

func TestMRList_NoUncheckedClearsRefreshing(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1, eligibleMR2, draftMR}
	m := loadModel(mrs)

	assert.False(t, m.Refreshing(), "refreshing should be false with only resolved statuses")
}

func TestMRList_BackgroundUpdatePreservesSelection(t *testing.T) {
	// Initial load with unchecked MRs.
	mrs := []*gitlab.MergeRequest{eligibleMR1, eligibleMR2, uncheckedMR}
	m := loadModel(mrs)
	assert.True(t, m.Refreshing())

	// Select MR 42.
	m = sendKey(m, " ")
	assert.Equal(t, []int{42}, m.Selected())

	// Move cursor to MR 38.
	m = sendKey(m, "down")
	assert.Equal(t, 1, m.Cursor())

	// Simulate background refetch (loading is already false).
	updated, _ := m.Update(mrsLoadedMsg{mrs: mrs})
	m = updated.(MRListModel)

	// Selection and cursor preserved.
	assert.Equal(t, []int{42}, m.Selected())
	assert.Equal(t, 1, m.Cursor())
}

func TestMRList_BackgroundUpdateClampsCursor(t *testing.T) {
	// Load 3 eligible + 1 unchecked = 4 total items.
	mrs := []*gitlab.MergeRequest{eligibleMR1, eligibleMR2, needRebaseMR, uncheckedMR}
	m := loadModel(mrs)

	// Move cursor to last item (index 3 = uncheckedMR in ineligible).
	m = sendKey(m, "down")
	m = sendKey(m, "down")
	m = sendKey(m, "down")
	assert.Equal(t, 3, m.Cursor())

	// Background refetch with fewer MRs (unchecked resolved to eligible).
	resolvedMR := &gitlab.MergeRequest{
		IID: 60, Title: "Add caching layer", Author: "judy",
		HeadPipelineStatus: "success", DetailedMergeStatus: "mergeable",
		BlockingDiscussionsResolved: true, CreatedAt: "2025-01-13T00:00:00Z",
	}
	fewerMRs := []*gitlab.MergeRequest{eligibleMR1, resolvedMR}
	updated, _ := m.Update(mrsLoadedMsg{mrs: fewerMRs})
	m = updated.(MRListModel)

	// Cursor clamped to new total - 1.
	assert.Equal(t, 1, m.Cursor())
	assert.False(t, m.Refreshing())
}

func TestMRList_SpinnerTicksWhenRefreshing(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1, checkingMR}
	m := loadModel(mrs)
	assert.True(t, m.Refreshing())

	// Set refreshing manually since loadModel sets loading=false.
	// refreshing should already be true from hasUncheckedMRs.
	updated, cmd := m.Update(spinnerTickMsg{})
	m = updated.(MRListModel)

	assert.NotNil(t, cmd, "spinner tick should return next tick cmd when refreshing")
	assert.Equal(t, 1, m.spinnerFrame)
}

func TestMRList_ViewShowsSpinnerForUnchecked(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1, checkingMR, uncheckedMR}
	m := loadModel(mrs)

	view := m.View()
	viewStr := view.Content

	// The spinner icon should appear for checking/unchecked MRs.
	assert.Contains(t, viewStr, spinnerFrames[0])
}

func TestMRList_RunningPipelineSetsRefreshing(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1, runningMR}
	m := loadModel(mrs)

	assert.True(t, m.Refreshing(), "refreshing should be true when running pipeline present")
	assert.True(t, m.HasRunningPipelines())
}

func TestMRList_RunningPipelineClearsRefreshing(t *testing.T) {
	// Initial load: pipeline running → refreshing.
	mrs := []*gitlab.MergeRequest{eligibleMR1, runningMR}
	m := loadModel(mrs)
	assert.True(t, m.Refreshing())

	// Background refetch: pipeline completed (success) → now eligible.
	resolvedMR := &gitlab.MergeRequest{
		IID: 47, Title: "Add oauth flow", Author: "eve",
		HeadPipelineStatus: "success", DetailedMergeStatus: "mergeable",
		BlockingDiscussionsResolved: true,
	}
	updated, _ := m.Update(mrsLoadedMsg{mrs: []*gitlab.MergeRequest{eligibleMR1, resolvedMR}})
	m = updated.(MRListModel)

	assert.False(t, m.Refreshing(), "refreshing should be false after pipeline completed")
	assert.False(t, m.HasRunningPipelines())
}

func TestMRList_ViewShowsSpinnerForRunningPipeline(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1, runningMR}
	m := loadModel(mrs)

	view := m.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, spinnerFrames[0])
}

func TestMRList_BackgroundUpdatePreservesSelectionWithRunningPipeline(t *testing.T) {
	// Initial load with running pipeline MR.
	mrs := []*gitlab.MergeRequest{eligibleMR1, eligibleMR2, runningMR}
	m := loadModel(mrs)
	assert.True(t, m.Refreshing())

	// Select MR 42.
	m = sendKey(m, " ")
	assert.Equal(t, []int{42}, m.Selected())

	// Move cursor to MR 38.
	m = sendKey(m, "down")
	assert.Equal(t, 1, m.Cursor())

	// Simulate background refetch (loading is already false).
	updated, _ := m.Update(mrsLoadedMsg{mrs: mrs})
	m = updated.(MRListModel)

	// Selection and cursor preserved.
	assert.Equal(t, []int{42}, m.Selected())
	assert.Equal(t, 1, m.Cursor())
	assert.True(t, m.Refreshing())
}

func TestMRList_ViewShowsRefreshingIndicator(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1, eligibleMR2}
	m := loadModel(mrs)
	m.refreshing = true
	m.userRefresh = true

	view := m.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, "Refreshing...")
	assert.NotContains(t, viewStr, "Loading merge requests...")
	// The MR list should still be visible.
	assert.Contains(t, viewStr, eligibleMR1.Title)
	assert.Contains(t, viewStr, eligibleMR2.Title)
}

func TestMRList_HelpModalToggle(t *testing.T) {
	m := loadModel(allFixtureMRs())

	m = sendKey(m, "f1")
	assert.True(t, m.HelpVisible())
	view := m.View().Content
	assert.Contains(t, view, "MR Status Icons")
	assert.Contains(t, view, "Pipeline failed")

	m = sendKey(m, "f1")
	assert.False(t, m.HelpVisible())
	assert.NotContains(t, m.View().Content, "MR Status Icons")
}

func TestMRList_HelpModalClosesOnEsc(t *testing.T) {
	m := loadModel(allFixtureMRs())

	m = sendKey(m, "f1")
	require.True(t, m.HelpVisible())

	updated, cmd := sendKeyCmd(m, "esc")
	assert.False(t, updated.HelpVisible())
	assert.Nil(t, cmd, "Esc should close the modal, not quit the app")
}

func TestMRList_HelpModalClosesOnQ(t *testing.T) {
	m := loadModel(allFixtureMRs())

	m = sendKey(m, "f1")
	require.True(t, m.HelpVisible())

	updated, cmd := sendKeyCmd(m, "q")
	assert.False(t, updated.HelpVisible())
	assert.Nil(t, cmd, "q should close the modal, not quit the app")
}

func TestMRList_HelpModalSuppressesKeys(t *testing.T) {
	m := loadModel(allFixtureMRs())

	m = sendKey(m, "f1")
	require.True(t, m.HelpVisible())

	cursorBefore := m.Cursor()
	selectedBefore := m.SelectedCount()

	m = sendKey(m, "down")
	assert.Equal(t, cursorBefore, m.Cursor(), "down should be ignored while modal open")

	m = sendKey(m, " ")
	assert.Equal(t, selectedBefore, m.SelectedCount(), "space should be ignored while modal open")

	_, cmd := sendKeyCmd(m, "R")
	assert.Nil(t, cmd, "R should not trigger refetch while modal open")
}

func TestMRList_HelpKeyHints(t *testing.T) {
	m := loadModel(allFixtureMRs())

	hints := m.KeyHints()
	hasF1 := false
	for _, h := range hints {
		if h.Key == "[F1]" {
			hasF1 = true
			break
		}
	}
	assert.True(t, hasF1, "closed-modal hints should include [F1]")

	m = sendKey(m, "f1")
	hints = m.KeyHints()
	require.Len(t, hints, 1)
	assert.Equal(t, "[F1/Esc]", hints[0].Key)
}

func TestIneligibleIcon(t *testing.T) {
	tests := []struct {
		reason   string
		wantChar string
	}{
		{"pipeline failed", "\u2717"},
		{"blocked", "\u2717"},
		{"pipeline running", spinnerFrames[0]},
		{"checking", spinnerFrames[0]},
		{"unchecked", spinnerFrames[0]},
		{"draft", "\u270E"},
		{"unresolved threads", "\u25C7"},
		{"requested changes", "\u21BB"},
		{"some_unknown_status", "\u2717"},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			icon := ineligibleIcon(tt.reason, 0)
			assert.Contains(t, icon, tt.wantChar)
		})
	}
}
