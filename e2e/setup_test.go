//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

// testEnv holds references to the running GitLab container and test data.
type testEnv struct {
	gitlabURL string
	token     string
	projectID int
	repoPath  string
	mrIIDs    []int
	cleanup   func()
}

func waitForAPI(t *testing.T, gitlabURL string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		resp, err := http.Get(gitlabURL + "/api/v4/version")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 401 {
				return
			}
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatal("GitLab API did not become ready in time")
}

// apiDo performs an HTTP request with retry on transient errors (502, 503, EOF).
func apiDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("Failed to read request body: %v", err)
		}
	}

	for time.Now().Before(deadline) {
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("Retrying %s %s: %v", req.Method, req.URL.Path, err)
			time.Sleep(5 * time.Second)
			continue
		}
		if resp.StatusCode == 502 || resp.StatusCode == 503 {
			_ = resp.Body.Close()
			t.Logf("Retrying %s %s: status %d", req.Method, req.URL.Path, resp.StatusCode)
			time.Sleep(5 * time.Second)
			continue
		}
		return resp
	}
	t.Fatalf("Timeout on %s %s", req.Method, req.URL.Path)
	return nil
}

func createRootToken(t *testing.T, gitlabURL string) string {
	t.Helper()

	data := fmt.Sprintf(`grant_type=password&username=root&password=%s`, "glmt-test-password-123")
	req, _ := http.NewRequest("POST", gitlabURL+"/oauth/token", strings.NewReader(data))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := apiDo(t, req)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("OAuth token request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var oauthResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&oauthResp); err != nil {
		t.Fatalf("Failed to decode OAuth response: %v", err)
	}

	patData := map[string]interface{}{
		"name":       "glmt-e2e-test",
		"scopes":     []string{"api"},
		"expires_at": time.Now().Add(24 * time.Hour).Format("2006-01-02"),
	}
	patJSON, _ := json.Marshal(patData)

	req, _ = http.NewRequest("POST", gitlabURL+"/api/v4/users/1/personal_access_tokens", bytes.NewReader(patJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+oauthResp.AccessToken)

	resp2 := apiDo(t, req)
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != 201 {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("PAT creation failed with status %d: %s", resp2.StatusCode, string(body))
	}

	var patResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&patResp); err != nil {
		t.Fatalf("Failed to decode PAT response: %v", err)
	}

	return patResp.Token
}

func createProject(t *testing.T, gitlabURL, token string) int {
	t.Helper()

	projectName := "glmt-e2e-test"

	data := map[string]interface{}{
		"name":                                  projectName,
		"visibility":                            "private",
		"merge_method":                          "rebase_merge",
		"only_allow_merge_if_pipeline_succeeds": true,
		"allow_merge_on_skipped_pipeline":       true,
	}
	body, _ := json.Marshal(data)

	req, _ := http.NewRequest("POST", gitlabURL+"/api/v4/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", token)

	resp := apiDo(t, req)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 400 {
		// Project may already exist due to a retried 502. Look it up by name.
		_ = resp.Body.Close()
		return findProjectByName(t, gitlabURL, token, projectName)
	}

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("Project creation failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var project struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&project); err != nil {
		t.Fatalf("Failed to decode project response: %v", err)
	}

	return project.ID
}

func findProjectByName(t *testing.T, gitlabURL, token, name string) int {
	t.Helper()

	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/v4/projects?search=%s&owned=true", gitlabURL, name), nil)
	req.Header.Set("PRIVATE-TOKEN", token)

	resp := apiDo(t, req)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("Project search failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var projects []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		t.Fatalf("Failed to decode project search response: %v", err)
	}

	for _, p := range projects {
		if p.Name == name {
			return p.ID
		}
	}

	t.Fatalf("Project %q not found after 400 response", name)
	return 0
}

// run executes a command in a directory and fatals on error.
func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Command %s %v failed in %s: %v\n%s", name, args, dir, err, string(out))
	}
}

