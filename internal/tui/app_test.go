package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/auth"
	"github.com/sairus2k/glmt/internal/config"
	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/sairus2k/glmt/internal/train"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractProjectPath_SSH(t *testing.T) {
	got := extractProjectPath("git@gitlab.com:team/project.git")
	assert.Equal(t, "team/project", got)
}

func TestExtractProjectPath_HTTPS(t *testing.T) {
	got := extractProjectPath("https://gitlab.com/team/project.git")
	assert.Equal(t, "team/project", got)
}

func TestExtractProjectPath_HTTPSNoGit(t *testing.T) {
	got := extractProjectPath("https://gitlab.com/team/project")
	assert.Equal(t, "team/project", got)
}

func TestExtractProjectPath_Empty(t *testing.T) {
	got := extractProjectPath("")
	assert.Empty(t, got)
}

func TestApp_StartsAtSetupWhenNoCredentials(t *testing.T) {
	m := NewAppModel(nil, config.DefaultConfig(), "/tmp/test-config.toml", 0, "test")
	assert.Equal(t, ScreenSetup, m.screen)
}

func TestApp_StartsAtMRListWhenProjectConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.ProjectID = 42
	cfg.Defaults.Repo = "team/project"
	creds := &auth.Credentials{Host: "gitlab.example.com", Token: "test-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml", 0, "test")
	assert.Equal(t, ScreenMRList, m.screen)
}

func TestApp_StartsAtRepoPickerWhenNoProject(t *testing.T) {
	cfg := config.DefaultConfig()
	creds := &auth.Credentials{Host: "gitlab.example.com", Token: "test-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml", 0, "test")
	assert.Equal(t, ScreenRepoPicker, m.screen)
}

func TestApp_SetupSuccessTransitionsToRepoPicker(t *testing.T) {
	cfg := config.DefaultConfig()
	cfgPath := t.TempDir() + "/config.toml"
	m := NewAppModel(nil, cfg, cfgPath, 0, "test")
	require.Equal(t, ScreenSetup, m.screen)

	// Type host and token to populate the setup model
	var model tea.Model = m
	model = typeString(t, model, "gitlab.example.com")
	model, _ = model.Update(specialKeyPress(tea.KeyEnter))
	model = typeString(t, model, "glpat-test-token")

	// Simulate credential validation success
	model, cmd := model.Update(credentialsValidMsg{userName: "Test User"})
	app := model.(AppModel)

	assert.Equal(t, ScreenRepoPicker, app.screen)
	assert.NotNil(t, app.client, "client must be set after setup success")
	assert.Equal(t, "gitlab.example.com", app.runtimeHost)
	require.NotNil(t, cmd, "should return fetchProjects command")
}

func TestApp_CLIOverrideProjectID_DoesNotAffectConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Defaults.ProjectID = 0
	creds := &auth.Credentials{Host: "gitlab.example.com", Token: "test-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml", 99, "test")

	// The override should be used for the current session
	assert.Equal(t, 99, m.projectID)
	assert.Equal(t, ScreenMRList, m.screen)
	// But the config should not be mutated
	assert.Equal(t, 0, cfg.Defaults.ProjectID)
}

func TestApp_CLICredentials_DoNotPersistToConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.GitLab.Host = "saved-host.example.com"
	cfg.GitLab.Token = "saved-token"
	creds := &auth.Credentials{Host: "cli-host.example.com", Token: "cli-token", Protocol: "https"}

	m := NewAppModel(creds, cfg, "/tmp/test-config.toml", 0, "test")

	// runtimeHost should reflect the CLI-provided creds
	assert.Equal(t, "cli-host.example.com", m.runtimeHost)
	// But the config should retain its original values
	assert.Equal(t, "saved-host.example.com", cfg.GitLab.Host)
	assert.Equal(t, "saved-token", cfg.GitLab.Token)
}

// ---------------------------------------------------------------------------
// Group 1: Pure Functions
// ---------------------------------------------------------------------------

func TestCountTrainResults(t *testing.T) {
	tests := []struct {
		name        string
		result      *train.Result
		wantMerged  int
		wantSkipped int
		wantPending int
	}{
		{"nil result", nil, 0, 0, 0},
		{"all merged", &train.Result{MRResults: []train.MRResult{
			{Status: train.MRStatusMerged},
			{Status: train.MRStatusMerged},
			{Status: train.MRStatusMerged},
		}}, 3, 0, 0},
		{"all skipped", &train.Result{MRResults: []train.MRResult{
			{Status: train.MRStatusSkipped},
			{Status: train.MRStatusSkipped},
		}}, 0, 2, 0},
		{"mixed", &train.Result{MRResults: []train.MRResult{
			{Status: train.MRStatusMerged},
			{Status: train.MRStatusSkipped},
			{Status: train.MRStatusPending},
		}}, 1, 1, 1},
		{"empty MRResults", &train.Result{MRResults: []train.MRResult{}}, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, skipped, pending := countTrainResults(tt.result)
			assert.Equal(t, tt.wantMerged, merged)
			assert.Equal(t, tt.wantSkipped, skipped)
			assert.Equal(t, tt.wantPending, pending)
		})
	}
}

