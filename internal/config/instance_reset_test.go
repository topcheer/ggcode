package config

import (
	"os"
	"path/filepath"
	"testing"
)

// #2106 probes: instance-scope partial reset must actually remove the
// overridden key from instance.yaml instead of letting the deep-merge keep
// it (and MergeInstance resurrect it on the next load).

// instanceTestEnv isolates HOME once per test so InstanceDir stays stable
// across multiple saves.
func instanceTestEnv(t *testing.T, globalPath string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	_ = globalPath
}

// saveInstanceVia loads the global+instance config and saves it back.
func saveInstanceVia(t *testing.T, globalPath, workspace string, mutate func(c *Config)) string {
	t.Helper()
	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}
	mutate(cfg)
	if err := cfg.SaveInstanceScoped(workspace); err != nil {
		t.Fatalf("SaveInstanceScoped: %v", err)
	}
	instPath := filepath.Join(InstanceDir(workspace), "ggcode.yaml")
	return instPath
}

func TestInstancePartialResetScalarRemovedFromDisk(t *testing.T) {
	gDir, ws := t.TempDir(), t.TempDir()
	globalPath := filepath.Join(gDir, "ggcode.yaml")
	if err := os.WriteFile(globalPath, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	instanceTestEnv(t, globalPath)

	// Step 1: set two overrides - default_mode=plan and max_iterations=50.
	// (max_iterations is a plain int with no default-fill path; language and
	// endpoint both have dedicated load/default chains that shadow the
	// instance merge - see the #734 probe notes.)
	instPath := saveInstanceVia(t, globalPath, ws, func(c *Config) {
		c.DefaultMode = "plan"
		c.MaxIterations = 50
	})
	data, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("instance file missing after first save: %v", err)
	}
	if !contains(string(data), "default_mode") || !contains(string(data), "max_iterations") {
		t.Fatalf("expected both overrides on disk, got:\n%s", data)
	}

	// Step 2: reset ONLY default_mode back to global ("") - keep max_iterations.
	saveInstanceVia(t, globalPath, ws, func(c *Config) {
		c.DefaultMode = "" // reset
		c.MaxIterations = 50
	})

	// Probe: default_mode must be gone from disk; max_iterations must survive.
	data, err = os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("instance file missing after partial reset: %v", err)
	}
	if contains(string(data), "default_mode") {
		t.Fatalf("#2106 regression: default_mode resurrected on disk:\n%s", data)
	}
	if !contains(string(data), "max_iterations") {
		t.Fatalf("unrelated override max_iterations lost:\n%s", data)
	}

	// Probe: reload + merge must not resurrect the reset value.
	cfg, err := LoadWithInstance(globalPath, ws)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.DefaultMode == "plan" {
		t.Fatalf("#2106 regression: DefaultMode resurrected as %q after reload", cfg.DefaultMode)
	}
	if cfg.MaxIterations != 50 {
		t.Fatalf("max_iterations override lost after reload: %d", cfg.MaxIterations)
	}
}

func TestInstanceFullResetRemovesInstanceFile(t *testing.T) {
	gDir, ws := t.TempDir(), t.TempDir()
	globalPath := filepath.Join(gDir, "ggcode.yaml")
	if err := os.WriteFile(globalPath, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	instanceTestEnv(t, globalPath)

	// Override max_iterations and default_mode, then reset BOTH.
	instPath := saveInstanceVia(t, globalPath, ws, func(c *Config) {
		c.MaxIterations = 50
		c.DefaultMode = "plan"
	})
	if _, err := os.Stat(instPath); err != nil {
		t.Fatalf("instance file should exist after overrides: %v", err)
	}

	saveInstanceVia(t, globalPath, ws, func(c *Config) {
		c.MaxIterations = 0 // reset
		c.DefaultMode = ""  // reset
	})

	// Probe: with every override reset the instance file must be REMOVED
	// (previously it survived with stale keys; only the empty-delta path
	// ever removed it).
	if _, err := os.Stat(instPath); !os.IsNotExist(err) {
		t.Fatalf("#2106: fully-reset instance file still exists (err=%v)", err)
	}
}

func TestInstanceResetKeepsUnmanagedKeys(t *testing.T) {
	gDir, ws := t.TempDir(), t.TempDir()
	globalPath := filepath.Join(gDir, "ggcode.yaml")
	if err := os.WriteFile(globalPath, []byte("language: en\n"), 0644); err != nil {
		t.Fatal(err)
	}
	instanceTestEnv(t, globalPath)

	// An existing instance file carrying a key OUTSIDE the managed reset
	// family must survive a save that resets a managed key (deletion is
	// scoped to the diff-managed scalar family).
	instDir := InstanceDir(ws)
	if err := os.MkdirAll(instDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("default_mode: plan\nui:\n  sidebar_visible: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	instPath := saveInstanceVia(t, globalPath, ws, func(c *Config) {
		c.DefaultMode = "" // reset
	})
	data, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("instance file should survive (ui remains): %v", err)
	}
	if contains(string(data), "default_mode") {
		t.Fatalf("managed reset key survived:\n%s", data)
	}
	if !contains(string(data), "sidebar_visible") {
		t.Fatalf("unmanaged ui block was incorrectly deleted:\n%s", data)
	}
}
