package tool

import (
	"os"
	"testing"
)

// Package-wide HOME isolation: these tests load real config paths
// (config.ConfigDir and friends); without a scratch HOME a stray Save
// would leak fixtures into the developer's real ~/.ggcode. The
// internal/config guard fails fast if anything still resolves it.
func TestMain(m *testing.M) {
	if home, err := os.MkdirTemp("", "tool-test-home-"); err == nil {
		defer os.RemoveAll(home)
		os.Setenv("HOME", home)
	}
	os.Exit(m.Run())
}
