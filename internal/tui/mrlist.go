package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
	refreshing    bool // background refresh in progress (list visible, spinner badges animate)
	userRefresh   bool // true only when user explicitly pressed R
	spinnerFrame  int
	errMsg        string
	contentHeight int
	scrollOffset  int
	width         int
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

type backgroundRefetchMsg struct{}

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
	if mr.HeadPipelineStatus != "success" && mr.HeadPipelineStatus != "skipped" {
		return false, "pipeline failed"
	}
	switch mr.DetailedMergeStatus {
	case "mergeable", "need_rebase", "not_approved":
		// mergeable: ready; need_rebase: train handles rebase; not_approved: GitLab Free can't enforce approvals
	case "discussions_not_resolved":
		return false, "unresolved threads"
	case "blocked_status":
		return false, "blocked"
	case "requested_changes":
		return false, "requested changes"
	case "checking", "unchecked":
		return false, mr.DetailedMergeStatus
	default:
		return false, mr.DetailedMergeStatus
	}
	if !mr.BlockingDiscussionsResolved {
		return false, "unresolved threads"
	}
	return true, ""
}

// ineligibleIcon returns a category-specific icon for an ineligible MR reason.
func ineligibleIcon(reason string, spinnerFrame int) string {
	switch reason {
	case "pipeline failed", "blocked":
		return sError.Styled("\u2717")
	case "pipeline running", "checking", "unchecked":
		return sRunning.Styled(spinnerFrames[spinnerFrame])
	case "draft":
		return sFaint.Styled("\u270E")
	case "unresolved threads":
		return sWarning.Styled("\u25C7")
	case "requested changes":
		return sWarning.Styled("\u21BB")
	default:
		return sError.Styled("\u2717")
	}
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
		if m.loading || m.refreshing {
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
	wasLoading := m.loading

	if wasLoading {
		// Initial load or manual refresh: reset everything.
		m.loading = false
		m.eligible = nil
		m.ineligible = nil
		m.selected = make(map[int]bool)
		m.cursor = 0
		m.scrollOffset = 0
		m.errMsg = ""

		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.refreshing = false
			return m
		}

		mrs := classifyAndSort(msg.mrs)
		m.eligible = mrs.eligible
		m.ineligible = mrs.ineligible
		m.refreshing = m.needsAutoRefresh()
		m.userRefresh = false
		return m
	}

	// Background refetch: preserve selection, cursor, scroll.
	if msg.err != nil {
		// Silently ignore errors during background refetch.
		return m
	}

	prevSelected := m.selected
	mrs := classifyAndSort(msg.mrs)
	m.eligible = mrs.eligible
	m.ineligible = mrs.ineligible

	// Restore selection by IID.
	m.selected = make(map[int]bool)
	for _, mr := range m.eligible {
		if prevSelected[mr.IID] {
			m.selected[mr.IID] = true
		}
	}

	// Clamp cursor.
	total := m.totalCount()
	if total == 0 {
		m.cursor = 0
	} else if m.cursor >= total {
		m.cursor = total - 1
	}

	m.refreshing = m.needsAutoRefresh()
	m.userRefresh = false
	return m
}

// classifyResult holds classified MRs.
type classifyResult struct {
	eligible   []*gitlab.MergeRequest
	ineligible []IneligibleMR
}

// classifyAndSort sorts MRs by CreatedAt and classifies them.
func classifyAndSort(raw []*gitlab.MergeRequest) classifyResult {
	mrs := make([]*gitlab.MergeRequest, len(raw))
	copy(mrs, raw)
	sort.Slice(mrs, func(i, j int) bool {
		return mrs[i].CreatedAt < mrs[j].CreatedAt
	})

	var r classifyResult
	for _, mr := range mrs {
		ok, reason := classifyMR(mr)
		if ok {
			r.eligible = append(r.eligible, mr)
		} else {
			r.ineligible = append(r.ineligible, IneligibleMR{MR: mr, Reason: reason})
		}
	}
	return r
}

// hasUncheckedMRs returns true if any ineligible MR has "checking" or "unchecked" status.
func (m MRListModel) hasUncheckedMRs() bool {
	for _, imr := range m.ineligible {
		if imr.Reason == "checking" || imr.Reason == "unchecked" {
			return true
		}
	}
	return false
}

