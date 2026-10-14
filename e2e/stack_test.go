//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/sairus2k/glmt/internal/train"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testStackedMRs merges a stack of MRs (each targeting the previous MR's branch).
// It runs on the GitLab instance of TestTrainMergesMRs, since booting a second
// container would not fit the CI e2e timeout.
func testStackedMRs(t *testing.T, env *testEnv) {
	remote := strings.Replace(env.gitlabURL, "http://", fmt.Sprintf("http://root:%s@", env.token), 1) + "/root/glmt-e2e-test.git"
	runWithRetry(t, env.cloneDir, 5, 10*time.Second, "git", "fetch", remote, "main")

	branches := []string{"stack-a", "stack-b", "stack-c"}
	base := "FETCH_HEAD"
	var mrIIDs []int
	for i, branch := range branches {
		run(t, env.cloneDir, "git", "checkout", "-B", branch, base)
		commitFile(t, env.cloneDir, branch+".txt")
		runWithRetry(t, env.cloneDir, 5, 10*time.Second, "git", "push", remote, branch)

		target := "main"
		if i > 0 {
			target = branches[i-1]
		}
		mrIIDs = append(mrIIDs, createMR(t, env.gitlabURL, env.token, env.projectID, branch, target, "Add "+branch))
		base = branch
	}
	t.Logf("Created stacked MRs: %v", mrIIDs)
	waitForMRPipelines(t, env.gitlabURL, env.token, env.projectID, mrIIDs)

	// Move main so the base MR's rebase rewrites its commits: the stacked MRs then
	// carry stale copies of them, which their rebase onto main has to drop.
	run(t, env.cloneDir, "git", "checkout", "--detach", "FETCH_HEAD")
	commitFile(t, env.cloneDir, "stack-main-moved.txt")
	runWithRetry(t, env.cloneDir, 5, 10*time.Second, "git", "push", remote, "HEAD:main")

	client, err := gitlab.NewAPIClient(env.gitlabURL, env.token)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var mrs []*gitlab.MergeRequest
	for _, iid := range mrIIDs {
		mr, err := client.GetMergeRequest(ctx, env.projectID, iid)
		require.NoError(t, err)
		mrs = append(mrs, mr)
	}

	runner := train.NewRunner(client, env.projectID, func(mrIID int, step, message string) {
		t.Logf("[MR !%d] [%s] %s", mrIID, step, message)
	})
	result, err := runner.Run(ctx, mrs)
	require.NoError(t, err)

	for _, res := range result.MRResults {
		assert.Equal(t, train.MRStatusMerged, res.Status, "MR !%d: %s", res.MR.IID, res.SkipReason)
	}
	for _, iid := range mrIIDs {
		mr, err := client.GetMergeRequest(ctx, env.projectID, iid)
		require.NoError(t, err)
		assert.Equal(t, "merged", mr.State, "MR !%d state", iid)
		assert.Equal(t, "main", mr.TargetBranch, "MR !%d target branch", iid)
	}

	titles := mainCommitTitles(t, env)
	for _, branch := range branches {
		want := "Add " + branch + ".txt"
		count := 0
		for _, title := range titles {
			if title == want {
				count++
			}
		}
		assert.Equal(t, 1, count, "commit %q on main", want)
	}
}

func commitFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0644); err != nil {
		t.Fatalf("Failed to write %s: %v", name, err)
	}
	run(t, dir, "git", "add", name)
	run(t, dir, "git", "commit", "-m", "Add "+name)
}

func mainCommitTitles(t *testing.T, env *testEnv) []string {
	t.Helper()
	url := fmt.Sprintf("%s/api/v4/projects/%d/repository/commits?ref_name=main&per_page=100", env.gitlabURL, env.projectID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("PRIVATE-TOKEN", env.token)
	resp := apiDo(t, req)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var commits []struct {
		Title string `json:"title"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&commits))
	titles := make([]string, len(commits))
	for i, c := range commits {
		titles[i] = c.Title
	}
	return titles
}
