package tui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/auth"
	"github.com/sairus2k/glmt/internal/config"
	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/sairus2k/glmt/internal/train"
)

// Screen represents the current active screen.
type Screen int

const (
	ScreenSetup Screen = iota
	ScreenRepoPicker
	ScreenMRList
	ScreenTrainRun
)

// AppModel is the top-level bubbletea model that coordinates screen transitions.
type AppModel struct {
	screen Screen

	setup      SetupModel
	repoPicker RepoPickerModel
	mrList     MRListModel
	trainRun   TrainRunModel

	client    gitlab.Client
	cfg       *config.Config
	cfgPath   string
	projectID int

	trainCancel context.CancelFunc
}

// NewAppModel creates the app model, deciding which screen to start on.
func NewAppModel(creds *auth.Credentials, cfg *config.Config, cfgPath string) AppModel {
	m := AppModel{
		cfg:     cfg,
		cfgPath: cfgPath,
	}

	if creds == nil {
		m.screen = ScreenSetup
		sm := NewSetupModel()
		sm.ValidateFn = func(host, token string) (string, error) {
			c, err := gitlab.NewAPIClient(host, token)
			if err != nil {
				return "", err
			}
			user, err := c.GetCurrentUser(context.Background())
			if err != nil {
				return "", err
			}
			return user.Name, nil
		}
		m.setup = sm
	} else {
		c, err := gitlab.NewAPIClient(creds.Host, creds.Token)
		if err == nil {
			m.client = c
		}
		m.cfg.GitLab.Host = creds.Host

		if cfg.Defaults.ProjectID != 0 {
			m.screen = ScreenMRList
			m.projectID = cfg.Defaults.ProjectID
			m.mrList = NewMRListModel(cfg.Defaults.Repo)
		} else {
			m.screen = ScreenRepoPicker
			m.repoPicker = NewRepoPickerModel(detectRepoFromGit())
		}
	}

	return m
}

func (m AppModel) Init() tea.Cmd {
	switch m.screen {
	case ScreenSetup:
		return m.setup.Init()
	case ScreenRepoPicker:
		return tea.Batch(m.fetchProjects(), spinnerTick())
	case ScreenMRList:
		return tea.Batch(m.fetchMRs(), spinnerTick())
	}
	return nil
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case spinnerTickMsg:
		return m.propagateSpinnerTick(msg)
	}

	switch m.screen {
	case ScreenSetup:
		return m.updateSetup(msg)
	case ScreenRepoPicker:
		return m.updateRepoPicker(msg)
	case ScreenMRList:
		return m.updateMRList(msg)
	case ScreenTrainRun:
		return m.updateTrainRun(msg)
	}
	return m, nil
}

func (m AppModel) propagateSpinnerTick(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case ScreenSetup:
		newSetup, cmd := m.setup.Update(msg)
		m.setup = newSetup.(SetupModel)
		return m, cmd
	case ScreenRepoPicker:
		newPicker, cmd := m.repoPicker.Update(msg)
		m.repoPicker = newPicker.(RepoPickerModel)
		return m, cmd
	case ScreenMRList:
		newList, cmd := m.mrList.Update(msg)
		m.mrList = newList.(MRListModel)
		return m, cmd
	case ScreenTrainRun:
		newRun, cmd := m.trainRun.Update(msg)
		m.trainRun = newRun.(TrainRunModel)
		return m, cmd
	}
	return m, nil
}

func (m AppModel) View() tea.View {
	var view tea.View
	switch m.screen {
	case ScreenSetup:
		view = m.setup.View()
	case ScreenRepoPicker:
		view = m.repoPicker.View()
	case ScreenMRList:
		view = m.mrList.View()
	case ScreenTrainRun:
		view = m.trainRun.View()
	default:
		view = tea.NewView("")
	}

	// Prepend common header
	header := "\n  " + sHeader.Styled("glmt") + "\n\n"
	view.Content = header + view.Content

	// Adjust cursor for the 3 prepended lines
	if view.Cursor != nil {
		view.Cursor.Y += 3
	}

	view.AltScreen = true
	view.WindowTitle = "glmt"
	return view
}

