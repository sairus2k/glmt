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

// userLoadedMsg is sent when the current user has been fetched.
type userLoadedMsg struct {
	userName string
}

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

	width       int
	height      int
	userName    string
	runtimeHost string // effective host for display (may differ from cfg)

	trainCancel context.CancelFunc
	trainStepCh chan trainStepMsg
}

// NewAppModel creates the app model, deciding which screen to start on.
// overrideProjectID is a per-invocation override (e.g. from CLI flags) that
// is used without being persisted to cfg.
func NewAppModel(creds *auth.Credentials, cfg *config.Config, cfgPath string, overrideProjectID int) AppModel {
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
		m.runtimeHost = creds.Host

		projectID := cfg.Defaults.ProjectID
		if overrideProjectID != 0 {
			projectID = overrideProjectID
		}
		if projectID != 0 {
			m.screen = ScreenMRList
			m.projectID = projectID
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
		return tea.Batch(m.fetchProjects(), m.fetchCurrentUser(), spinnerTick())
	case ScreenMRList:
		return tea.Batch(m.fetchMRs(), m.fetchCurrentUser(), spinnerTick())
	case ScreenTrainRun:
		return nil
	}
	return nil
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		return m.propagateSpinnerTick(tea.Msg(msg))
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.propagateContentHeight()
		return m, nil
	case userLoadedMsg:
		m.userName = msg.userName
		return m, nil
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

func (m AppModel) contentHeight() int {
	h := m.height - 5 // 3 header + 2 footer
	if h < 1 {
		h = 1
	}
	return h
}

func (m *AppModel) propagateContentHeight() {
	ch := m.contentHeight()
	m.repoPicker.contentHeight = ch
	m.mrList.contentHeight = ch
	m.mrList.width = m.width
}

func (m AppModel) loginStatus() string {
	host := m.runtimeHost
	if host == "" {
		host = m.cfg.GitLab.Host
	}
	if m.userName != "" && host != "" {
		return m.userName + " @ " + host
	}
	if host != "" {
		return host
	}
	return ""
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
	var hints []KeyHint
	switch m.screen {
	case ScreenSetup:
		view = m.setup.View()
		hints = m.setup.KeyHints()
	case ScreenRepoPicker:
		view = m.repoPicker.View()
		hints = m.repoPicker.KeyHints()
	case ScreenMRList:
		view = m.mrList.View()
		hints = m.mrList.KeyHints()
	case ScreenTrainRun:
		view = m.trainRun.View()
		hints = m.trainRun.KeyHints()
	default:
		view = tea.NewView("")
	}

	// Header (3 lines)
	header := "\n" + renderHeader(m.width) + "\n\n"

	// Pad content to fill available space
	contentLines := strings.Count(view.Content, "\n")
	available := m.contentHeight()
	if pad := available - contentLines; pad > 0 {
		view.Content += strings.Repeat("\n", pad)
	}

	// Footer (2 lines)
	footer := "\n" + renderFooter(hints, m.loginStatus(), m.width)

	view.Content = header + view.Content + footer

	// Adjust cursor for the 3 prepended header lines
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
		m.runtimeHost = m.setup.Host()
		m.userName = m.setup.UserName()
		_ = config.Save(m.cfgPath, m.cfg)

		m.screen = ScreenRepoPicker
		m.repoPicker = NewRepoPickerModel(detectRepoFromGit())
		m.propagateContentHeight()
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
		m.propagateContentHeight()
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
		m.propagateContentHeight()
		return m, tea.Batch(m.fetchProjects(), spinnerTick())
	case refetchMRsMsg:
		_ = msg
		m.mrList.refreshing = true
		m.mrList.userRefresh = true
		cmds := []tea.Cmd{m.fetchMRs(), spinnerTick()}
		iids := collectUncheckedIIDs(m.mrList)
		if len(iids) > 0 {
			cmds = append(cmds, m.triggerMergeChecks(iids))
		}
		return m, tea.Batch(cmds...)
	case backgroundRefetchMsg:
		if m.mrList.refreshing {
			return m, m.fetchMRs() // silent refetch, loading stays false
		}
		return m, nil
	case startTrainMsg:
		m.screen = ScreenTrainRun
		m.trainRun = NewTrainRunModel(msg.mrs)
		return m, tea.Batch(m.startTrain(msg.mrs), spinnerTick())
	case mrsLoadedMsg:
		wasLoading := m.mrList.loading
		newList, cmd := m.mrList.Update(msg)
		m.mrList = newList.(MRListModel)
		cmds := []tea.Cmd{cmd}
		if m.mrList.refreshing {
			if wasLoading {
				// First load: trigger REST calls to kick merge status recalculation.
				iids := collectUncheckedIIDs(m.mrList)
				if len(iids) > 0 {
					cmds = append(cmds, m.triggerMergeChecks(iids))
				}
				cmds = append(cmds, spinnerTick())
			}
			// Unchecked statuses resolve quickly (4s); pipelines take minutes (15s).
			if m.mrList.HasUncheckedMRs() {
				cmds = append(cmds, scheduleBackgroundRefetch())
			} else {
				cmds = append(cmds, scheduleBackgroundRefetchAfter(15*time.Second))
			}
		}
		return m, tea.Batch(cmds...)
	}

	newList, cmd := m.mrList.Update(msg)
	m.mrList = newList.(MRListModel)
	return m, cmd
}

