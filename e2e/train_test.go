//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/sairus2k/glmt/internal/gitlab"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTrainMergesAllMRs is the main e2e test.
// It starts a real GitLab CE instance, creates MRs, and runs glmt to merge them.
func TestTrainMergesAllMRs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// Build glmt binary
	binPath := buildGlmt(t)

	// Start GitLab and seed data
	env := setupGitLab(t)
	defer env.cleanup()

	// Verify commit counts are populated via the glmt client
	apiClient, err := gitlab.NewAPIClient(env.gitlabURL, env.token)
	require.NoError(t, err)
	for _, iid := range env.mrIIDs {
		mr, err := apiClient.GetMergeRequest(context.Background(), env.projectID, iid)
		require.NoError(t, err, "GetMergeRequest for !%d", iid)
		assert.Equal(t, 1, mr.CommitCount, "MR !%d should have 1 commit", iid)
	}

	// Build the --mrs flag value
	mrsFlag := make([]string, len(env.mrIIDs))
	for i, iid := range env.mrIIDs {
		mrsFlag[i] = fmt.Sprintf("%d", iid)
	}

	// Run glmt in non-interactive mode
	cmd := exec.Command(binPath,
		"--non-interactive",
		"--host", env.gitlabURL,
		"--token", env.token,
		"--project-id", fmt.Sprintf("%d", env.projectID),
		"--mrs", strings.Join(mrsFlag, ","),
	)
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("glmt output:\n%s", output)

	// Assert: exit code 0 (all MRs merged)
	require.NoError(t, err, "glmt should exit 0 when all MRs merge successfully")

	// Assert: output contains merged status for each MR
	assert.Contains(t, output, "=== Train Results ===")
	for _, iid := range env.mrIIDs {
		assert.Contains(t, output, fmt.Sprintf("MR !%d: merged", iid))
	}

	// Verify MRs are actually merged via the API
	for _, iid := range env.mrIIDs {
		state := getMRState(t, env.gitlabURL, env.token, env.projectID, iid)
		assert.Equal(t, "merged", state, "MR !%d should be in merged state", iid)
	}
}

// TestTrainSkipsFailedPipeline tests that an MR with a failing pipeline is skipped.
func TestTrainSkipsFailedPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// This test would require more sophisticated setup (a branch with a failing CI).
	// For now, we test the core happy path above. Additional failure scenarios
	// are covered by the unit tests in internal/train/runner_test.go.
	t.Skip("TODO: implement failure scenario e2e test")
}

// getMRState returns the state of an MR ("opened", "merged", "closed").
func getMRState(t *testing.T, gitlabURL, token string, projectID, mrIID int) string {
	t.Helper()

	url := fmt.Sprintf("%s/api/v4/projects/%d/merge_requests/%d", gitlabURL, projectID, mrIID)
	cmd := exec.Command("curl", "-s", "-H", fmt.Sprintf("PRIVATE-TOKEN: %s", token), url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to get MR state: %v", err)
	}

	// Parse just the state field
	output := string(out)
	for _, line := range strings.Split(output, ",") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, `"state"`) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), `"`)
			}
		}
	}
	return ""
}
