package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/emdash-ai/glmt/internal/gitlab"
)

// MRListModel is the bubbletea model for the MR list + selection screen.
type MRListModel struct {
	eligible   []*gitlab.MergeRequest
	ineligible []IneligibleMR
	selected   map[int]bool // MR IID -> selected
	cursor     int
	repoPath   string
}

// IneligibleMR pairs a merge request with the reason it cannot be selected.
type IneligibleMR struct {
	MR     *gitlab.MergeRequest
	Reason string // "draft", "pipeline failed", "pipeline running", "conflicts", "unresolved threads"
}

// Messages

type mrsLoadedMsg struct {
	mrs []*gitlab.MergeRequest
}

type startTrainMsg struct {
	mrs []*gitlab.MergeRequest
}

type changeRepoMsg struct{}

type refetchMRsMsg struct{}

// NewMRListModel creates a new MR list model for the given repo path.
func NewMRListModel(repoPath string) MRListModel {
	return MRListModel{
		selected: make(map[int]bool),
		repoPath: repoPath,
	}
}

// Init returns nil; the parent is responsible for fetching MRs and sending mrsLoadedMsg.
func (m MRListModel) Init() tea.Cmd {
	return nil
}

// classifyMR determines whether a merge request is eligible for the merge train.
func classifyMR(mr *gitlab.MergeRequest) (eligible bool, reason string) {
	if mr.Draft {
		return false, "draft"
	}
	if mr.HeadPipelineStatus == "running" || mr.HeadPipelineStatus == "pending" {
		return false, "pipeline running"
	}
	if mr.HeadPipelineStatus != "success" {
		return false, "pipeline failed"
	}
	if mr.DetailedMergeStatus != "mergeable" {
		return false, "conflicts"
	}
	if !mr.BlockingDiscussionsResolved {
		return false, "unresolved threads"
	}
	return true, ""
}

// totalCount returns the total number of MRs (eligible + ineligible).
func (m MRListModel) totalCount() int {
	return len(m.eligible) + len(m.ineligible)
}

// isIneligibleIndex returns true if the cursor is in the ineligible section.
func (m MRListModel) isIneligibleIndex(idx int) bool {
	return idx >= len(m.eligible)
}

// Update handles messages and key presses.
func (m MRListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case mrsLoadedMsg:
		return m.handleMRsLoaded(msg), nil

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	}

	return m, nil
}

func (m MRListModel) handleMRsLoaded(msg mrsLoadedMsg) MRListModel {
	m.eligible = nil
	m.ineligible = nil
	m.selected = make(map[int]bool)
	m.cursor = 0

	// Sort all MRs by CreatedAt ascending first.
	mrs := make([]*gitlab.MergeRequest, len(msg.mrs))
	copy(mrs, msg.mrs)
	sort.Slice(mrs, func(i, j int) bool {
		return mrs[i].CreatedAt < mrs[j].CreatedAt
	})

	for _, mr := range mrs {
		ok, reason := classifyMR(mr)
		if ok {
			m.eligible = append(m.eligible, mr)
		} else {
			m.ineligible = append(m.ineligible, IneligibleMR{MR: mr, Reason: reason})
		}
	}

	return m
}

func (m MRListModel) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total := m.totalCount()
	if total == 0 {
		// Handle quit even with no MRs.
		if msg.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "j":
		if m.cursor < total-1 {
			m.cursor++
		}

	case "shift+up", "K":
		m = m.reorderUp()

	case "shift+down", "J":
		m = m.reorderDown()

	case "space":
		m = m.toggleSelection()

	case "a":
		m = m.selectAllEligible()

	case "A":
		m = m.deselectAll()

	case "r":
		return m, func() tea.Msg { return changeRepoMsg{} }

	case "R":
		return m, func() tea.Msg { return refetchMRsMsg{} }

	case "enter":
		return m.startTrain()

	case "q":
		return m, tea.Quit
	}

	return m, nil
}

func (m MRListModel) toggleSelection() MRListModel {
	if m.isIneligibleIndex(m.cursor) {
		return m
	}
	mr := m.eligible[m.cursor]
	if m.selected[mr.IID] {
		delete(m.selected, mr.IID)
	} else {
		m.selected[mr.IID] = true
	}
	return m
}