// collectUncheckedIIDs returns IIDs of ineligible MRs with "unchecked" status.
func collectUncheckedIIDs(m MRListModel) []int {
	var iids []int
	for _, imr := range m.ineligible {
		if imr.Reason == "unchecked" {
			iids = append(iids, imr.MR.IID)
		}
	}
	return iids
}

// triggerMergeChecks fires GET requests for each IID to kick GitLab's mergeability recalculation.
func (m AppModel) triggerMergeChecks(iids []int) tea.Cmd {
	client := m.client
	projectID := m.projectID
	return func() tea.Msg {
		for _, iid := range iids {
			// Fire-and-forget: ignore errors.
			_, _ = client.GetMergeRequest(context.Background(), projectID, iid)
		}
		return nil
	}
}

// scheduleBackgroundRefetch returns a command that sends backgroundRefetchMsg after a delay.
func scheduleBackgroundRefetch() tea.Cmd {
	return scheduleBackgroundRefetchAfter(4 * time.Second)
}

// scheduleBackgroundRefetchAfter returns a command that sends backgroundRefetchMsg after the given delay.
func scheduleBackgroundRefetchAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return backgroundRefetchMsg{}
	})
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
	case trainStepMsg:
		newRun, cmd := m.trainRun.Update(msg)
		m.trainRun = newRun.(TrainRunModel)
		return m, tea.Batch(cmd, waitForTrainStep(m.trainStepCh))
	case trainDoneMsg:
		newRun, _ := m.trainRun.Update(msg)
		m.trainRun = newRun.(TrainRunModel)
		return m, nil
	case trainBackMsg:
		m.screen = ScreenMRList
		m.mrList.loading = true
		return m, tea.Batch(m.fetchMRs(), spinnerTick())
	}

	newRun, cmd := m.trainRun.Update(msg)
	m.trainRun = newRun.(TrainRunModel)
	return m, cmd
}

// Commands

func (m AppModel) fetchCurrentUser() tea.Cmd {
	client := m.client
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		user, err := client.GetCurrentUser(context.Background())
		if err != nil || user == nil {
			return userLoadedMsg{}
		}
		return userLoadedMsg{userName: user.Name}
	}
}

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

func waitForTrainStep(ch chan trainStepMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *AppModel) startTrain(mrs []*gitlab.MergeRequest) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.trainCancel = cancel

	ch := make(chan trainStepMsg, 32)
	m.trainStepCh = ch

	client := m.client
	projectID := m.projectID

	runCmd := func() tea.Msg {
		runner := train.NewRunner(client, projectID, func(mrIID int, step, message string) {
			ch <- trainStepMsg{mrIID: mrIID, step: step, message: message}
		})
		runner.PollPipelineInterval = 10 * time.Second
		runner.PollRebaseInterval = 2 * time.Second

		result, _ := runner.Run(ctx, mrs)
		close(ch)
		return trainDoneMsg{result: result}
	}

	return tea.Batch(runCmd, waitForTrainStep(ch))
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
