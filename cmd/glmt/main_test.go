package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogout_RemovesConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(cfgPath, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use a non-existent glab dir so glab note is not printed.
	err := logout(cfgPath, filepath.Join(dir, "no-glab"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Error("config file should have been removed")
	}
}

func TestLogout_NoErrorWhenConfigMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "does-not-exist.toml")

	err := logout(cfgPath, filepath.Join(dir, "no-glab"))
	if err != nil {
		t.Fatalf("expected no error for missing config, got: %v", err)
	}
}

func TestLogout_PrintsGlabNote_WhenCredsExist(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// Create a fake glab config with credentials.
	glabDir := filepath.Join(dir, "glab-cli")
	if err := os.MkdirAll(glabDir, 0o755); err != nil {
		t.Fatal(err)
	}
	glabConfig := `hosts:
  gitlab.example.com:
    token: glpat-test
    api_protocol: https
`
	if err := os.WriteFile(filepath.Join(glabDir, "config.yml"), []byte(glabConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture stdout to verify the glab note.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := logout(cfgPath, glabDir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if expected := "Note: glab CLI credentials still exist"; !contains(output, expected) {
		t.Errorf("expected output to contain %q, got: %s", expected, output)
	}
}

func TestLogout_NoGlabNote_WhenNoGlabCreds(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := logout(cfgPath, filepath.Join(dir, "no-glab"))

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if contains(output, "glab CLI credentials") {
		t.Errorf("should not mention glab when no creds exist, got: %s", output)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