// runWithRetry executes a command, retrying on transient errors (502, 503, "hung up")
// up to the given number of attempts with a delay between retries.
func runWithRetry(t *testing.T, dir string, attempts int, delay time.Duration, name string, args ...string) {
	t.Helper()
	for i := 0; i < attempts; i++ {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err == nil {
			return
		}
		output := string(out)
		transient := strings.Contains(output, "502") ||
			strings.Contains(output, "503") ||
			strings.Contains(output, "hung up") ||
			strings.Contains(output, "unexpected disconnect")
		if !transient || i == attempts-1 {
			t.Fatalf("Command %s %v failed in %s: %v\n%s", name, args, dir, err, output)
		}
		t.Logf("Retrying %s %v (attempt %d/%d): %s", name, args, i+1, attempts, output)
		time.Sleep(delay)
	}
}

// cloneRepo clones the glmt repo from GitHub to a temp directory.
func cloneRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "glmt")
	run(t, "", "git", "clone", "https://github.com/sairus2k/glmt.git", dir)
	run(t, dir, "git", "config", "user.email", "test@glmt-e2e.local")
	run(t, dir, "git", "config", "user.name", "E2E Test")
	return dir
}

// pushToGitLab adds a gitlab remote and pushes main branch.
func pushToGitLab(t *testing.T, cloneDir, gitlabURL, token string) {
	t.Helper()
	// Build authenticated remote URL: http://root:<token>@host:port/root/glmt-e2e-test.git
	remote := strings.Replace(gitlabURL, "http://", fmt.Sprintf("http://root:%s@", token), 1) + "/root/glmt-e2e-test.git"
	run(t, cloneDir, "git", "remote", "add", "gitlab", remote)
	runWithRetry(t, cloneDir, 5, 10*time.Second, "git", "push", "gitlab", "main")
}

// createBranchesAndPush creates n feature branches with test files and pushes them.
func createBranchesAndPush(t *testing.T, cloneDir, gitlabURL, token string, n int) {
	t.Helper()
	remote := strings.Replace(gitlabURL, "http://", fmt.Sprintf("http://root:%s@", token), 1) + "/root/glmt-e2e-test.git"
	for i := 0; i < n; i++ {
		branch := fmt.Sprintf("feature-%c", 'a'+i)
		fileName := fmt.Sprintf("e2e_test_feature_%c.txt", 'a'+i)
		run(t, cloneDir, "git", "checkout", "main")
		run(t, cloneDir, "git", "checkout", "-b", branch)
		filePath := filepath.Join(cloneDir, fileName)
		if err := os.WriteFile(filePath, []byte(fmt.Sprintf("Feature %c\n", 'A'+i)), 0644); err != nil {
			t.Fatalf("Failed to write %s: %v", fileName, err)
		}
		run(t, cloneDir, "git", "add", fileName)
		run(t, cloneDir, "git", "commit", "-m", fmt.Sprintf("Add %s", fileName))
		runWithRetry(t, cloneDir, 5, 10*time.Second, "git", "push", remote, branch)
	}
}

