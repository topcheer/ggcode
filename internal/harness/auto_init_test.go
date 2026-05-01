package harness

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAutoInit_CreatesMinimalConfig(t *testing.T) {
	dir := t.TempDir()

	result, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("AutoInit() error = %v", err)
	}

	// Verify result fields
	if result.Project.RootDir != dir {
		t.Errorf("Project.RootDir = %q, want %q", result.Project.RootDir, dir)
	}
	if result.Config == nil {
		t.Fatal("Config is nil")
	}

	// Verify harness.yaml exists
	configPath := filepath.Join(dir, ".ggcode", "harness.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("harness.yaml not created")
	}

	// Verify harness.yaml is valid YAML with expected structure
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read harness.yaml: %v", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal harness.yaml: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("config version = %d, want 1", cfg.Version)
	}
}

func TestAutoInit_CreatesStateDirectories(t *testing.T) {
	dir := t.TempDir()

	_, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("AutoInit() error = %v", err)
	}

	expectedDirs := []string{
		filepath.Join(dir, ".ggcode", "harness"),
		filepath.Join(dir, ".ggcode", "harness", "tasks"),
		filepath.Join(dir, ".ggcode", "harness", "logs"),
		filepath.Join(dir, ".ggcode", "harness", "archive"),
	}
	for _, d := range expectedDirs {
		info, err := os.Stat(d)
		if os.IsNotExist(err) {
			t.Errorf("directory %s not created", d)
		} else if !info.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
	}
}

func TestAutoInit_DoesNotCreateAgentsMD(t *testing.T) {
	dir := t.TempDir()

	_, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("AutoInit() error = %v", err)
	}

	agentsPath := filepath.Join(dir, "AGENTS.md")
	if _, err := os.Stat(agentsPath); err == nil {
		t.Error("AGENTS.md should not be created by AutoInit")
	}
}

func TestAutoInit_DoesNotCreateRunbooks(t *testing.T) {
	dir := t.TempDir()

	_, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("AutoInit() error = %v", err)
	}

	runbookPath := filepath.Join(dir, "docs", "runbooks", "harness.md")
	if _, err := os.Stat(runbookPath); err == nil {
		t.Error("runbook should not be created by AutoInit")
	}
}

func TestAutoInit_FailsIfAlreadyInitialized(t *testing.T) {
	dir := t.TempDir()

	// First init should succeed
	_, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("first AutoInit() error = %v", err)
	}

	// Second init should fail
	_, err = AutoInit(dir)
	if err == nil {
		t.Fatal("second AutoInit() should fail when harness.yaml already exists")
	}
}

func TestAutoInit_CreatedPathsListed(t *testing.T) {
	dir := t.TempDir()

	result, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("AutoInit() error = %v", err)
	}

	if len(result.CreatedPaths) < 4 {
		t.Errorf("CreatedPaths has %d entries, want at least 4 (dirs + config)", len(result.CreatedPaths))
	}

	// Config path should be in created paths
	found := false
	for _, p := range result.CreatedPaths {
		if p == result.ConfigPath {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ConfigPath %q not in CreatedPaths", result.ConfigPath)
	}
}

func TestAutoInit_ConfigPath(t *testing.T) {
	dir := t.TempDir()

	result, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("AutoInit() error = %v", err)
	}

	expected := filepath.Join(dir, ".ggcode", "harness.yaml")
	if result.ConfigPath != expected {
		t.Errorf("ConfigPath = %q, want %q", result.ConfigPath, expected)
	}
}

func TestAutoInit_EmptyProjectDir(t *testing.T) {
	dir := t.TempDir()

	result, err := AutoInit(dir)
	if err != nil {
		t.Fatalf("AutoInit() error = %v", err)
	}

	// Verify RootDir is set correctly
	if result.Project.RootDir != dir {
		t.Errorf("Project.RootDir = %q, want %q", result.Project.RootDir, dir)
	}
}
