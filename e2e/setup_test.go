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
	"github.com/testcontainers/testcontainers-go/wait"
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
// This is slow (~3-5 min for GitLab to boot).
func setupGitLab(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	t.Log("Starting GitLab CE container (this takes a few minutes)...")
	req := testcontainers.ContainerRequest{
		Image:        "gitlab/gitlab-ce:latest",
		ExposedPorts: []string{"80/tcp"},
		Env: map[string]string{
			"GITLAB_OMNIBUS_CONFIG": strings.Join([]string{
				"external_url 'http://gitlab.local'",
				"gitlab_rails['initial_root_password'] = 'glmt-test-password-123'",
				"prometheus_monitoring['enable'] = false",
				"alertmanager['enable'] = false",
				"grafana['enable'] = false",
				"gitlab_kas['enable'] = false",
				"sentinel['enable'] = false",
				"registry['enable'] = false",
				"mattermost['enable'] = false",
				"gitlab_pages['enable'] = false",
				"gitlab_rails['gitlab_shell_ssh_port'] = 2222",
			}, "; "),
		},
		WaitingFor: wait.ForHTTP("/-/readiness").
			WithPort("80/tcp").
			WithStatusCodeMatcher(func(status int) bool {
				return status == 200
			}).
			WithStartupTimeout(10 * time.Minute).
			WithPollInterval(10 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start GitLab container: %v", err)
	}

	mappedPort, err := container.MappedPort(ctx, "80")
	if err != nil {
		t.Fatalf("Failed to get mapped port: %v", err)
	}

	hostIP, err := container.Host(ctx)
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

	registerRunner(t, ctx, container, gitlabURL, token)
	t.Log("Registered shared runner")

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
			_ = container.Terminate(ctx)
		},
	}
}

func waitForAPI(t *testing.T, gitlabURL string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
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

	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to create PAT: %v", err)
	}
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

	data := map[string]interface{}{
		"name":                   "glmt-e2e-test",
		"visibility":            "private",
		"initialize_with_readme": true,
		"default_branch":        "main",
	}
	body, _ := json.Marshal(data)

	req, _ := http.NewRequest("POST", gitlabURL+"/api/v4/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to create file %s: %v", filePath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 400 {
		req2, _ := http.NewRequest("PUT", url, bytes.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("PRIVATE-TOKEN", token)
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatalf("Failed to update file %s: %v", filePath, err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp2.Body)
			t.Fatalf("File update for %s failed with status %d: %s", filePath, resp2.StatusCode, string(respBody))
		}
	} else if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("File creation for %s failed with status %d: %s", filePath, resp.StatusCode, string(respBody))
	}
}

func registerRunner(t *testing.T, ctx context.Context, container testcontainers.Container, gitlabURL, token string) {
	t.Helper()

	data := map[string]interface{}{
		"token":        token,
		"description":  "e2e-shared-runner",
		"tag_list":     "shared",
		"run_untagged": true,
		"access_level": "not_protected",
	}
	body, _ := json.Marshal(data)
	req, _ := http.NewRequest("POST", gitlabURL+"/api/v4/runners", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to register runner: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Logf("Runner registration via API failed (%d): %s", resp.StatusCode, string(respBody))
		registerRunnerViaExec(t, ctx, container)
		return
	}

	var runnerResp struct {
		ID    int    `json:"id"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runnerResp); err != nil {
		t.Fatalf("Failed to decode runner response: %v", err)
	}

	startRunnerProcess(t, ctx, container, runnerResp.Token)
}

func registerRunnerViaExec(t *testing.T, ctx context.Context, container testcontainers.Container) {
	t.Helper()

	code, reader, err := container.Exec(ctx, []string{
		"gitlab-rails", "runner",
		"puts Gitlab::CurrentSettings.current_application_settings.runners_registration_token",
	})
	if err != nil || code != 0 {
		_, reader, err = container.Exec(ctx, []string{
			"gitlab-rails", "runner",
			"puts ApplicationSetting.current.runners_registration_token || 'none'",
		})
		if err != nil {
			t.Logf("Warning: could not get runner registration token: %v", err)
			return
		}
	}

	output, _ := io.ReadAll(reader)
	regToken := strings.TrimSpace(string(output))
	if regToken == "" || regToken == "none" {
		t.Log("Warning: no runner registration token available, skipping runner setup")
		return
	}

	_, _, err = container.Exec(ctx, []string{
		"gitlab-runner", "register",
		"--non-interactive",
		"--url", "http://localhost",
		"--registration-token", regToken,
		"--executor", "shell",
		"--tag-list", "shared",
		"--run-untagged=true",
		"--description", "e2e-runner",
	})
	if err != nil {
		t.Logf("Warning: runner registration failed: %v", err)
		return
	}

	_, _, err = container.Exec(ctx, []string{
		"sh", "-c", "nohup gitlab-runner run --working-directory=/tmp/runner &",
	})
	if err != nil {
		t.Logf("Warning: runner start failed: %v", err)
	}
}

func startRunnerProcess(t *testing.T, ctx context.Context, container testcontainers.Container, runnerToken string) {
	t.Helper()

	configContent := fmt.Sprintf(`
concurrent = 1
[[runners]]
  name = "e2e-runner"
  url = "http://localhost"
  token = "%s"
  executor = "shell"
  [runners.cache]
`, runnerToken)

	code, _, err := container.Exec(ctx, []string{
		"sh", "-c", fmt.Sprintf("mkdir -p /etc/gitlab-runner && echo '%s' > /etc/gitlab-runner/config.toml", configContent),
	})
	if err != nil || code != 0 {
		t.Logf("Warning: failed to write runner config: %v (code %d)", err, code)
		return
	}

	code, _, err = container.Exec(ctx, []string{
		"sh", "-c", "nohup gitlab-runner run &",
	})
	if err != nil || code != 0 {
		t.Logf("Warning: failed to start runner: %v (code %d)", err, code)
	}
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
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to create branch %s: %v", br.name, err)
		}
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
		mrResp, err := http.DefaultClient.Do(mrReq)
		if err != nil {
			t.Fatalf("Failed to create MR for %s: %v", br.name, err)
		}

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
