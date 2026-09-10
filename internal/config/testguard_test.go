package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The guard compares the live HOME against realHomeSnapshot (captured at
// process init). This package's TestMain already redirects HOME to a
// scratch dir, so by default the guard sees an isolated home and passes.
// These tests flip HOME back to the snapshot value to exercise the
// fail-fast branches.

// withUnisolatedHome temporarily restores the snapshot (real) HOME.
func withUnisolatedHome(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv("HOME")
	os.Setenv("HOME", realHomeSnapshot)
	t.Cleanup(func() {
		if had {
			os.Setenv("HOME", prev)
		} else {
			os.Unsetenv("HOME")
		}
	})
}

func TestGuardRealHomeDirPanicsWhenUnisolated(t *testing.T) {
	withUnisolatedHome(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when a test resolves the real user home")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "REAL user home") {
			t.Fatalf("unexpected panic payload: %v", r)
		}
	}()
	_ = ConfigDir()
}

func TestGuardRealHomeDirPassesWhenIsolated(t *testing.T) {
	// HOME is the package TestMain scratch dir already.
	dir := ConfigDir()
	if dir == "" {
		t.Fatal("ConfigDir() returned empty")
	}
}

func TestGuardRealHomeDirOptIn(t *testing.T) {
	withUnisolatedHome(t)
	os.Setenv("GGCODE_TEST_ALLOW_REAL_HOME", "1")
	t.Cleanup(func() { os.Unsetenv("GGCODE_TEST_ALLOW_REAL_HOME") })
	// Must not panic.
	_ = ConfigDir()
}

func TestGuardRealHomePathRejectsRealHomeTarget(t *testing.T) {
	withUnisolatedHome(t)
	target := filepath.Join(realHomeSnapshot, ".ggcode", "vendors.yaml")
	err := GuardRealHomePath(target, "Save()")
	if err == nil {
		t.Fatal("expected error writing under the real home")
	}
	if !strings.Contains(err.Error(), "REAL user home") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGuardRealHomePathAllowsTempTargets(t *testing.T) {
	withUnisolatedHome(t)
	// Explicit temp paths (tests that pass a tempdir file into Load) pass
	// even though HOME is not isolated.
	if err := GuardRealHomePath(t.TempDir()+"/config.yaml", "Save()"); err != nil {
		t.Fatalf("temp target should pass: %v", err)
	}
}

func TestGuardRealHomePathOptInAllowsRealHome(t *testing.T) {
	withUnisolatedHome(t)
	os.Setenv("GGCODE_TEST_ALLOW_REAL_HOME", "1")
	t.Cleanup(func() { os.Unsetenv("GGCODE_TEST_ALLOW_REAL_HOME") })
	target := filepath.Join(realHomeSnapshot, ".ggcode", "ggcode.yaml")
	if err := GuardRealHomePath(target, "Save()"); err != nil {
		t.Fatalf("opt-in should allow real home writes: %v", err)
	}
}

func TestSaveFailsFastIntoRealHome(t *testing.T) {
	withUnisolatedHome(t)
	cfg := DefaultConfig()
	cfg.FilePath = filepath.Join(realHomeSnapshot, ".ggcode", "ggcode.yaml")
	err := cfg.Save()
	if err == nil {
		t.Fatal("Save() into the real home must fail fast in tests")
	}
	if !strings.Contains(err.Error(), "REAL user home") {
		t.Fatalf("unexpected error: %v", err)
	}
}
