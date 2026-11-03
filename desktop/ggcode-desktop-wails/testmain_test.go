package main

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

	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	os.Exit(m.Run())
}
