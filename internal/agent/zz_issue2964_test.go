package agent

import (
	"strings"
	"testing"
)

// zz_issue2964_test.go - regression probes for #2964: isPoorResult's
// truncation-marker branch used bare strings.Contains for ALL non-search
// tools. read_file returns file payloads verbatim, and this repository's own
// tool-layer sources embed the bracket literals (e.g. tool/git_show.go
// builds "\n... [output truncated]" into a string) - reading three such
// files made the effectiveness tracker report "read_file has only 0%
// success rate". Per the #1211 scoping precedent, content-mirror tools only
// count markers found in the tail window (advisory append position);
// read_file itself emits no truncation marker, so this costs no true
// positives.

func TestIssue2964ContentMirrorMidContentMarkerNotPoor(t *testing.T) {
	// Mirrors reading a source file that CONSTRUCTS the marker literal
	// mid-file (git_show.go-style payload). Must be successful.
	src := strings.Repeat("package tool\n\n// ordinary prologue padding line\n", 20) +
		"\tout += \"\\n... [output truncated]\"\n" +
		strings.Repeat("// ordinary epilogue padding line\n", 40)
	if isPoorResult("read_file", src) {
		t.Fatalf("#2964: mid-content marker literal in read_file payload counted as poor result")
	}
	if isPoorResult("multi_file_read", src) {
		t.Fatalf("#2964: mid-content marker literal in multi_file_read payload counted as poor result")
	}
}

func TestIssue2964ContentMirrorTailMarkerStillPoor(t *testing.T) {
	// A layer above read_file appending the advisory at the tail (the
	// position the scoping exempts) must still count as degraded.
	body := strings.Repeat("payload line\n", 100) + "\n[output truncated]\n"
	if !isPoorResult("read_file", body) {
		t.Fatalf("#2964: tail-window truncation marker must still flag read_file result as poor")
	}
}

func TestIssue2964NonMirrorToolsUnchanged(t *testing.T) {
	// run_command (non-search, non-mirror) keeps full-content Contains
	// semantics: the truncateMiddle marker can sit mid-output (head+marker+
	// tail), so no tail window applies. Note git_show/git_blame/git_diff are
	// searchTools and never reach the marker branch - before or after.
	src := strings.Repeat("package tool\n", 5) + "\tout += \"\\n... [output truncated]\"\n"
	if !isPoorResult("run_command", src) {
		t.Fatalf("#2964: non-mirror tool marker semantics must be unchanged")
	}
}

func TestIssue2964ThreeMarkerFileReadsNoGuidance(t *testing.T) {
	// End-to-end: three successful read_file results each containing the
	// mid-content literal must NOT trip the effectiveness guidance.
	s := newToolEffTracker()
	src := strings.Repeat("padding\n", 60) + "\tout += \"\\n... [output truncated]\"\n" + strings.Repeat("padding\n", 60)
	for i := 0; i < 3; i++ {
		if msg := s.recordCall("read_file", src, false); msg != "" {
			t.Fatalf("#2964: false effectiveness guidance on successful reads: %s", msg)
		}
	}
}
