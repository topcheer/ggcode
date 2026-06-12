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
	// On macOS, HOME is the primary source and os.UserHomeDir may also
	// rely on it. An empty result is acceptable when HOME is unset.
	// The important thing is that it doesn't panic or return a hard-coded
	// path like "/root" which is wrong on most systems.
	if got == "/root" {
		t.Error("HomeDir() should not return /root as fallback")
	}
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
