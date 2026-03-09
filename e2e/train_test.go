//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sairus2k/glmt/internal/auth"
	"github.com/sairus2k/glmt/internal/config"
	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/sairus2k/glmt/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrainMergesMRs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	const mrCount = 3

	env := setupGitLabN(t, mrCount)
	defer env.cleanup()

	// Build credentials and config for in-process TUI.
	creds := &auth.Credentials{
		Host:     env.gitlabURL,
		Token:    env.token,
		Protocol: "http",
	}

	cfg := config.DefaultConfig()
	cfg.Defaults.ProjectID = env.projectID
	cfg.Defaults.Repo = env.repoPath

	tmpCfgPath := t.TempDir() + "/glmt.toml"

	model := tui.NewAppModel(creds, cfg, tmpCfgPath)

	// Channels for synchronization via tea.WithFilter.
	mrsLoaded := make(chan struct{}, 16)
	trainDone := make(chan struct{}, 1)

	filter := func(m tea.Model, msg tea.Msg) tea.Msg {
		typeName := fmt.Sprintf("%T", msg)
		if strings.HasSuffix(typeName, "mrsLoadedMsg") {
			select {
			case mrsLoaded <- struct{}{}:
			default:
			}
		}
		if strings.HasSuffix(typeName, "trainDoneMsg") {
			select {
			case trainDone <- struct{}{}:
			default:
			}
		}
		return msg
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	var buf bytes.Buffer
	p := tea.NewProgram(model,
		tea.WithInput(nil),
		tea.WithOutput(&buf),
		tea.WithWindowSize(120, 40),
		tea.WithContext(ctx),
		tea.WithFilter(filter),
		tea.WithoutSignals(),
	)

	// Run program in goroutine.
	type runResult struct {
		model tea.Model
		err   error
	}
	done := make(chan runResult, 1)
	go func() {
		m, err := p.Run()
		done <- runResult{model: m, err: err}
	}()

	// Wait for initial MR list load.
	t.Log("Waiting for MR list to load...")
	select {
	case <-mrsLoaded:
		t.Log("MR list loaded")
	case <-ctx.Done():
		t.Fatal("Timeout waiting for MR list to load")
	}

	// Wait for unchecked merge statuses to resolve (background refresh polls every 4s).
	t.Log("Waiting for merge statuses to resolve...")
	deadline := time.After(60 * time.Second)
	for {
		select {
		case <-mrsLoaded:
			t.Log("MR list refreshed (background refetch)")
		case <-deadline:
			t.Log("Merge status wait deadline reached, proceeding")
			goto statusesDone
		case <-ctx.Done():
			t.Fatal("Context cancelled while waiting for merge statuses")
		}
	}
statusesDone:

	// Select all eligible MRs.
	t.Log("Sending 'a' to select all eligible MRs")
	p.Send(tea.KeyPressMsg(tea.Key{Code: -1, Text: "a"}))

	// Small delay to let the message process.
	time.Sleep(100 * time.Millisecond)

	// Start the train.
	t.Log("Sending Enter to start train")
	p.Send(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	// Wait for train completion.
	t.Log("Waiting for train to complete...")
	select {
	case <-trainDone:
		t.Log("Train completed")
	case <-ctx.Done():
		t.Fatal("Timeout waiting for train to complete")
	}

	// Small delay to let the final model state settle.
	time.Sleep(200 * time.Millisecond)

	// Quit the program.
	t.Log("Sending 'q' to quit")
	p.Send(tea.KeyPressMsg(tea.Key{Code: -1, Text: "q"}))

	// Wait for p.Run() to return.
	select {
	case res := <-done:
		require.NoError(t, res.err, "tea.Program.Run should not error")
		t.Logf("Program exited, output length: %d bytes", buf.Len())
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for program to exit")
	}

	// Verify all MRs are actually merged via the GitLab API.
	apiClient, err := gitlab.NewAPIClient(env.gitlabURL, env.token)
	require.NoError(t, err)

	for _, iid := range env.mrIIDs {
		mr, err := apiClient.GetMergeRequest(context.Background(), env.projectID, iid)
		require.NoError(t, err, "GetMergeRequest for !%d", iid)
		assert.Equal(t, "merged", mr.State, "MR !%d should be in merged state", iid)
	}
}
