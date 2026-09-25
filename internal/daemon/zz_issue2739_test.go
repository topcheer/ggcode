package daemon

// #2739 regression (identity side): a daemon restarted via exec-restart
// carries --__daemonized in its rebuilt argv; daemonIdentityMatches must
// recognize it (and must NOT recognize a marker-less cmdline - that is the
// PID-reuse false positive that deletes the PID file and admits a second
// daemon).

import (
	"strings"
	"testing"
)

func TestIssue2739_IdentityMatchesRestartedCmdline(t *testing.T) {
	orig := testProcessCmdline
	t.Cleanup(func() { testProcessCmdline = orig })

	// Simulated /proc/<pid>/cmdline after exec-restart with the fix.
	cmdline := strings.Join([]string{
		"/usr/local/bin/ggcode", "--config", "/home/u/.ggcode/ggcode.yaml",
		"daemon", "--follow", "--__daemonized", "--resume", "abc123",
	}, "\x00")
	testProcessCmdline = func(int) string { return cmdline }
	if !daemonIdentityMatches(1234, "/home/u/proj") {
		t.Fatalf("restarted daemon cmdline with --__daemonized must match identity")
	}

	// Without the marker (the pre-fix bug): identity must NOT match, so a
	// live restarted daemon was misjudged as PID reuse.
	noMarker := strings.Join([]string{
		"/usr/local/bin/ggcode", "--config", "/x.yaml", "daemon", "--follow",
	}, "\x00")
	testProcessCmdline = func(int) string { return noMarker }
	if daemonIdentityMatches(1234, "/home/u/proj") {
		t.Fatalf("cmdline without markers must not match daemon identity")
	}
}