func TestLoginStatus(t *testing.T) {
	tests := []struct {
		name        string
		runtimeHost string
		cfgHost     string
		userName    string
		want        string
	}{
		{"runtimeHost + userName", "gitlab.io", "", "alice", "alice @ gitlab.io"},
		{"cfg host + userName", "", "gitlab.com", "bob", "bob @ gitlab.com"},
		{"runtimeHost only", "gitlab.io", "", "", "gitlab.io"},
		{"cfg host only", "", "gitlab.com", "", "gitlab.com"},
		{"all empty", "", "", "", ""},
		{"runtimeHost takes precedence", "runtime.io", "config.com", "carol", "carol @ runtime.io"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := AppModel{
				runtimeHost: tt.runtimeHost,
				cfg:         &config.Config{GitLab: config.GitLabConfig{Host: tt.cfgHost}},
				userName:    tt.userName,
			}
			assert.Equal(t, tt.want, m.loginStatus())
		})
	}
}

func TestCollectUncheckedIIDs(t *testing.T) {
	tests := []struct {
		name string
		mrs  []*gitlab.MergeRequest
		want []int
	}{
		{"no ineligible", []*gitlab.MergeRequest{eligibleMR1}, nil},
		{"ineligible but no unchecked reason", []*gitlab.MergeRequest{draftMR, conflictMR}, nil},
		{"mix of unchecked and other reasons", []*gitlab.MergeRequest{eligibleMR1, uncheckedMR, draftMR}, []int{60}},
		{"checking reason is NOT collected", []*gitlab.MergeRequest{checkingMR}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := loadModel(tt.mrs)
			got := collectUncheckedIIDs(m)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestContentHeight(t *testing.T) {
	tests := []struct {
		height int
		want   int
	}{
		{30, 25},
		{5, 1},
		{0, 1},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			m := AppModel{height: tt.height}
			assert.Equal(t, tt.want, m.contentHeight())
		})
	}
}

// ---------------------------------------------------------------------------
// Group 2: Global Update Dispatch
// ---------------------------------------------------------------------------

func TestApp_WindowSizeMsg(t *testing.T) {
	m := AppModel{
		screen: ScreenMRList,
		cfg:    config.DefaultConfig(),
		mrList: NewMRListModel("team/project"),
	}

	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := updated.(AppModel)

	assert.Nil(t, cmd)
	assert.Equal(t, 120, app.width)
	assert.Equal(t, 40, app.height)
	assert.Equal(t, 35, app.contentHeight())
	assert.Equal(t, 35, app.mrList.contentHeight)
	assert.Equal(t, 120, app.mrList.width)
}

func TestApp_UserLoadedMsg(t *testing.T) {
	m := AppModel{screen: ScreenMRList, cfg: config.DefaultConfig()}

	updated, cmd := m.Update(userLoadedMsg{userName: "Ada"})
	app := updated.(AppModel)

	assert.Nil(t, cmd)
	assert.Equal(t, "Ada", app.userName)
}

// ---------------------------------------------------------------------------
// Group 3: Screen Transitions
// ---------------------------------------------------------------------------

func TestApp_RepoSelected_TransitionsToMRList(t *testing.T) {
	cfgPath := t.TempDir() + "/config.toml"
	m := AppModel{
		screen:  ScreenRepoPicker,
		client:  &train.MockClient{},
		cfg:     config.DefaultConfig(),
		cfgPath: cfgPath,
	}

	updated, cmd := m.Update(repoSelectedMsg{project: &gitlab.Project{ID: 7, PathWithNamespace: "team/alpha"}})
	app := updated.(AppModel)

	assert.Equal(t, ScreenMRList, app.screen)
	assert.Equal(t, 7, app.projectID)
	assert.Equal(t, "team/alpha", app.mrList.repoPath)
	assert.NotNil(t, cmd)

	// Verify config was persisted.
	loaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "team/alpha", loaded.Defaults.Repo)
	assert.Equal(t, 7, loaded.Defaults.ProjectID)
}

