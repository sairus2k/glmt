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
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

// testEnv holds references to the running GitLab container and test data.
type testEnv struct {
	gitlabURL string
	token     string
	projectID int
	mrIIDs    []int
	cleanup   func()
}

// setupGitLab starts a GitLab CE container, seeds test data, and returns the test environment.
func setupGitLab(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	// Create a Docker network so GitLab and the runner can communicate
	t.Log("Creating Docker network...")
	net, err := network.New(ctx)
	if err != nil {
		t.Fatalf("Failed to create Docker network: %v", err)
	}
	networkName := net.Name

	t.Log("Starting GitLab CE container (this takes a few minutes)...")
	gitlabReq := testcontainers.ContainerRequest{
		Image:        "gitlab/gitlab-ce:17.4.0-ce.0",
		ExposedPorts: []string{"80/tcp"},
		Networks:     []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"gitlab"},
		},
		Env: map[string]string{
			"GITLAB_OMNIBUS_CONFIG": strings.Join([]string{
				"external_url 'http://gitlab'",
				"gitlab_rails['initial_root_password'] = 'glmt-test-password-123'",
				// Reduce memory footprint
				"puma['worker_processes'] = 0",
				"sidekiq['concurrency'] = 2",
				"postgresql['shared_buffers'] = '128MB'",
				// Disable non-essential services
				"prometheus_monitoring['enable'] = false",
				"alertmanager['enable'] = false",
				"gitlab_kas['enable'] = false",
				"registry['enable'] = false",
				"mattermost['enable'] = false",
				"gitlab_pages['enable'] = false",
				"gitlab_rails['gitlab_shell_ssh_port'] = 2222",
			}, "; "),
		},
		// No WaitingFor — we handle readiness ourselves via waitForAPI()
		// because GitLab CE is amd64-only and very slow to boot on arm64.
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

	projectID := createProject(t, gitlabURL, token)
	t.Logf("Created test project with ID %d", projectID)

	createFile(t, gitlabURL, token, projectID, "main", ".gitlab-ci.yml", `
test:
  script:
    - "true"
  tags:
    - shared
`)
	t.Log("Created .gitlab-ci.yml on main branch")

	// Create runner via API and start sidecar container
	runnerContainer := startRunner(t, ctx, networkName, gitlabURL, token)
	t.Log("Runner sidecar started")

	// Wait for GitLab to stabilize after runner start (memory pressure can cause 502s)
	waitForAPI(t, gitlabURL)

	mrIIDs := createTestMRs(t, gitlabURL, token, projectID)
	t.Logf("Created %d test MRs: %v", len(mrIIDs), mrIIDs)

	waitForMRPipelines(t, gitlabURL, token, projectID, mrIIDs)
	t.Log("All MR pipelines passed")

	return &testEnv{
		gitlabURL: gitlabURL,
		token:     token,
		projectID: projectID,
		mrIIDs:    mrIIDs,
		cleanup: func() {
			_ = runnerContainer.Terminate(ctx)
			_ = gitlabContainer.Terminate(ctx)
			_ = net.Remove(ctx)
		},
	}
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
	resp, err := http.Post(gitlabURL+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(data))
	if err != nil {
		t.Fatalf("Failed to get OAuth token: %v", err)
	}
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

	req, _ := http.NewRequest("POST", gitlabURL+"/api/v4/users/1/personal_access_tokens", bytes.NewReader(patJSON))
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
		"name":                   projectName,
		"visibility":             "private",
		"initialize_with_readme": true,
		"default_branch":         "main",
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

func createFile(t *testing.T, gitlabURL, token string, projectID int, branch, filePath, content string) {
	t.Helper()

	data := map[string]interface{}{
		"branch":         branch,
		"content":        content,
		"commit_message": fmt.Sprintf("Add %s", filePath),
	}
	body, _ := json.Marshal(data)

	url := fmt.Sprintf("%s/api/v4/projects/%d/repository/files/%s", gitlabURL, projectID, filePath)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", token)

	resp := apiDo(t, req)

	if resp.StatusCode == 400 {
		_ = resp.Body.Close()
		// File already exists, update it
		req2, _ := http.NewRequest("PUT", url, bytes.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("PRIVATE-TOKEN", token)
		resp2 := apiDo(t, req2)
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp2.Body)
			t.Fatalf("File update for %s failed with status %d: %s", filePath, resp2.StatusCode, string(respBody))
		}
		return
	}

	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("File creation for %s failed with status %d: %s", filePath, resp.StatusCode, string(respBody))
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

	// Runner config — connects to GitLab via the Docker network alias "gitlab"
	configContent := fmt.Sprintf(`concurrent = 1
check_interval = 3

[[runners]]
  name = "e2e-runner"
  url = "http://gitlab"
  token = "%s"
  executor = "shell"
  [runners.cache]
`, runnerResp.Token)

	// Start gitlab-runner sidecar container
	runnerReq := testcontainers.ContainerRequest{
		Image:    "gitlab/gitlab-runner:v17.4.0",
		Networks: []string{networkName},
		Files: []testcontainers.ContainerFile{
			{
				Reader:            strings.NewReader(configContent),
				ContainerFilePath: "/etc/gitlab-runner/config.toml",
				FileMode:          0644,
			},
		},
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

func createTestMRs(t *testing.T, gitlabURL, token string, projectID int) []int {
	t.Helper()

	branches := []struct {
		name     string
		fileName string
		content  string
		mrTitle  string
	}{
		{"feature-a", "feature_a.txt", "Feature A content\n", "Add feature A"},
		{"feature-b", "feature_b.txt", "Feature B content\n", "Add feature B"},
	}

	var mrIIDs []int

	for _, br := range branches {
		brData := map[string]interface{}{
			"branch": br.name,
			"ref":    "main",
		}
		body, _ := json.Marshal(brData)
		req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v4/projects/%d/repository/branches", gitlabURL, projectID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("PRIVATE-TOKEN", token)
		resp := apiDo(t, req)
		_ = resp.Body.Close()

		createFile(t, gitlabURL, token, projectID, br.name, br.fileName, br.content)

		mrData := map[string]interface{}{
			"source_branch": br.name,
			"target_branch": "main",
			"title":         br.mrTitle,
		}
		mrBody, _ := json.Marshal(mrData)
		mrReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v4/projects/%d/merge_requests", gitlabURL, projectID), bytes.NewReader(mrBody))
		mrReq.Header.Set("Content-Type", "application/json")
		mrReq.Header.Set("PRIVATE-TOKEN", token)
		mrResp := apiDo(t, mrReq)

		if mrResp.StatusCode != 201 {
			respBody, _ := io.ReadAll(mrResp.Body)
			_ = mrResp.Body.Close()
			t.Fatalf("MR creation failed for %s with status %d: %s", br.name, mrResp.StatusCode, string(respBody))
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

func buildGlmt(t *testing.T) string {
	t.Helper()

	binPath := t.TempDir() + "/glmt"
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/glmt/")
	cmd.Dir = projectRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build glmt: %v\n%s", err, string(out))
	}
	return binPath
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	if strings.HasSuffix(wd, "/e2e") {
		return wd[:len(wd)-4]
	}
	return wd
}
