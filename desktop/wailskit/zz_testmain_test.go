package wailskit

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Setenv("GGCODE_WAILSKIT_DISABLE_A2A", "1")
	if home, err := os.MkdirTemp("", "wailskit-test-home-"); err == nil {
		defer os.RemoveAll(home)
		os.Setenv("HOME", home)
	}
	code := m.Run()
	os.Unsetenv("GGCODE_WAILSKIT_DISABLE_A2A")
	os.Exit(code)
}
