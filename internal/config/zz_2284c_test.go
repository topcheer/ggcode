package config

// #2284-C: an instance file that EXPLICITLY contains a zero-valued key
// ("max_iterations: 0") is a deliberate clear-to-default. The old merge
// gate (global==0 && instance!=0) resurrected the cleared global value:
// set 80 -> clear to 0 -> SaveInstance -> reload -> 80 back.

import (
	"os"
	"path/filepath"
	"testing"
)

func Test2284C_ExplicitZeroSurvivesReload(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", ws)
	t.Setenv("USERPROFILE", ws)
	instDir := InstanceDir(ws)
	if instDir == "" {
		t.Fatal("no instance dir")
	}
	if err := os.MkdirAll(instDir, 0o700); err != nil {
		t.Fatal(err)
	}
	instPath := InstanceConfigPath(ws)

	// Step 1: instance explicitly sets max_iterations: 80.
	if err := os.WriteFile(instPath, []byte("max_iterations: 80\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadWithInstance(filepath.Join(ws, ".ggcode", "ggcode.yaml"), ws)
	if cfg == nil || cfg.MaxIterations != 80 {
		t.Fatalf("step1: explicit 80 must apply, got %+v", cfg)
	}

	// Step 2: clear to 0 in the instance file (the deliberate reset).
	if err := os.WriteFile(instPath, []byte("max_iterations: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg2, _ := LoadWithInstance(filepath.Join(ws, ".ggcode", "ggcode.yaml"), ws)
	if cfg2 == nil {
		t.Fatal("step2: load failed")
	}
	if cfg2.MaxIterations != 0 {
		t.Fatalf("step2: explicit 0 must survive (the #2284-C resurrection), got %d", cfg2.MaxIterations)
	}

	// Step 3: absent key keeps the default value passing through (regression
	// guard on the non-explicit path).
	if err := os.WriteFile(instPath, []byte("language: zh-CN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg3, _ := LoadWithInstance(filepath.Join(ws, ".ggcode", "ggcode.yaml"), ws)
	if cfg3 == nil {
		t.Fatal("step3: load failed")
	}
	if cfg3.MaxIterations != DefaultConfig().MaxIterations {
		t.Fatalf("step3: absent key falls back to the default, got %d want %d", cfg3.MaxIterations, DefaultConfig().MaxIterations)
	}
	if cfg3.Language != "zh-CN" {
		t.Fatalf("step3: unrelated explicit key still applies, got %q", cfg3.Language)
	}
}