func TestApp_ProjectsLoaded_StaysOnRepoPicker(t *testing.T) {
	m := AppModel{
		screen:     ScreenRepoPicker,
		cfg:        config.DefaultConfig(),
		repoPicker: NewRepoPickerModel(""),
	}

	updated, _ := m.Update(projectsLoadedMsg{projects: testProjects})
	app := updated.(AppModel)

	assert.Equal(t, ScreenRepoPicker, app.screen)
}

func TestApp_ChangeRepo_TransitionsToRepoPicker(t *testing.T) {
	m := AppModel{
		screen: ScreenMRList,
		client: &train.MockClient{},
		cfg:    config.DefaultConfig(),
		mrList: NewMRListModel("team/project"),
	}

	updated, cmd := m.Update(changeRepoMsg{})
	app := updated.(AppModel)

	assert.Equal(t, ScreenRepoPicker, app.screen)
	assert.NotNil(t, cmd)
}

func TestApp_StartTrain_TransitionsToTrainRun(t *testing.T) {
	m := AppModel{
		screen:    ScreenMRList,
		client:    &train.MockClient{},
		cfg:       config.DefaultConfig(),
		projectID: 42,
		mrList:    NewMRListModel("team/project"),
	}

	mrs := []*gitlab.MergeRequest{eligibleMR1, eligibleMR2}
	updated, cmd := m.Update(startTrainMsg{mrs: mrs})
	app := updated.(AppModel)

	assert.Equal(t, ScreenTrainRun, app.screen)
	assert.Len(t, app.trainRun.mrs, 2)
	assert.NotNil(t, cmd)
}

func TestApp_RefetchMRs(t *testing.T) {
	mrList := NewMRListModel("team/project")
	updated, _ := mrList.Update(mrsLoadedMsg{mrs: []*gitlab.MergeRequest{eligibleMR1, uncheckedMR}})
	mrList = updated.(MRListModel)

	m := AppModel{
		screen:    ScreenMRList,
		client:    &train.MockClient{},
		cfg:       config.DefaultConfig(),
		projectID: 42,
		mrList:    mrList,
	}

	result, cmd := m.Update(refetchMRsMsg{})
	app := result.(AppModel)

	assert.True(t, app.mrList.refreshing)
	assert.True(t, app.mrList.userRefresh)
	assert.NotNil(t, cmd)
}

func TestApp_BackgroundRefetch_WhenRefreshing(t *testing.T) {
	mrList := NewMRListModel("team/project")
	mrList.refreshing = true

	m := AppModel{
		screen:    ScreenMRList,
		client:    &train.MockClient{},
		cfg:       config.DefaultConfig(),
		projectID: 42,
		mrList:    mrList,
	}

	_, cmd := m.Update(backgroundRefetchMsg{})
	assert.NotNil(t, cmd)
}

func TestApp_BackgroundRefetch_WhenNotRefreshing_Noop(t *testing.T) {
	mrList := NewMRListModel("team/project")
	mrList.refreshing = false

	m := AppModel{
		screen: ScreenMRList,
		cfg:    config.DefaultConfig(),
		mrList: mrList,
	}

	_, cmd := m.Update(backgroundRefetchMsg{})
	assert.Nil(t, cmd)
}

func TestApp_TrainDone(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1}
	m := AppModel{
		screen:   ScreenTrainRun,
		cfg:      config.DefaultConfig(),
		trainRun: NewTrainRunModel(mrs),
	}

	result := &train.Result{
		MRResults:          []train.MRResult{{Status: train.MRStatusMerged}},
		MainPipelineStatus: "success",
	}
	updated, cmd := m.Update(trainDoneMsg{result: result})
	app := updated.(AppModel)

	assert.Equal(t, ScreenTrainRun, app.screen)
	assert.True(t, app.trainRun.Done())
	assert.NotNil(t, app.trainRun.Result())
	assert.Nil(t, cmd)
}

func TestApp_TrainBack_TransitionsToMRList(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1}
	m := AppModel{
		screen:    ScreenTrainRun,
		client:    &train.MockClient{},
		cfg:       config.DefaultConfig(),
		projectID: 42,
		trainRun:  NewTrainRunModel(mrs),
		mrList:    NewMRListModel("team/project"),
	}

	updated, cmd := m.Update(trainBackMsg{})
	app := updated.(AppModel)

	assert.Equal(t, ScreenMRList, app.screen)
	assert.True(t, app.mrList.loading)
	assert.NotNil(t, cmd)
}