func (m AppModel) updateSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	newSetup, cmd := m.setup.Update(msg)
	m.setup = newSetup.(SetupModel)

	if m.setup.State() == SetupStateSuccess {
		// Credentials validated — create the client and move to repo picker
		c, err := gitlab.NewAPIClient(m.setup.Host(), m.setup.Token())
		if err != nil {
			return m, nil
		}
		m.client = c
		m.cfg.GitLab.Host = m.setup.Host()
		m.cfg.GitLab.Token = m.setup.Token()
		_ = config.Save(m.cfgPath, m.cfg)

		m.screen = ScreenRepoPicker
		m.repoPicker = NewRepoPickerModel(detectRepoFromGit())
		return m, tea.Batch(m.fetchProjects(), spinnerTick())
	}

	return m, cmd
}

func (m AppModel) updateRepoPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case projectsLoadedMsg:
		newPicker, cmd := m.repoPicker.Update(msg)
		m.repoPicker = newPicker.(RepoPickerModel)
		return m, cmd
	case repoSelectedMsg:
		m.projectID = msg.project.ID
		m.cfg.Defaults.Repo = msg.project.PathWithNamespace
		m.cfg.Defaults.ProjectID = msg.project.ID
		_ = config.Save(m.cfgPath, m.cfg)

		m.screen = ScreenMRList
		m.mrList = NewMRListModel(msg.project.PathWithNamespace)
		return m, tea.Batch(m.fetchMRs(), spinnerTick())
	}

	newPicker, cmd := m.repoPicker.Update(msg)
	m.repoPicker = newPicker.(RepoPickerModel)
	return m, cmd
}

func (m AppModel) updateMRList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case changeRepoMsg:
		_ = msg
		m.screen = ScreenRepoPicker
		m.repoPicker = NewRepoPickerModel(detectRepoFromGit())
		return m, tea.Batch(m.fetchProjects(), spinnerTick())
	case refetchMRsMsg:
		_ = msg
		m.mrList.loading = true
		return m, tea.Batch(m.fetchMRs(), spinnerTick())
	case startTrainMsg:
		m.screen = ScreenTrainRun
		m.trainRun = NewTrainRunModel(msg.mrs)
		return m, tea.Batch(m.startTrain(msg.mrs), spinnerTick())
	case mrsLoadedMsg:
		newList, cmd := m.mrList.Update(msg)
		m.mrList = newList.(MRListModel)
		return m, cmd
	}

	newList, cmd := m.mrList.Update(msg)
	m.mrList = newList.(MRListModel)
	return m, cmd
}

func (m AppModel) updateTrainRun(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case trainAbortMsg:
		if m.trainCancel != nil {
			m.trainCancel()
		}
		newRun, cmd := m.trainRun.Update(msg)
		m.trainRun = newRun.(TrainRunModel)
		return m, cmd
	case trainDoneMsg:
		newRun, _ := m.trainRun.Update(msg)
		m.trainRun = newRun.(TrainRunModel)
		return m, nil
	}

	newRun, cmd := m.trainRun.Update(msg)
	m.trainRun = newRun.(TrainRunModel)
	return m, cmd
}

// Commands

func (m AppModel) fetchProjects() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		projects, err := client.ListProjects(context.Background(), "")
		if err != nil {
			return projectsLoadedMsg{projects: nil}
		}
		return projectsLoadedMsg{projects: projects}
	}
}

func (m AppModel) fetchMRs() tea.Cmd {
	client := m.client
	repoPath := m.mrList.repoPath
	return func() tea.Msg {
		mrs, err := client.ListMergeRequestsFull(context.Background(), repoPath)
		if err != nil {
			return mrsLoadedMsg{err: err}
		}
		return mrsLoadedMsg{mrs: mrs}
	}
}

func (m *AppModel) startTrain(mrs []*gitlab.MergeRequest) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.trainCancel = cancel
	client := m.client
	projectID := m.projectID

	return func() tea.Msg {
		runner := train.NewRunner(client, projectID, func(mrIID int, step, message string) {
			// Logger callback — in a real impl this would send messages to the TUI
			// via tea.Program.Send, but for now we just log
		})
		runner.PollPipelineInterval = 10 * time.Second
		runner.PollRebaseInterval = 2 * time.Second

		result, _ := runner.Run(ctx, mrs)
		return trainDoneMsg{result: result}
	}
}

func detectRepoFromGit() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	return extractProjectPath(url)
}

func extractProjectPath(url string) string {
	// Handle SSH: git@gitlab.com:team/project.git
	if strings.Contains(url, ":") && strings.HasPrefix(url, "git@") {
		parts := strings.SplitN(url, ":", 2)
		if len(parts) == 2 {
			return strings.TrimSuffix(parts[1], ".git")
		}
	}
	// Handle HTTPS: https://gitlab.com/team/project.git
	url = strings.TrimSuffix(url, ".git")
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return ""
}