// hasRunningPipelines returns true if any ineligible MR has "pipeline running" status.
func (m MRListModel) hasRunningPipelines() bool {
	for _, imr := range m.ineligible {
		if imr.Reason == "pipeline running" {
			return true
		}
	}
	return false
}

// needsAutoRefresh returns true if the list should auto-refresh (unchecked MRs or running pipelines).
func (m MRListModel) needsAutoRefresh() bool {
	return m.hasUncheckedMRs() || m.hasRunningPipelines()
}

// HasUncheckedMRs is an exported getter for app.go to query.
func (m MRListModel) HasUncheckedMRs() bool {
	return m.hasUncheckedMRs()
}

// HasRunningPipelines is an exported getter for app.go to query.
func (m MRListModel) HasRunningPipelines() bool {
	return m.hasRunningPipelines()
}

func (m MRListModel) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total := m.totalCount()
	key := msg.String()

	if key == "ctrl+c" || key == "esc" {
		return m, tea.Quit
	}

	// Always allow these actions regardless of MR count.
	switch key {
	case "r":
		return m, func() tea.Msg { return changeRepoMsg{} }
	case "R":
		return m, func() tea.Msg { return refetchMRsMsg{} }
	}

	if total == 0 {
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

	case "enter":
		return m.startTrain()

	case "o":
		if url := m.currentMRURL(); url != "" {
			_ = openBrowser(url)
		}

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

// tableLayout holds precomputed column widths for the MR table.
type tableLayout struct {
	maxIID       int
	maxAuthor    int
	maxCommits   int
	maxApprovals int
	titleWidth   int
}

// computeLayout computes column widths for table alignment across all MRs.
func (m MRListModel) computeLayout() tableLayout {
	var l tableLayout
	for _, mr := range m.eligible {
		l.maxIID = max(l.maxIID, ansi.StringWidth(fmt.Sprintf("!%d", mr.IID)))
		l.maxAuthor = max(l.maxAuthor, ansi.StringWidth("@"+mr.Author))
		l.maxCommits = max(l.maxCommits, ansi.StringWidth(fmt.Sprintf("%d commits", mr.CommitCount)))
		if mr.ApprovalCount > 0 {
			l.maxApprovals = max(l.maxApprovals, ansi.StringWidth(fmt.Sprintf("✓ %d", mr.ApprovalCount)))
		}
	}
	for _, imr := range m.ineligible {
		mr := imr.MR
		l.maxIID = max(l.maxIID, ansi.StringWidth(fmt.Sprintf("!%d", mr.IID)))
		l.maxAuthor = max(l.maxAuthor, ansi.StringWidth("@"+mr.Author))
		l.maxCommits = max(l.maxCommits, ansi.StringWidth(fmt.Sprintf("%d commits", mr.CommitCount)))
		if mr.ApprovalCount > 0 {
			l.maxApprovals = max(l.maxApprovals, ansi.StringWidth(fmt.Sprintf("✓ %d", mr.ApprovalCount)))
		}
	}

	const prefixWidth = 4 // "> ● " or "  ○ " or "  ✗ "
	const colGap = 2
	fixed := prefixWidth + l.maxIID + colGap + colGap + l.maxAuthor + colGap + l.maxCommits
	if l.maxApprovals > 0 {
		fixed += colGap + l.maxApprovals
	}

	if m.width > 0 {
		l.titleWidth = m.width - fixed
	} else {
		l.titleWidth = 200
	}
	if l.titleWidth < 10 {
		l.titleWidth = 10
	}
	return l
}

func padLeft(s string, width int) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

func padRight(s string, width int) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// wrapTitle splits a title at a word boundary if it exceeds the given width.
func wrapTitle(title string, width int) (string, string) {
	if width <= 0 || ansi.StringWidth(title) <= width {
		return title, ""
	}
	lastSpace := -1
	w := 0
	for i, r := range title {
		rw := ansi.StringWidth(string(r))
		if w+rw > width {
			break
		}
		if r == ' ' {
			lastSpace = i
		}
		w += rw
	}
	if lastSpace > 0 {
		return title[:lastSpace], title[lastSpace+1:]
	}
	w = 0
	for i, r := range title {
		rw := ansi.StringWidth(string(r))
		if w+rw > width {
			return title[:i], title[i:]
		}
		w += rw
	}
	return title, ""
}

// truncateText truncates a string to fit within width, appending "…" if needed.
func truncateText(s string, width int) string {
	if width <= 1 || ansi.StringWidth(s) <= width {
		return s
	}
	w := 0
	for i, r := range s {
		rw := ansi.StringWidth(string(r))
		if w+rw > width-1 {
			return s[:i] + "…"
		}
		w += rw
	}
	return s
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
	if m.userRefresh {
		b.WriteString(sRunning.Styled(spinnerFrames[m.spinnerFrame] + " Refreshing..."))
	} else {
		b.WriteString(sFaint.Styled("select and reorder to set merge sequence"))
	}
	b.WriteString("  ")
	b.WriteString(sSuccess.Styled(fmt.Sprintf("%d selected", m.SelectedCount())))
	b.WriteString(sFaint.Styled(fmt.Sprintf(" / %d eligible", len(m.eligible))))
	b.WriteString("\n\n")

	// Compute table layout.
	lay := m.computeLayout()

	// Build display items with line counts for scroll accounting.
	type displayItem struct {
		text  string
		lines int
	}
	var items []displayItem

	for i, mr := range m.eligible {
		var lb strings.Builder
		if i == m.cursor {
			lb.WriteString(sCursor.Styled(">"))
		} else {
			lb.WriteString(" ")
		}
		lb.WriteString(" ")
		if m.selected[mr.IID] {
			lb.WriteString(sSelected.Styled("\u25cf"))
		} else {
			lb.WriteString(sFaint.Styled("\u25cb"))
		}
		lb.WriteString(" ")
		lb.WriteString(padLeft(sBold.Styled(fmt.Sprintf("!%d", mr.IID)), lay.maxIID))
		lb.WriteString("  ")

		first, second := wrapTitle(mr.Title, lay.titleWidth)
		lb.WriteString(padRight(first, lay.titleWidth))
		lb.WriteString("  ")

		lb.WriteString(padLeft(sFaint.Styled("@"+mr.Author), lay.maxAuthor))
		lb.WriteString("  ")
		lb.WriteString(padLeft(sFaint.Styled(fmt.Sprintf("%d commits", mr.CommitCount)), lay.maxCommits))

		if lay.maxApprovals > 0 {
			lb.WriteString("  ")
			if mr.ApprovalCount > 0 {
				lb.WriteString(padLeft(sSuccess.Styled(fmt.Sprintf("✓ %d", mr.ApprovalCount)), lay.maxApprovals))
			} else {
				lb.WriteString(strings.Repeat(" ", lay.maxApprovals))
			}
		}
		lineCount := 1
		if second != "" {
			second = truncateText(second, lay.titleWidth)
			lb.WriteString("\n")
			lb.WriteString(strings.Repeat(" ", 4+lay.maxIID+2))
			lb.WriteString(padRight(second, lay.titleWidth))
			lineCount = 2
		}
		items = append(items, displayItem{text: lb.String(), lines: lineCount})
	}

	// Separator between eligible and ineligible.
	if len(m.ineligible) > 0 && len(m.eligible) > 0 {
		items = append(items, displayItem{text: "", lines: 1})
	}

	for i, imr := range m.ineligible {
		var lb strings.Builder
		idx := len(m.eligible) + i
		if idx == m.cursor {
			lb.WriteString(sCursor.Styled(">"))
		} else {
			lb.WriteString(" ")
		}
		lb.WriteString(" ")
		lb.WriteString(ineligibleIcon(imr.Reason, m.spinnerFrame))
		lb.WriteString(" ")
		lb.WriteString(padLeft(sDim.Styled(fmt.Sprintf("!%d", imr.MR.IID)), lay.maxIID))
		lb.WriteString("  ")

		first, second := wrapTitle(imr.MR.Title, lay.titleWidth)
		lb.WriteString(padRight(sDim.Styled(first), lay.titleWidth))
		lb.WriteString("  ")

		lb.WriteString(padLeft(sDim.Styled("@"+imr.MR.Author), lay.maxAuthor))
		lb.WriteString("  ")
		lb.WriteString(padLeft(sDim.Styled(fmt.Sprintf("%d commits", imr.MR.CommitCount)), lay.maxCommits))

		if lay.maxApprovals > 0 {
			lb.WriteString("  ")
			if imr.MR.ApprovalCount > 0 {
				lb.WriteString(padLeft(sDim.Styled(fmt.Sprintf("✓ %d", imr.MR.ApprovalCount)), lay.maxApprovals))
			} else {
				lb.WriteString(strings.Repeat(" ", lay.maxApprovals))
			}
		}
		lineCount := 1
		if second != "" {
			second = truncateText(second, lay.titleWidth)
			lb.WriteString("\n")
			lb.WriteString(strings.Repeat(" ", 4+lay.maxIID+2))
			lb.WriteString(padRight(sDim.Styled(second), lay.titleWidth))
			lineCount = 2
		}
		items = append(items, displayItem{text: lb.String(), lines: lineCount})
	}

	// Render visible window (line-based).
	available := m.visibleItems()
	linesRendered := 0
	for i := m.scrollOffset; i < len(items) && linesRendered < available; i++ {
		b.WriteString(items[i].text)
		b.WriteString("\n")
		linesRendered += items[i].lines
	}

	return tea.NewView(b.String())
}

// KeyHints returns the keyboard hints for the current state.
func (m MRListModel) KeyHints() []KeyHint {
	if m.loading || m.totalCount() == 0 {
		return []KeyHint{
			{"[o]", "open"},
			{"[R]", "refresh"},
			{"[r]", "change repo"},
			{"[Esc]", "quit"},
		}
	}
	return []KeyHint{
		{"[Space]", "toggle"},
		{"[a]", "all"},
		{"[Shift+↑↓]", "reorder"},
		{"[o]", "open"},
		{"[R]", "refresh"},
		{"[r]", "change repo"},
		{"[Enter]", "start"},
		{"[Esc]", "quit"},
	}
}

const mrListHeaderLines = 4 // repo, blank, section header, blank

// visibleItems returns the number of lines that fit in the content area.
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

// adjustScroll adjusts scrollOffset to keep the cursor visible,
// accounting for multi-line items (wrapped titles).
func (m *MRListModel) adjustScroll() {
	if m.totalCount() == 0 {
		return
	}

	available := m.visibleItems()
	lay := m.computeLayout()
	hasSep := len(m.ineligible) > 0 && len(m.eligible) > 0

	// Build line counts per display item (same order as View).
	var itemLines []int
	for _, mr := range m.eligible {
		_, second := wrapTitle(mr.Title, lay.titleWidth)
		if second != "" {
			itemLines = append(itemLines, 2)
		} else {
			itemLines = append(itemLines, 1)
		}
	}
	if hasSep {
		itemLines = append(itemLines, 1)
	}
	for _, imr := range m.ineligible {
		_, second := wrapTitle(imr.MR.Title, lay.titleWidth)
		if second != "" {
			itemLines = append(itemLines, 2)
		} else {
			itemLines = append(itemLines, 1)
		}
	}

	// Map cursor (MR index) to display items index.
	cursorIdx := m.cursor
	if hasSep && m.cursor >= len(m.eligible) {
		cursorIdx = m.cursor + 1
	}

	if cursorIdx < m.scrollOffset {
		m.scrollOffset = cursorIdx
	}

	// Count lines from scrollOffset to cursorIdx (inclusive).
	linesUsed := 0
	for i := m.scrollOffset; i <= cursorIdx && i < len(itemLines); i++ {
		linesUsed += itemLines[i]
	}

	// Advance scrollOffset until cursor fits within available lines.
	for linesUsed > available && m.scrollOffset < cursorIdx {
		linesUsed -= itemLines[m.scrollOffset]
		m.scrollOffset++
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

// Refreshing returns whether background refresh is active.
func (m MRListModel) Refreshing() bool {
	return m.refreshing
}

// currentMRURL returns the WebURL of the MR under the cursor.
func (m MRListModel) currentMRURL() string {
	if m.cursor < len(m.eligible) {
		return m.eligible[m.cursor].WebURL
	}
	idx := m.cursor - len(m.eligible)
	if idx < len(m.ineligible) {
		return m.ineligible[idx].MR.WebURL
	}
	return ""
}
