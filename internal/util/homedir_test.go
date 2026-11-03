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

	t.Setenv("HOME", "")
	_ = ConfigDir()
	t.Setenv("HOME", origHome)
}
