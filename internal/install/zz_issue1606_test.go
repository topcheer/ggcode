package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIssue1606_WriteFailureSurfaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix profile logic")
	}
	home := t.TempDir()

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
