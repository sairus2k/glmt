package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCredentials_SingleHost(t *testing.T) {
	configDir := setupConfigDir(t, filepath.Join("testdata", "glab_config_single_host.yml"))

	creds, err := ReadCredentials(configDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if creds.Host != "gitlab.example.com" {
		t.Errorf("got host %q, want %q", creds.Host, "gitlab.example.com")
	}

	if creds.Token != "glpat-xxxxxxxxxxxxxxxxxxxx" {
		t.Errorf("got token %q, want %q", creds.Token, "glpat-xxxxxxxxxxxxxxxxxxxx")
	}

	if creds.Protocol != "https" {
		t.Errorf("got protocol %q, want %q", creds.Protocol, "https")
	}
}

func TestReadCredentials_MultiHost_Default(t *testing.T) {
	configDir := setupConfigDir(t, filepath.Join("testdata", "glab_config_multi_host.yml"))

	creds, err := ReadCredentials(configDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The first host in file order should be returned.
	if creds.Host != "gitlab.example.com" {
		t.Errorf("got host %q, want %q", creds.Host, "gitlab.example.com")
	}

	if creds.Token != "glpat-xxxxxxxxxxxxxxxxxxxx" {
		t.Errorf("got token %q, want %q", creds.Token, "glpat-xxxxxxxxxxxxxxxxxxxx")
	}

	if creds.Protocol != "https" {
		t.Errorf("got protocol %q, want %q", creds.Protocol, "https")
	}
}

func TestReadCredentials_MultiHost_Specific(t *testing.T) {
	configDir := setupConfigDir(t, filepath.Join("testdata", "glab_config_multi_host.yml"))

	creds, err := ReadCredentials(configDir, "gitlab.other.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if creds.Host != "gitlab.other.com" {
		t.Errorf("got host %q, want %q", creds.Host, "gitlab.other.com")
	}

	if creds.Token != "glpat-yyyyyyyyyyyyyyyyyyyy" {
		t.Errorf("got token %q, want %q", creds.Token, "glpat-yyyyyyyyyyyyyyyyyyyy")
	}

	if creds.Protocol != "http" {
		t.Errorf("got protocol %q, want %q", creds.Protocol, "http")
	}
}

func TestReadCredentials_MultiHost_NotFound(t *testing.T) {
	configDir := setupConfigDir(t, filepath.Join("testdata", "glab_config_multi_host.yml"))

	_, err := ReadCredentials(configDir, "unknown.host.com")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	want := "host unknown.host.com not found"
	if err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}

func TestReadCredentials_NoFile(t *testing.T) {
	configDir := t.TempDir()

	_, err := ReadCredentials(configDir, "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.HasPrefix(err.Error(), "no glab config found at") {
		t.Errorf("got error %q, want prefix %q", err.Error(), "no glab config found at")
	}
}

func TestReadCredentials_EmptyHosts(t *testing.T) {
	configDir := setupConfigDirWithContent(t, "hosts:\n")

	_, err := ReadCredentials(configDir, "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	want := "no hosts configured"
	if err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}

// setupConfigDir copies a fixture file into a temp directory as config.yml.
func setupConfigDir(t *testing.T, fixturePath string) string {
	t.Helper()

	content, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", fixturePath, err)
	}

	return setupConfigDirWithContent(t, string(content))
}

// setupConfigDirWithContent writes content to config.yml in a temp directory.
func setupConfigDirWithContent(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")

	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	return dir
}