func (m MRListModel) selectAllEligible() MRListModel {
	for _, mr := range m.eligible {
		m.selected[mr.IID] = true
	}
	return m
}

func (m MRListModel) deselectAll() MRListModel {
	m.selected = make(map[int]bool)
	return m
}

func (m MRListModel) reorderUp() MRListModel {
	// Can only reorder eligible MRs.
	if m.isIneligibleIndex(m.cursor) {
		return m
	}
	if m.cursor == 0 {
		return m
	}
	// Swap with previous eligible MR.
	m.eligible[m.cursor], m.eligible[m.cursor-1] = m.eligible[m.cursor-1], m.eligible[m.cursor]
	m.cursor--
	return m
}

func (m MRListModel) reorderDown() MRListModel {
	// Can only reorder eligible MRs.
	if m.isIneligibleIndex(m.cursor) {
		return m
	}
	if m.cursor >= len(m.eligible)-1 {
		return m
	}
	// Swap with next eligible MR.
	m.eligible[m.cursor], m.eligible[m.cursor+1] = m.eligible[m.cursor+1], m.eligible[m.cursor]
	m.cursor++
	return m
}

func (m MRListModel) startTrain() (tea.Model, tea.Cmd) {
	if m.SelectedCount() == 0 {
		return m, nil
	}
	// Collect selected MRs in their current eligible-list order.
	var mrs []*gitlab.MergeRequest
	for _, mr := range m.eligible {
		if m.selected[mr.IID] {
			mrs = append(mrs, mr)
		}
	}
	return m, func() tea.Msg { return startTrainMsg{mrs: mrs} }
}

// View renders the MR list screen.
func (m MRListModel) View() tea.View {
	var b strings.Builder

	fmt.Fprintf(&b, "  Repo: %s\n\n", m.repoPath)

	total := m.totalCount()
	if total == 0 {
		b.WriteString("  No merge requests found.\n\n")
		b.WriteString("  [R] refresh  [r] change repo  [q] quit\n")
		return tea.NewView(b.String())
	}

	fmt.Fprintf(&b, "  Open merge requests                          %d selected / %d eligible\n\n",
		m.SelectedCount(), len(m.eligible))

	// Render eligible MRs.
	for i, mr := range m.eligible {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		sel := "\u25cb" // ○
		if m.selected[mr.IID] {
			sel = "\u25cf" // ●
		}
		fmt.Fprintf(&b, "%s %s !%d  %s  @%s  %d commits\n",
			cursor, sel, mr.IID, mr.Title, mr.Author, mr.CommitCount)
	}

	if len(m.ineligible) > 0 && len(m.eligible) > 0 {
		b.WriteString("\n")
	}

	// Render ineligible MRs.
	for i, imr := range m.ineligible {
		idx := len(m.eligible) + i
		cursor := " "
		if idx == m.cursor {
			cursor = ">"
		}
		fmt.Fprintf(&b, "%s \u2717 !%d  %s  @%s  %d commits  [%s]\n",
			cursor, imr.MR.IID, imr.MR.Title, imr.MR.Author, imr.MR.CommitCount, imr.Reason)
	}

	b.WriteString("\n")
	b.WriteString("  [Space] toggle  [a] all  [Shift+\u2191\u2193] reorder  [R] refresh  [Enter] start  [q] quit\n")

	return tea.NewView(b.String())
}

// Exported getters for testing.

// Cursor returns the current cursor position.
func (m MRListModel) Cursor() int {
	return m.cursor
}

// Selected returns a sorted list of selected MR IIDs.
func (m MRListModel) Selected() []int {
	iids := make([]int, 0, len(m.selected))
	for iid := range m.selected {
		iids = append(iids, iid)
	}
	sort.Ints(iids)
	return iids
}

// Eligible returns the list of eligible MRs in their current order.
func (m MRListModel) Eligible() []*gitlab.MergeRequest {
	return m.eligible
}

// Ineligible returns the list of ineligible MRs.
func (m MRListModel) Ineligible() []IneligibleMR {
	return m.ineligible
}

// SelectedCount returns the number of selected MRs.
func (m MRListModel) SelectedCount() int {
	return len(m.selected)
}
