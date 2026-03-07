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
)

// allFixtureMRs returns a fresh slice of all test fixtures.
func allFixtureMRs() []*gitlab.MergeRequest {
	return []*gitlab.MergeRequest{
		eligibleMR1, eligibleMR2, draftMR, runningMR, conflictMR, unresolvedMR,
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
func sendKeyCmd(m MRListModel, key string) (MRListModel, tea.Cmd) {
	updated, cmd := m.Update(mrListKey(key))
	return updated.(MRListModel), cmd
}

func TestMRList_Classification(t *testing.T) {
	m := loadModel(allFixtureMRs())

	assert.Len(t, m.Eligible(), 2)
	assert.Len(t, m.Ineligible(), 4)

	// Check eligible MRs are the right ones (sorted by CreatedAt asc).
	assert.Equal(t, 42, m.Eligible()[0].IID)
	assert.Equal(t, 38, m.Eligible()[1].IID)

	// Check ineligible reasons.
	reasons := make(map[int]string)
	for _, imr := range m.Ineligible() {
		reasons[imr.MR.IID] = imr.Reason
	}
	assert.Equal(t, "draft", reasons[51])
	assert.Equal(t, "pipeline running", reasons[47])
	assert.Equal(t, "conflicts", reasons[40])
	assert.Equal(t, "unresolved threads", reasons[44])
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
	assert.Equal(t, 2, m.SelectedCount())
	assert.Equal(t, []int{38, 42}, m.Selected())
}

func TestMRList_DeselectAll(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Select all, then deselect all.
	m = sendKey(m, "a")
	assert.Equal(t, 2, m.SelectedCount())

	m = sendKey(m, "A")
	assert.Equal(t, 0, m.SelectedCount())
	assert.Empty(t, m.Selected())
}

func TestMRList_CannotSelectIneligible(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Move cursor to ineligible section (past 2 eligible MRs).
	m = sendKey(m, "down")
	m = sendKey(m, "down")
	assert.Equal(t, 2, m.Cursor()) // First ineligible index.

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

func TestMRList_ViewShowsBadges(t *testing.T) {
	m := loadModel(allFixtureMRs())

	view := m.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, "[draft]")
	assert.Contains(t, viewStr, "[pipeline running]")
	assert.Contains(t, viewStr, "[conflicts]")
	assert.Contains(t, viewStr, "[unresolved threads]")
}

func TestMRList_CurrentMRURL(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Cursor on first eligible MR.
	assert.Equal(t, eligibleMR1.WebURL, m.currentMRURL())

	// Move to second eligible MR.
	m = sendKey(m, "j")
	assert.Equal(t, eligibleMR2.WebURL, m.currentMRURL())

	// Move into ineligible section (first ineligible is draftMR after sorting).
	m = sendKey(m, "j")
	assert.Equal(t, m.Ineligible()[0].MR.WebURL, m.currentMRURL())

	// Move cursor past all items — should return empty.
	for i := 0; i < 10; i++ {
		m = sendKey(m, "j")
	}
	// Cursor is clamped to last item, so URL should still be valid.
	assert.NotEmpty(t, m.currentMRURL())
}

func TestMRList_CurrentMRURL_Empty(t *testing.T) {
	m := loadModel(nil)
	assert.Equal(t, "", m.currentMRURL())
}

func TestMRList_ViewShowsSelectionCount(t *testing.T) {
	m := loadModel(allFixtureMRs())

	// Select both eligible MRs.
	m = sendKey(m, "a")

	view := m.View()
	viewStr := view.Content

	assert.Contains(t, viewStr, "2 selected")
}
