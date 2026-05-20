package util

import (
	"os"
	"testing"
)

func TestHomeDir_RespectsEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	home := HomeDir()
	if home != tmpDir {
		t.Errorf("expected %q, got %q", tmpDir, home)
	}
}

func TestHomeDir_EmptyEnv(t *testing.T) {
	t.Setenv("HOME", "")
	// On most systems os.UserHomeDir will still return a valid home.
	// We just verify it doesn't crash and returns something.
	home := HomeDir()
	_ = home
}

func TestConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	cfg := ConfigDir()
	expected := tmpDir + "/.ggcode"
	if cfg != expected {
		t.Errorf("expected %q, got %q", expected, cfg)
	}
}

func TestConfigDir_EmptyHome(t *testing.T) {
	origHome := os.Getenv("HOME")
	// We can't easily force os.UserHomeDir to fail, but we can test
	// the HOME="" path returns something reasonable.
	t.Setenv("HOME", "")
	_ = ConfigDir() // should not panic
	t.Setenv("HOME", origHome)
}
