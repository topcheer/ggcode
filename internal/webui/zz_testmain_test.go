package webui

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if home, err := os.MkdirTemp("", "webui-test-home-"); err == nil {
		defer os.RemoveAll(home)
		os.Setenv("HOME", home)
	}
	os.Exit(m.Run())
}
