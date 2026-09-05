package install

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIssue1606_WriteFailureSurfaced pins #1606-A: when a write was needed
// but EVERY target failed (here: all profile paths are DIRECTORIES, so
// ReadFile returns EISDIR - not IsNotExist - and the write would fail too),
// EnsureOnPath must return a non-nil error instead of a false "will be
// available in new terminals" promise.
func TestIssue1606_WriteFailureSurfaced(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("unix profile logic")
	}
	home := t.TempDir()
	// Every candidate profile path is a directory: reads fail with EISDIR
	// (not IsNotExist), writes would fail likewise.
	for _, rc := range []string{".zshrc", ".zprofile"} {
		if err := os.MkdirAll(filepath.Join(home, rc), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")

	changed, err := EnsureOnPath(filepath.Join(home, "bin"))
	if err == nil {
		t.Fatal("all-target failure must surface a non-nil error, got nil")
	}
	if changed {
		t.Fatal("changed must be false when nothing was written")
	}
}
