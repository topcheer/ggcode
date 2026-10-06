package swarm

import (
	"os"
	"testing"
)

// Package-wide HOME isolation (mirrors internal/tool/zz_testmain_test.go):
// board persistence writes to ConfigDir()/swarm/teams/<id>/board.json and
// EnsureTaskManager restores from it, so a test running against the
// developer's real HOME would both leak scratch boards into ~/.ggcode and
// resurrect stale ones. A scratch HOME makes every test start empty.
func TestMain(m *testing.M) {
	if home, err := os.MkdirTemp("", "swarm-test-home-"); err == nil {
		defer os.RemoveAll(home)
		os.Setenv("HOME", home)
	}
	os.Exit(m.Run())
}
