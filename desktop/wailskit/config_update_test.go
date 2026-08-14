package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// setupConfigTestEnv isolates HOME and loads a global config (with optional
// instance config content). The returned config has no vendor configured on
// disk; tests that need an active selection set it in memory.
func setupConfigTestEnv(t *testing.T, instanceContent string) (globalPath, workspace string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	tmpDir := t.TempDir()
	globalPath = filepath.Join(tmpDir, "ggcode.yaml")
	if err := os.WriteFile(globalPath, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	workspace = t.TempDir()
	if instanceContent != "" {
		instDir := config.InstanceDir(workspace)
		if err := os.MkdirAll(instDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte(instanceContent), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	SetConfig(cfg)
	return globalPath, workspace
}

// TestUpdateConfigEmptyVendorEndpointGuard (#284): submitting empty
// vendor/endpoint strings (e.g. frontend cold-start race where component
// state hasn't loaded yet) must not clobber the active selection.
func TestUpdateConfigEmptyVendorEndpointGuard(t *testing.T) {
	_, _ = setupConfigTestEnv(t, "")

	// Simulate an already-selected vendor/endpoint in the live config.
	cfg := GetGlobalConfig()
	cfg.Vendor = "testv"
	cfg.Endpoint = "main"
	cfg.Model = "m1"
	cfg.Vendors = map[string]config.VendorConfig{
		"testv":  {Endpoints: map[string]config.EndpointConfig{"main": {BaseURL: "https://example.com", Protocol: "openai"}}},
		"otherv": {Endpoints: map[string]config.EndpointConfig{"alt": {BaseURL: "https://other.example.com", Protocol: "openai"}}},
	}

	// Empty vendor/endpoint submitted alongside a valid update.
	if err := UpdateConfig(map[string]interface{}{
		"vendor":   "",
		"endpoint": "",
		"model":    "newmodel",
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	cfg = GetGlobalConfig()
	if cfg.Vendor != "testv" {
		t.Errorf("cfg.Vendor = %q, want unchanged %q", cfg.Vendor, "testv")
	}
	if cfg.Endpoint != "main" {
		t.Errorf("cfg.Endpoint = %q, want unchanged %q", cfg.Endpoint, "main")
	}
	if cfg.Model != "newmodel" {
		t.Errorf("cfg.Model = %q, want %q (non-empty values must still apply)", cfg.Model, "newmodel")
	}

	// Non-empty vendor/endpoint still update normally.
	if err := UpdateConfig(map[string]interface{}{"vendor": "otherv", "endpoint": "alt"}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	cfg = GetGlobalConfig()
	if cfg.Vendor != "otherv" || cfg.Endpoint != "alt" {
		t.Errorf("vendor/endpoint update failed: %q/%q", cfg.Vendor, cfg.Endpoint)
	}
}

// TestSetEndpointLimitsKeepsGlobalSaveTarget (#282): after saving endpoint
// limits, a preference save must still write to the GLOBAL config file, not
// be silently redirected to the instance file.
func TestSetEndpointLimitsKeepsGlobalSaveTarget(t *testing.T) {
	globalPath, workspace := setupConfigTestEnv(t, "")

	cfg := GetGlobalConfig()
	cfg.Vendors = map[string]config.VendorConfig{
		"testv": {Endpoints: map[string]config.EndpointConfig{
			"main": {BaseURL: "https://example.com"},
		}},
	}
	if err := SetEndpointLimits("testv", "main", 200000, 16384); err != nil {
		t.Fatalf("SetEndpointLimits: %v", err)
	}

	if got := GetGlobalConfig().GetSaveScope(); got == "instance" {
		t.Errorf("saveScope = %q after SetEndpointLimits; sticky scope was dirtied and preference saves will be redirected (#282)", got)
	}

	// A preference save must land in the global file.
	if err := SaveDefaultMode("auto"); err != nil {
		t.Fatalf("SaveDefaultMode: %v", err)
	}
	globData, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("reading global config: %v", err)
	}
	if !strings.Contains(string(globData), "default_mode") {
		t.Errorf("default_mode missing from global config after limits save; got:\n%s", globData)
	}

	// And the limits themselves must be persisted in the instance file.
	instPath := filepath.Join(config.InstanceDir(workspace), "ggcode.yaml")
	instData, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	if !strings.Contains(string(instData), "context_window") {
		t.Errorf("instance config missing limits delta; got:\n%s", instData)
	}
}

// TestUpdateConfigInstanceFieldWriteBack (#282): modifying an instance-sourced
// field via UpdateConfig must persist to the instance file — simulated restart
// by reloading the config must observe the new value.
func TestUpdateConfigInstanceFieldWriteBack(t *testing.T) {
	globalPath, workspace := setupConfigTestEnv(t, "default_mode: auto\n")

	cfg := GetGlobalConfig()
	if cfg.DefaultMode != "auto" {
		t.Fatalf("instance default_mode not merged: %q", cfg.DefaultMode)
	}

	if err := UpdateConfig(map[string]interface{}{"defaultMode": "plan"}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	// Simulated restart: reload from disk and verify the value survived.
	after, err := config.LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance after update: %v", err)
	}
	if after.DefaultMode != "plan" {
		t.Errorf("default_mode after reload = %q, want %q (instance write-back missing, change lost on restart)", after.DefaultMode, "plan")
	}

	// The global file must NOT carry the stripped instance field.
	globData, _ := os.ReadFile(globalPath)
	if strings.Contains(string(globData), "default_mode: plan") {
		t.Errorf("instance-sourced default_mode leaked into global config file:\n%s", globData)
	}
}
