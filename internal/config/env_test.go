package config

import (
	"os"
	"testing"
)

func TestHomeDir_RespectsEnv(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	got := HomeDir()
	if got != tmp {
		t.Errorf("HomeDir() = %q, want %q", got, tmp)
	}
}

func TestHomeDir_FallbackToOS(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	os.Unsetenv("HOME")

	got := HomeDir()
	if got == "" {
		t.Error("HomeDir() returned empty string")
	}
	// Just verify it returns something reasonable (not empty)
}

func TestConfigDir_RespectsHomeOverride(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	got := ConfigDir()
	want := tmp + "/.ggcode"
	if got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}
