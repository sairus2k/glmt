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

func TestTrainMergesMRs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	const mrCount = 3

	env := setupGitLabN(t, mrCount)
	defer env.cleanup()

	apiClient, err := gitlab.NewAPIClient(env.gitlabURL, env.token)
	require.NoError(t, err)

	// Build the --mrs flag value
	mrsFlag := make([]string, len(env.mrIIDs))
	for i, iid := range env.mrIIDs {
		mrsFlag[i] = fmt.Sprintf("%d", iid)
	}

	// Run glmt in non-interactive mode
	cmd := exec.Command(glmtBin,
		"--non-interactive",
		"--host", env.gitlabURL,
		"--token", env.token,
		"--project-id", fmt.Sprintf("%d", env.projectID),
		"--mrs", strings.Join(mrsFlag, ","),
	)
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("glmt output:\n%s", output)

	require.NoError(t, err, "glmt should exit 0 when all MRs merge successfully")

	assert.Contains(t, output, "=== Train Results ===")
	for _, iid := range env.mrIIDs {
		assert.Contains(t, output, fmt.Sprintf("MR !%d: merged", iid))
	}

	// Pipeline cancellation should happen for all non-last MRs
	assert.GreaterOrEqual(t, strings.Count(output, "Cancelled main pipeline"), mrCount-1,
		"should cancel main pipeline at least %d times for %d-MR train", mrCount-1, mrCount)

	// Verify MRs are actually merged via the API
	for _, iid := range env.mrIIDs {
		mr, err := apiClient.GetMergeRequest(context.Background(), env.projectID, iid)
		require.NoError(t, err, "GetMergeRequest for !%d", iid)
		assert.Equal(t, "merged", mr.State, "MR !%d should be in merged state", iid)
	}
}
