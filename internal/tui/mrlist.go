package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/gitlab"
)

// MRListModel is the bubbletea model for the MR list + selection screen.
type MRListModel struct {
	eligible      []*gitlab.MergeRequest
	ineligible    []IneligibleMR
	selected      map[int]bool // MR IID -> selected
	cursor        int
	repoPath      string
	loading       bool
	spinnerFrame  int
	errMsg        string
	contentHeight int
	scrollOffset  int
}

// IneligibleMR pairs a merge request with the reason it cannot be selected.
type IneligibleMR struct {
	MR     *gitlab.MergeRequest
	Reason string // "draft", "pipeline failed", "pipeline running", "conflicts", "unresolved threads"
}

// Messages

type mrsLoadedMsg struct {
	mrs []*gitlab.MergeRequest
	err error
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
		loading:  true,
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
	case spinnerTickMsg:
		if m.loading {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, spinnerTick()
		}
		return m, nil
	case mrsLoadedMsg:
		return m.handleMRsLoaded(msg), nil

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)
	}

	return m, nil
}

func (m MRListModel) handleMRsLoaded(msg mrsLoadedMsg) MRListModel {
	m.loading = false
	m.eligible = nil
	m.ineligible = nil
	m.selected = make(map[int]bool)
	m.cursor = 0
	m.errMsg = ""

	if msg.err != nil {
		m.errMsg = msg.err.Error()
		return m
	}

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
	key := msg.String()

	if key == "ctrl+c" || key == "esc" {
		return m, tea.Quit
	}

	if total == 0 {
		// Handle quit even with no MRs.
		if key == "q" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.adjustScroll()
		}

	case "down", "j":
		if m.cursor < total-1 {
			m.cursor++
			m.adjustScroll()
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
	m.adjustScroll()
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
	m.adjustScroll()
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

	b.WriteString("  ")
	b.WriteString(sBold.Styled("Repo:"))
	b.WriteString(" ")
	b.WriteString(sRunning.Styled(m.repoPath))
	b.WriteString("\n\n")

	if m.loading {
		b.WriteString("  ")
		b.WriteString(sRunning.Styled(spinnerFrames[m.spinnerFrame] + " Loading merge requests..."))
		b.WriteString("\n")
		return tea.NewView(b.String())
	}

	total := m.totalCount()
	if total == 0 {
		b.WriteString("  ")
		if m.errMsg != "" {
			b.WriteString(sError.Styled("Error: " + m.errMsg))
		} else {
			b.WriteString(sFaint.Styled("No merge requests found."))
		}
		b.WriteString("\n")
		return tea.NewView(b.String())
	}

	b.WriteString("  ")
	b.WriteString(sBold.Styled("Open merge requests"))
	b.WriteString("  ")
	b.WriteString(sFaint.Styled("select and reorder to set merge sequence"))
	b.WriteString("  ")
	b.WriteString(sSuccess.Styled(fmt.Sprintf("%d selected", m.SelectedCount())))
	b.WriteString(sFaint.Styled(fmt.Sprintf(" / %d eligible", len(m.eligible))))
	b.WriteString("\n\n")

	// Build all item lines, then render only the visible window.
	type itemLine struct {
		idx  int // global index (eligible + ineligible)
		text string
	}
	var items []itemLine

	for i, mr := range m.eligible {
		var lb strings.Builder
		if i == m.cursor {
			lb.WriteString(sCursor.Styled(">"))
		} else {
			lb.WriteString(" ")
		}
		lb.WriteString(" ")
		if m.selected[mr.IID] {
			lb.WriteString(sSelected.Styled("\u25cf")) // ●
		} else {
			lb.WriteString(sFaint.Styled("\u25cb")) // ○
		}
		approvals := ""
		if mr.ApprovalCount > 0 {
			approvals = "  " + sSuccess.Styled(fmt.Sprintf("✓ %d", mr.ApprovalCount))
		}
		fmt.Fprintf(&lb, " %s  %s  %s  %s%s",
			sBold.Styled(fmt.Sprintf("!%d", mr.IID)),
			mr.Title,
			sFaint.Styled("@"+mr.Author),
			sFaint.Styled(fmt.Sprintf("%d commits", mr.CommitCount)),
			approvals)
		items = append(items, itemLine{idx: i, text: lb.String()})
	}

	// Separator between eligible and ineligible.
	if len(m.ineligible) > 0 && len(m.eligible) > 0 {
		items = append(items, itemLine{idx: -1, text: ""})
	}

	for i, imr := range m.ineligible {
		var lb strings.Builder
		idx := len(m.eligible) + i
		if idx == m.cursor {
			lb.WriteString(sCursor.Styled(">"))
		} else {
			lb.WriteString(" ")
		}
		approvals := ""
		if imr.MR.ApprovalCount > 0 {
			approvals = "  " + sDim.Styled(fmt.Sprintf("✓ %d", imr.MR.ApprovalCount))
		}
		fmt.Fprintf(&lb, " %s %s  %s  %s  %s%s  %s",
			sError.Styled("\u2717"),
			sDim.Styled(fmt.Sprintf("!%d", imr.MR.IID)),
			sDim.Styled(imr.MR.Title),
			sDim.Styled("@"+imr.MR.Author),
			sDim.Styled(fmt.Sprintf("%d commits", imr.MR.CommitCount)),
			approvals,
			sWarning.Styled(fmt.Sprintf("[%s]", imr.Reason)))
		items = append(items, itemLine{idx: idx, text: lb.String()})
	}

	// Render visible window.
	visible := m.visibleItems()
	end := m.scrollOffset + visible
	if end > len(items) {
		end = len(items)
	}
	start := m.scrollOffset
	if start > len(items) {
		start = len(items)
	}
	for i := start; i < end; i++ {
		b.WriteString(items[i].text)
		b.WriteString("\n")
	}

	return tea.NewView(b.String())
}

// KeyHints returns the keyboard hints for the current state.
func (m MRListModel) KeyHints() []KeyHint {
	if m.loading || m.totalCount() == 0 {
		return []KeyHint{
			{"[R]", "refresh"},
			{"[r]", "change repo"},
			{"[Esc]", "quit"},
		}
	}
	return []KeyHint{
		{"[Space]", "toggle"},
		{"[a]", "all"},
		{"[Shift+↑↓]", "reorder"},
		{"[R]", "refresh"},
		{"[r]", "change repo"},
		{"[Enter]", "start"},
		{"[Esc]", "quit"},
	}
}

const mrListHeaderLines = 4 // repo, blank, section header, blank

// visibleItems returns the number of items that fit in the content area.
func (m MRListModel) visibleItems() int {
	if m.contentHeight <= mrListHeaderLines {
		return m.totalCount() + 1 // +1 for separator; no constraint
	}
	v := m.contentHeight - mrListHeaderLines
	if v < 1 {
		v = 1
	}
	return v
}

// adjustScroll adjusts scrollOffset to keep the cursor visible.
func (m *MRListModel) adjustScroll() {
	visible := m.visibleItems()
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}
	if m.cursor >= m.scrollOffset+visible {
		m.scrollOffset = m.cursor - visible + 1
	}
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
