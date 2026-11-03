package config

import (
	"strings"
	"testing"
)

func TestConfigDirEmptyHomeReturnsEmpty(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if got := ConfigDir(); got != "" {
		t.Fatalf("ConfigDir() = %q, want \"\" when no home is determinable", got)
	}
}

func TestConfigPathEmptyHomeNeverRootAbsolute(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	got := ConfigPath()
	if strings.HasPrefix(got, "/") || strings.HasPrefix(got, `\`) {
		t.Fatalf("ConfigPath() = %q: root-absolute config path from empty home (was /.ggcode/ggcode.yaml)", got)
	}
	if !strings.HasSuffix(got, "ggcode.yaml") {
		t.Fatalf("ConfigPath() = %q, want it to keep the file name", got)
	}
}
