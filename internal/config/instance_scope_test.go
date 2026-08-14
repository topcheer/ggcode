package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGlobalConfig writes a minimal valid global config and returns its path.
func writeGlobalConfig(t *testing.T, tmpDir string) string {
	t.Helper()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	if err := os.WriteFile(globalPath, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return globalPath
}

// TestSaveInstanceScopedDoesNotChangeStickyScope verifies that the non-sticky
// instance save (#282) persists to the instance file without redirecting
// subsequent scope-aware saves: after a limits-style SaveInstanceScoped call,
// SaveDefaultModePreference must still write to the GLOBAL config file.
func TestSaveInstanceScopedDoesNotChangeStickyScope(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := writeGlobalConfig(t, tmpDir)
	workspace := t.TempDir()

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	if cfg.GetSaveScope() == "instance" {
		t.Fatal("fresh config should not start in instance scope")
	}

	// Simulate a SetEndpointLimits-style mutation: vendors differ from the
	// global snapshot, then a non-sticky instance save.
	cfg.Vendors = map[string]VendorConfig{
		"testv": {Endpoints: map[string]EndpointConfig{
			"main": {ContextWindow: 100000, MaxTokens: 8192},
		}},
	}
	if err := cfg.SaveInstanceScoped(workspace); err != nil {
		t.Fatalf("SaveInstanceScoped: %v", err)
	}

	// Sticky scope must be unchanged...
	if got := cfg.GetSaveScope(); got == "instance" {
		t.Errorf("SaveInstanceScoped changed sticky saveScope to %q; subsequent preference saves would be redirected to the instance file", got)
	}

	// ...and the limits delta must have landed in the instance file.
	instData, err := os.ReadFile(filepath.Join(InstanceDir(workspace), "ggcode.yaml"))
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	if !strings.Contains(string(instData), "context_window") {
		t.Errorf("instance config missing vendors limits delta; got:\n%s", instData)
	}

	// A preference save after the limits save must go to the GLOBAL file.
	if err := cfg.SaveDefaultModePreference("plan"); err != nil {
		t.Fatalf("SaveDefaultModePreference: %v", err)
	}
	globData, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("reading global config: %v", err)
	}
	if !strings.Contains(string(globData), "default_mode: plan") {
		t.Errorf("default_mode should be written to the global file; got:\n%s", globData)
	}
	instAfter, _ := os.ReadFile(filepath.Join(InstanceDir(workspace), "ggcode.yaml"))
	if strings.Contains(string(instAfter), "default_mode") {
		t.Errorf("default_mode leaked into instance file; got:\n%s", instAfter)
	}
}

// TestInstanceFields verifies the accessor used by desktop UpdateConfig to
// detect instance-sourced fields that Save() strips from the global write.
func TestInstanceFields(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := writeGlobalConfig(t, tmpDir)
	workspace := t.TempDir()

	instDir := InstanceDir(workspace)
	if err := os.MkdirAll(instDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte("default_mode: auto\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	if cfg.DefaultMode != "auto" {
		t.Fatalf("instance merge failed: default_mode=%q", cfg.DefaultMode)
	}

	fields := cfg.InstanceFields()
	joined := strings.Join(fields, ",")
	if !strings.Contains(joined, "default_mode") {
		t.Errorf("InstanceFields() = %v, want %q present", fields, "default_mode")
	}
	if strings.Contains(joined, "language") {
		t.Errorf("InstanceFields() = %v, should not contain global-only field language", fields)
	}
}
