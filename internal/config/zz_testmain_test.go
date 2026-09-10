package config

import (
	"os"
	"testing"
)

// Package-wide HOME isolation. Config tests touch ConfigDir()/external
// section loading constantly; without a scratch HOME any test that strays
// off an explicit temp path would read (or worse, Save into) the
// developer's real ~/.ggcode. Redirecting HOME here makes the whole
// package hermetic; internal/config's test guard still fails fast if
// anything resolves the real home.
func TestMain(m *testing.M) {
	if home, err := os.MkdirTemp("", "config-test-home-"); err == nil {
		defer os.RemoveAll(home)
		os.Setenv("HOME", home)
	}
	os.Exit(m.Run())
}
