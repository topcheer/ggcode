package main

// Package-wide test isolation for the #2033 real-home guard: several App
// entry points (StartShare, tunnelSnapshot, ...) resolve the config path via
// LoadConfigForWorkspace, which must never touch the real user HOME from a
// test. Individual tests may still call t.Setenv for stricter per-test
// isolation; this TestMain is the package floor.
//
// This test package was unrunnable for a stretch of the v1.3.237 cycle (the
// desktop module did not compile, #2094), so the guard landed in
// internal/config without these tests ever executing - TestMain closes that
// latent gap for every current and future test in the package.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ggcode-desktop-test-home-")
	if err != nil {
		panic("test home setup: " + err.Error())
	}
	defer os.RemoveAll(home)
	os.Setenv("HOME", home)
	// Keep XDG_CONFIG_HOME consistent with the isolated HOME so config
	// lookups stay inside the sandbox on platforms that honor XDG.
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	os.Exit(m.Run())
}