func TestApp_TrainAbort_CallsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mrs := []*gitlab.MergeRequest{eligibleMR1}
	m := AppModel{
		screen:      ScreenTrainRun,
		cfg:         config.DefaultConfig(),
		trainRun:    NewTrainRunModel(mrs),
		trainCancel: cancel,
	}

	m.Update(trainAbortMsg{})

	assert.Equal(t, context.Canceled, ctx.Err())
}

func TestApp_TrainAbort_NilCancel_NoPanic(t *testing.T) {
	mrs := []*gitlab.MergeRequest{eligibleMR1}
	m := AppModel{
		screen:      ScreenTrainRun,
		cfg:         config.DefaultConfig(),
		trainRun:    NewTrainRunModel(mrs),
		trainCancel: nil,
	}

	assert.NotPanics(t, func() {
		m.Update(trainAbortMsg{})
	})
}

// ---------------------------------------------------------------------------
// Group 4: handleMRsLoaded Logic
// ---------------------------------------------------------------------------

func TestApp_HandleMRsLoaded_FirstLoad(t *testing.T) {
	mock := &train.MockClient{}
	mrList := NewMRListModel("team/project")
	mrList.loading = true

	m := AppModel{
		screen:    ScreenMRList,
		client:    mock,
		cfg:       config.DefaultConfig(),
		projectID: 42,
		mrList:    mrList,
	}

	updated, cmd := m.Update(mrsLoadedMsg{mrs: []*gitlab.MergeRequest{eligibleMR1, uncheckedMR}})
	app := updated.(AppModel)

	assert.False(t, app.mrList.loading)
	// refreshing is true because uncheckedMR has DetailedMergeStatus "unchecked".
	assert.True(t, app.mrList.refreshing)
	assert.NotNil(t, cmd)

	// executeBatchCmd runs all sub-commands in the batch, including triggerMergeChecks.
	executeBatchCmd(cmd)

	calls := mock.CallsTo("GetMergeRequest")
	require.Len(t, calls, 1)
	assert.Equal(t, 42, calls[0].Args[0]) // projectID
	assert.Equal(t, 60, calls[0].Args[1]) // IID of uncheckedMR
}

func TestApp_HandleMRsLoaded_NotRefreshing(t *testing.T) {
	mrList := NewMRListModel("team/project")
	mrList.loading = true
	mrList.refreshing = false

	m := AppModel{
		screen: ScreenMRList,
		cfg:    config.DefaultConfig(),
		mrList: mrList,
	}

	updated, _ := m.Update(mrsLoadedMsg{mrs: []*gitlab.MergeRequest{eligibleMR1}})
	app := updated.(AppModel)

	assert.False(t, app.mrList.refreshing)
}

// ---------------------------------------------------------------------------
// Group 5: Command Functions with Mock Client
// ---------------------------------------------------------------------------

func TestApp_FetchCurrentUser(t *testing.T) {
	mock := &train.MockClient{
		GetCurrentUserFn: func(_ context.Context) (*gitlab.User, error) {
			return &gitlab.User{Name: "Test"}, nil
		},
	}
	m := AppModel{client: mock}

	cmd := m.fetchCurrentUser()
	require.NotNil(t, cmd)

	msg := cmd()
	ulm, ok := msg.(userLoadedMsg)
	require.True(t, ok)
	assert.Equal(t, "Test", ulm.userName)
}

func TestApp_FetchCurrentUser_NilClient(t *testing.T) {
	m := AppModel{client: nil}
	cmd := m.fetchCurrentUser()
	assert.Nil(t, cmd)
}

func TestApp_FetchProjects(t *testing.T) {
	mock := &train.MockClient{
		ListProjectsFn: func(_ context.Context, _ string) ([]*gitlab.Project, error) {
			return testProjects, nil
		},
	}
	m := AppModel{client: mock}

	cmd := m.fetchProjects()
	require.NotNil(t, cmd)

	msg := cmd()
	plm, ok := msg.(projectsLoadedMsg)
	require.True(t, ok)
	assert.Len(t, plm.projects, 3)
}

func TestApp_FetchMRs(t *testing.T) {
	expectedMRs := []*gitlab.MergeRequest{eligibleMR1, eligibleMR2}
	mock := &train.MockClient{
		ListMergeRequestsFullFn: func(_ context.Context, _ string) ([]*gitlab.MergeRequest, error) {
			return expectedMRs, nil
		},
	}
	m := AppModel{
		client: mock,
		mrList: NewMRListModel("team/project"),
	}

	cmd := m.fetchMRs()
	require.NotNil(t, cmd)

	msg := cmd()
	mlm, ok := msg.(mrsLoadedMsg)
	require.True(t, ok)
	assert.Len(t, mlm.mrs, 2)
}
