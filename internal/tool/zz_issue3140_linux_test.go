//go:build linux

package tool

import (
	"strings"
	"testing"
)

// V2 probes (construction-level): the -e argv must route through a shell
// so argumented commands survive execvp semantics, with an escaped cd
// prefix when working_dir is set.
func TestIssue3140_LinuxArgvRoutesThroughShell(t *testing.T) {
	argv := ghosttyLinuxCommandArgv("make test", "")
	if len(argv) != 3 || argv[0] != "sh" || argv[1] != "-c" || argv[2] != "make test" {
		t.Fatalf("argv = %v, want [sh -c 'make test']", argv)
	}
	argv = ghosttyLinuxCommandArgv("make test", "/Volumes/new ggai")
	if !strings.HasPrefix(argv[2], "cd '/Volumes/new ggai' && ") {
		t.Fatalf("cd prefix missing/wrong: %q", argv[2])
	}
	// Hostile working_dir with an embedded quote must not escape the
	// single-quoted cd context: the escaped-quote dance (close-quote,
	// escaped-quote, reopen) keeps the payload inert.
	argv = ghosttyLinuxCommandArgv("ls", "/x'y")
	want := "cd '/x'\\''y' && ls" // close-quote + escaped-quote + reopen
	if argv[2] != want {
		t.Fatalf("quote escaping wrong:\n got %q\nwant %q", argv[2], want)
	}
}
