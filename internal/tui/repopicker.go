package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/gitlab"
)

// projectsLoadedMsg is sent when the project list has been fetched from GitLab.
type projectsLoadedMsg struct {
	projects []*gitlab.Project
}

// repoSelectedMsg is sent when the user selects a project.
type repoSelectedMsg struct {
	project *gitlab.Project
}

// RepoPickerModel is a bubbletea model for the repo picker screen.
type RepoPickerModel struct {
	projects      []*gitlab.Project
	filtered      []*gitlab.Project
	cursor        int
	search        string
	selected      *gitlab.Project
	autoDetect    string // pre-detected project path
	spinnerFrame  int
	contentHeight int
	scrollOffset  int
}

// NewRepoPickerModel creates a new RepoPickerModel with an optional auto-detected project path.
func NewRepoPickerModel(autoDetect string) RepoPickerModel {
	return RepoPickerModel{
		autoDetect: autoDetect,
	}
}

// Init returns the initial command for the model.
func (m RepoPickerModel) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates the model state.
func (m RepoPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		if len(m.projects) == 0 {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, spinnerTick()
		}
		return m, nil
	case projectsLoadedMsg:
		m.projects = msg.projects
		m.filtered = filterProjects(m.projects, m.search)
		m.cursor = 0
		if m.autoDetect != "" {
			for i, p := range m.filtered {
				if p.PathWithNamespace == m.autoDetect {
					m.cursor = i
					break
				}
			}
		}
		return m, nil

	case tea.PasteMsg:
		m.search += msg.Content
		m.filtered = filterProjects(m.projects, m.search)
		if m.cursor >= len(m.filtered) && len(m.filtered) > 0 {
			m.cursor = len(m.filtered) - 1
		} else if len(m.filtered) == 0 {
			m.cursor = 0
		}
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "down":
			if len(m.filtered) > 0 && m.cursor < len(m.filtered)-1 {
				m.cursor++
				m.adjustScroll()
			}
			return m, nil

		case "up":
			if m.cursor > 0 {
				m.cursor--
				m.adjustScroll()
			}
			return m, nil

		case "enter":
			if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
				m.selected = m.filtered[m.cursor]
				return m, func() tea.Msg {
					return repoSelectedMsg{project: m.selected}
				}
			}
			return m, nil

		case "esc":
			if m.search == "" {
				return m, tea.Quit
			}
			m.search = ""
			m.filtered = filterProjects(m.projects, m.search)
			m.cursor = 0
			return m, nil

		case "backspace":
			if len(m.search) > 0 {
				m.search = m.search[:len(m.search)-1]
				m.filtered = filterProjects(m.projects, m.search)
				if m.cursor >= len(m.filtered) && len(m.filtered) > 0 {
					m.cursor = len(m.filtered) - 1
				} else if len(m.filtered) == 0 {
					m.cursor = 0
				}
			}
			return m, nil

		default:
			text := msg.Key().Text
			if text != "" {
				m.search += text
				m.filtered = filterProjects(m.projects, m.search)
				if m.cursor >= len(m.filtered) && len(m.filtered) > 0 {
					m.cursor = len(m.filtered) - 1
				} else if len(m.filtered) == 0 {
					m.cursor = 0
				}
			}
			return m, nil
		}
	}

	return m, nil
}

// View renders the repo picker screen.
func (m RepoPickerModel) View() tea.View {
	var b strings.Builder

	b.WriteString("  ")
	b.WriteString(sBold.Styled("Select repository"))
	b.WriteString("\n\n")

	if m.search != "" {
		b.WriteString("  " + sBold.Styled("Search:") + " " + m.search)
	} else {
		b.WriteString("  " + sBold.Styled("Search:") + " " + sFaint.Styled("(type to filter)"))
	}
	b.WriteString("\n\n")

	switch {
	case len(m.projects) == 0:
		b.WriteString("  ")
		b.WriteString(sRunning.Styled(spinnerFrames[m.spinnerFrame] + " Loading projects..."))
		b.WriteString("\n")
	case len(m.filtered) == 0:
		b.WriteString("  ")
		b.WriteString(sFaint.Styled("No matching projects."))
		b.WriteString("\n")
	default:
		visible := m.visibleItems()
		end := m.scrollOffset + visible
		end = min(end, len(m.filtered))
		for i := m.scrollOffset; i < end; i++ {
			p := m.filtered[i]
			if i == m.cursor {
				b.WriteString("  ")
				b.WriteString(sCursor.Styled("> "))
				b.WriteString(sSelected.Styled(p.PathWithNamespace))
			} else {
				b.WriteString("    ")
				b.WriteString(p.PathWithNamespace)
			}
			b.WriteString("\n")
		}
	}

	view := tea.NewView(b.String())
	// Cursor after "  Search: " (col 10) + search length
	searchCol := 10 + len(m.search)
	view.Cursor = tea.NewCursor(searchCol, 2)
	return view
}

// Cursor returns the current cursor position.
func (m RepoPickerModel) Cursor() int {
	return m.cursor
}

// Search returns the current search string.
func (m RepoPickerModel) Search() string {
	return m.search
}

// Selected returns the currently selected project, or nil if none.
func (m RepoPickerModel) Selected() *gitlab.Project {
	return m.selected
}

// Filtered returns the currently filtered project list.
func (m RepoPickerModel) Filtered() []*gitlab.Project {
	return m.filtered
}

// KeyHints returns the keyboard hints for the repo picker screen.
func (m RepoPickerModel) KeyHints() []KeyHint {
	return []KeyHint{
		{"[↑/↓]", "navigate"},
		{"[Enter]", "select"},
		{"[Esc]", "clear/quit"},
		{"[type]", "filter"},
	}
}

const repoPickerHeaderLines = 4 // title, blank, search, blank

// visibleItems returns the number of items that fit in the content area.
func (m RepoPickerModel) visibleItems() int {
	if m.contentHeight <= repoPickerHeaderLines {
		return len(m.filtered) // no constraint
	}
	return max(m.contentHeight-repoPickerHeaderLines, 1)
}

// adjustScroll adjusts scrollOffset to keep the cursor visible.
func (m *RepoPickerModel) adjustScroll() {
	visible := m.visibleItems()
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}
	if m.cursor >= m.scrollOffset+visible {
		m.scrollOffset = m.cursor - visible + 1
	}
}

// filterProjects filters projects by case-insensitive substring match on PathWithNamespace.
func filterProjects(projects []*gitlab.Project, search string) []*gitlab.Project {
	if search == "" {
		result := make([]*gitlab.Project, len(projects))
		copy(result, projects)
		return result
	}

	lower := strings.ToLower(search)
	var result []*gitlab.Project
	for _, p := range projects {
		if strings.Contains(strings.ToLower(p.PathWithNamespace), lower) {
			result = append(result, p)
		}
	}
	return result
}