// startRunner creates a runner via the GitLab 17.x API and launches
// a gitlab/gitlab-runner sidecar container on the same Docker network.
func startRunner(t *testing.T, ctx context.Context, networkName, gitlabURL, token string) testcontainers.Container {
	t.Helper()

	// Create runner via new GitLab 17.x API (works with PAT)
	data := map[string]interface{}{
		"runner_type":  "instance_type",
		"tag_list":     []string{"shared"},
		"run_untagged": true,
		"description":  "e2e-shared-runner",
	}
	body, _ := json.Marshal(data)
	req, _ := http.NewRequest("POST", gitlabURL+"/api/v4/user/runners", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", token)

	resp := apiDo(t, req)
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("Runner creation failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var runnerResp struct {
		ID    int    `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runnerResp); err != nil {
		t.Fatalf("Failed to decode runner response: %v", err)
	}
	t.Logf("Created runner ID=%d", runnerResp.ID)

	// Runner config — Docker executor so CI jobs can use `image: golang:1.26`
	configContent := fmt.Sprintf(`concurrent = 1
check_interval = 3

[[runners]]
  name = "e2e-runner"
  url = "http://gitlab"
  token = "%s"
  executor = "docker"
  [runners.docker]
    image = "golang:1.26"
    privileged = false
    volumes = ["/var/run/docker.sock:/var/run/docker.sock"]
    network_mode = "%s"
    pull_policy = ["if-not-present"]
`, runnerResp.Token, networkName)

	// Start gitlab-runner sidecar container with Docker socket mounted
	runnerReq := testcontainers.ContainerRequest{
		Image:    "gitlab/gitlab-runner:v18.8.0",
		Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{
			{
				Reader:            strings.NewReader(configContent),
				ContainerFilePath: "/etc/gitlab-runner/config.toml",
				FileMode:          0644,
			},
		},
		Mounts: testcontainers.Mounts(
			testcontainers.BindMount("/var/run/docker.sock", "/var/run/docker.sock"),
		),
	}

	runnerContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: runnerReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start runner container: %v", err)
	}

	// Give the runner a moment to connect and verify
	time.Sleep(3 * time.Second)
	checkRunnersAPI(t, gitlabURL, token)

	return runnerContainer
}

// setupGitLabN starts a GitLab CE container, seeds test data with N MRs, and returns the test environment.
func setupGitLabN(t *testing.T, n int) *testEnv {
	t.Helper()
	ctx := context.Background()

	t.Log("Creating Docker network...")
	net, err := network.New(ctx)
	if err != nil {
		t.Fatalf("Failed to create Docker network: %v", err)
	}
	networkName := net.Name

	t.Log("Starting GitLab CE container (this takes a few minutes)...")
	gitlabReq := testcontainers.ContainerRequest{
		Image:        "gitlab/gitlab-ce:18.8.5-ce.0",
		ExposedPorts: []string{"80/tcp"},
		Networks:     []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"gitlab"},
		},
		Env: map[string]string{
			"GITLAB_OMNIBUS_CONFIG": strings.Join([]string{
				"external_url 'http://gitlab'",
				"gitlab_rails['initial_root_password'] = 'glmt-test-password-123'",
				"puma['worker_processes'] = 0",
				"sidekiq['concurrency'] = 2",
				"postgresql['shared_buffers'] = '128MB'",
				"prometheus_monitoring['enable'] = false",
				"alertmanager['enable'] = false",
				"gitlab_kas['enable'] = false",
				"registry['enable'] = false",
				"mattermost['enable'] = false",
				"gitlab_pages['enable'] = false",
				"gitlab_rails['gitlab_shell_ssh_port'] = 2222",
			}, "; "),
		},
	}

	gitlabContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: gitlabReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start GitLab container: %v", err)
	}

	mappedPort, err := gitlabContainer.MappedPort(ctx, "80")
	if err != nil {
		t.Fatalf("Failed to get mapped port: %v", err)
	}

	hostIP, err := gitlabContainer.Host(ctx)
	if err != nil {
		t.Fatalf("Failed to get container host: %v", err)
	}

	gitlabURL := fmt.Sprintf("http://%s:%s", hostIP, mappedPort.Port())
	t.Logf("GitLab CE available at %s", gitlabURL)

	waitForAPI(t, gitlabURL)

	token := createRootToken(t, gitlabURL)
	t.Log("Created root access token")

	t.Log("Cloning glmt repo from GitHub...")
	cloneDir := cloneRepo(t)

	projectID := createProject(t, gitlabURL, token)
	t.Logf("Created test project with ID %d", projectID)

	t.Log("Pushing cloned repo to GitLab...")
	pushToGitLab(t, cloneDir, gitlabURL, token)

	runnerContainer := startRunner(t, ctx, networkName, gitlabURL, token)
	t.Log("Runner sidecar started")

	t.Log("Creating feature branches and pushing...")
	createBranchesAndPush(t, cloneDir, gitlabURL, token, n)

	mrIIDs := createTestMRsN(t, gitlabURL, token, projectID, n)
	t.Logf("Created %d test MRs: %v", len(mrIIDs), mrIIDs)

	waitForMRPipelines(t, gitlabURL, token, projectID, mrIIDs)
	t.Log("All MR pipelines passed")

	return &testEnv{
		gitlabURL: gitlabURL,
		token:     token,
		projectID: projectID,
		repoPath:  "root/glmt-e2e-test",
		mrIIDs:    mrIIDs,
		cleanup: func() {
			_ = runnerContainer.Terminate(ctx)
			_ = gitlabContainer.Terminate(ctx)
			_ = net.Remove(ctx)
		},
	}
}

func createTestMRsN(t *testing.T, gitlabURL, token string, projectID, n int) []int {
	t.Helper()

	var mrIIDs []int

	for i := 0; i < n; i++ {
		branchName := fmt.Sprintf("feature-%c", 'a'+i)
		mrTitle := fmt.Sprintf("Add feature %c", 'A'+i)

		mrData := map[string]interface{}{
			"source_branch": branchName,
			"target_branch": "main",
			"title":         mrTitle,
		}
		mrBody, _ := json.Marshal(mrData)
		mrReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v4/projects/%d/merge_requests", gitlabURL, projectID), bytes.NewReader(mrBody))
		mrReq.Header.Set("Content-Type", "application/json")
		mrReq.Header.Set("PRIVATE-TOKEN", token)
		mrResp := apiDo(t, mrReq)

		if mrResp.StatusCode != 201 {
			respBody, _ := io.ReadAll(mrResp.Body)
			_ = mrResp.Body.Close()
			t.Fatalf("MR creation failed for %s with status %d: %s", branchName, mrResp.StatusCode, string(respBody))
		}

		var mr struct {
			IID int `json:"iid"`
		}
		if err := json.NewDecoder(mrResp.Body).Decode(&mr); err != nil {
			_ = mrResp.Body.Close()
			t.Fatalf("Failed to decode MR response: %v", err)
		}
		_ = mrResp.Body.Close()
		mrIIDs = append(mrIIDs, mr.IID)
	}

	return mrIIDs
}

func waitForMRPipelines(t *testing.T, gitlabURL, token string, projectID int, mrIIDs []int) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Minute)

	for _, iid := range mrIIDs {
		t.Logf("Waiting for pipeline on MR !%d...", iid)
		for time.Now().Before(deadline) {
			status := getMRPipelineStatus(t, gitlabURL, token, projectID, iid)
			switch status {
			case "success":
				t.Logf("MR !%d pipeline passed", iid)
				goto nextMR
			case "failed", "canceled", "skipped":
				t.Fatalf("MR !%d pipeline %s", iid, status)
			case "":
				t.Logf("MR !%d: no pipeline yet, waiting...", iid)
			default:
				t.Logf("MR !%d pipeline status: %s", iid, status)
			}
			time.Sleep(10 * time.Second)
		}
		t.Fatalf("Timeout waiting for MR !%d pipeline", iid)
	nextMR:
	}
}

func checkRunnersAPI(t *testing.T, gitlabURL, token string) {
	t.Helper()
	req, _ := http.NewRequest("GET", gitlabURL+"/api/v4/runners/all", nil)
	req.Header.Set("PRIVATE-TOKEN", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("Failed to check runners: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	t.Logf("Registered runners: %s", string(body))
}

func getMRPipelineStatus(t *testing.T, gitlabURL, token string, projectID, mrIID int) string {
	t.Helper()

	url := fmt.Sprintf("%s/api/v4/projects/%d/merge_requests/%d", gitlabURL, projectID, mrIID)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 502 || resp.StatusCode == 503 {
		return ""
	}

	var mr struct {
		HeadPipeline *struct {
			Status string `json:"status"`
		} `json:"head_pipeline"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return ""
	}

	if mr.HeadPipeline == nil {
		return ""
	}
	return mr.HeadPipeline.Status
}
