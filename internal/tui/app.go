package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/emdash-ai/glmt/internal/auth"
	"github.com/emdash-ai/glmt/internal/config"
	"github.com/emdash-ai/glmt/internal/gitlab"
	"github.com/emdash-ai/glmt/internal/train"
)

// Screen represents the current active screen.
type Screen int

const (
	ScreenSetup      Screen = iota
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
			c, err := gitlab.NewAPIClient(fmt.Sprintf("%s://%s", "https", host), token)
			if err != nil {
				return "", err
			}
			user, err := c.GetCurrentUser(context.Background())
			if err != nil {
				return "", err
			}
			m.client = c
			return user.Name, nil
		}
		m.setup = sm
	} else {
		c, err := gitlab.NewAPIClient(fmt.Sprintf("%s://%s", creds.Protocol, creds.Host), creds.Token)
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
		return m.fetchProjects()
	case ScreenMRList:
		return m.fetchMRs()
	}
	return nil
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m AppModel) View() tea.View {
	switch m.screen {
	case ScreenSetup:
		return m.setup.View()
	case ScreenRepoPicker:
		return m.repoPicker.View()
	case ScreenMRList:
		return m.mrList.View()
	case ScreenTrainRun:
		return m.trainRun.View()
	}
	return tea.NewView("")
}

func (m AppModel) updateSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	newSetup, cmd := m.setup.Update(msg)
	m.setup = newSetup.(SetupModel)

	if m.setup.State() == SetupStateSuccess {
		// Credentials validated, move to repo picker
		m.cfg.GitLab.Host = m.setup.Host()
		_ = config.Save(m.cfgPath, m.cfg)

		m.screen = ScreenRepoPicker
		m.repoPicker = NewRepoPickerModel(detectRepoFromGit())
		return m, m.fetchProjects()
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
		return m, m.fetchMRs()
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
		return m, m.fetchProjects()
	case refetchMRsMsg:
		_ = msg
		return m, m.fetchMRs()
	case startTrainMsg:
		m.screen = ScreenTrainRun
		m.trainRun = NewTrainRunModel(msg.mrs)
		return m, m.startTrain(msg.mrs)
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
	projectID := m.projectID
	return func() tea.Msg {
		mrs, err := client.ListMergeRequests(context.Background(), projectID)
		if err != nil {
			return mrsLoadedMsg{mrs: nil}
		}
		// Enrich each MR with pipeline status via GetMergeRequest
		enriched := make([]*gitlab.MergeRequest, len(mrs))
		for i, mr := range mrs {
			full, err := client.GetMergeRequest(context.Background(), projectID, mr.IID)
			if err != nil {
				enriched[i] = mr
			} else {
				enriched[i] = full
			}
		}
		return mrsLoadedMsg{mrs: enriched}
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
