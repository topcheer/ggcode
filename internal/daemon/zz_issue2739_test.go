package daemon

import (
	"strings"
	"testing"
)

func TestIssue2739_IdentityMatchesRestartedCmdline(t *testing.T) {
	orig := testProcessCmdline
	t.Cleanup(func() { testProcessCmdline = orig })

	cmdline := strings.Join([]string{
		"/usr/local/bin/ggcode", "--config", "/home/u/.ggcode/ggcode.yaml",
		"daemon", "--follow", "--__daemonized", "--resume", "abc123",
	}, "\x00")
	testProcessCmdline = func(int) string { return cmdline }
	if !daemonIdentityMatches(1234, "/home/u/proj") {
		t.Fatalf("restarted daemon cmdline with --__daemonized must match identity")
	}

	noMarker := strings.Join([]string{
		"/usr/local/bin/ggcode", "--config", "/x.yaml", "daemon", "--follow",
	}, "\x00")
	testProcessCmdline = func(int) string { return noMarker }
	if daemonIdentityMatches(1234, "/home/u/proj") {
		t.Fatalf("cmdline without markers must not match daemon identity")
	}
}
