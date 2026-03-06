package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/emdash-ai/glmt/internal/gitlab"
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
	projects   []*gitlab.Project
	filtered   []*gitlab.Project
	cursor     int
	search     string
	selected   *gitlab.Project
	autoDetect string // pre-detected project path
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

		case "down", "j":
			if len(m.filtered) > 0 && m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
			return m, nil

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
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

	b.WriteString("Select a repository\n\n")

	if m.search != "" {
		b.WriteString("Search: " + m.search + "\n\n")
	} else {
		b.WriteString("Search: (type to filter)\n\n")
	}

	if len(m.projects) == 0 {
		b.WriteString("Loading projects...\n")
	} else if len(m.filtered) == 0 {
		b.WriteString("No matching projects.\n")
	} else {
		for i, p := range m.filtered {
			cursor := "  "
			if i == m.cursor {
				cursor = "> "
			}
			b.WriteString(cursor + p.PathWithNamespace + "\n")
		}
	}

	b.WriteString("\n[j/k] navigate  [enter] select  [esc] clear search  [type] filter")

	return tea.NewView(b.String())
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
