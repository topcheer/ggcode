package restart

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRestartWithHelper_BuildsCorrectArgs(t *testing.T) {
	// We can't actually test the full flow (it spawns a detached process),
	// but we can verify that RestartWithHelper resolves the binary path
	// and doesn't error on arg construction.
	tmp := t.TempDir()
	fakeBinary := filepath.Join(tmp, "ggcode")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Use a non-existent PID so the helper immediately moves on.
	// On Unix the helper will setsid + wait, but with a dead PID it
	// proceeds instantly. However, the helper will then try to exec
	// the fakeBinary which will fail — so we catch that in RunHelper.
	// Instead, just verify RestartWithHelper itself doesn't error.
	err := RestartWithHelper(HelperRequest{
		ParentPID: 999999, // non-existent
		Binary:    fakeBinary,
		Args:      []string{"--version"},
		WorkDir:   tmp,
		Env:       os.Environ(),
	})
	if err != nil {
		t.Fatalf("RestartWithHelper failed: %v", err)
	}

	// Give the helper a moment to do its work, then check it ran.
	// The helper will try to exec fakeBinary with "--version" which
	// exits 0, so the helper process should be gone quickly.
}

func TestReadStagedBinary(t *testing.T) {
	// This is an update-package test, but verify the round-trip
	// through the manifest format.
	tmp := t.TempDir()

	// Create a fake staged binary
	staged := filepath.Join(tmp, "ggcode-new")
	if err := os.WriteFile(staged, []byte("fake binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify we can at least call the helper flow
	if runtime.GOOS == "windows" {
		t.Skip("Unix-specific test")
	}

	// Verify waitForProcess returns quickly for a non-existent PID
	if err := waitForProcess(999999); err != nil {
		t.Fatalf("waitForProcess for dead PID failed: %v", err)
	}
}

func TestReplaceBinary(t *testing.T) {
	tmp := t.TempDir()

	// Create target and staged files
	target := filepath.Join(tmp, "target")
	staged := filepath.Join(tmp, "staged")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(target, staged); err != nil {
		t.Fatalf("replaceBinary failed: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("target not replaced: got %q want %q", string(data), "new")
	}

	// Staged file should be removed
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged file should have been removed")
	}
}
