//go:build linux

package tool

import (
	"strings"
	"testing"
)

func TestIssue3140_LinuxArgvRoutesThroughShell(t *testing.T) {
	argv := ghosttyLinuxCommandArgv("make test", "")
	if len(argv) != 3 || argv[0] != "sh" || argv[1] != "-c" || argv[2] != "make test" {
		t.Fatalf("argv = %v, want [sh -c 'make test']", argv)
	}
	argv = ghosttyLinuxCommandArgv("make test", "/Volumes/new ggai")
	if !strings.HasPrefix(argv[2], "cd '/Volumes/new ggai' && ") {
		t.Fatalf("cd prefix missing/wrong: %q", argv[2])
	}

	argv = ghosttyLinuxCommandArgv("ls", "/x'y")
	want := "cd '/x'\\''y' && ls"
	if argv[2] != want {
		t.Fatalf("quote escaping wrong:\n got %q\nwant %q", argv[2], want)
	}
}
