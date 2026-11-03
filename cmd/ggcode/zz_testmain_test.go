package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if home, err := os.MkdirTemp("", "ggcode-cmd-test-home-"); err == nil {
		defer os.RemoveAll(home)
		os.Setenv("HOME", home)
	}
	os.Exit(m.Run())
}
