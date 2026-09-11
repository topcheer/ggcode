package config

// #1837 case 2 regression: with no determinable home (HOME unset/empty and
// os.UserHomeDir failing), ConfigDir used to produce the ABSOLUTE path
// /.ggcode at the filesystem root (strings.Join with an empty first
// element), and the loader tried to read and create it - silently
// succeeding when running as root. ConfigDir must return "" (mirroring
// util.ConfigDir) and ConfigPath must degrade to a relative filename,
// never a root-absolute path.

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
