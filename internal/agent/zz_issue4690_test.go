package agent

import (
	"strings"
	"testing"
)

// #4690: truncTail must keep the END of a long string - unmarked-failure
// signals (signal: killed, segfault, panic) live at the output's end, and
// the #4607 fallback snippet is labeled "output tail".
func TestIssue4690_TruncTailKeepsEnd(t *testing.T) {
	long := strings.Repeat("x", 3000) + "signal: killed"
	got := truncTail(long, 2000)
	if len(got) != 2000 {
		t.Fatalf("truncTail length = %d, want 2000 (#4690)", len(got))
	}
	if !strings.HasSuffix(got, "signal: killed") {
		t.Fatal("truncTail must keep the END - the diagnostic signal (#4690)")
	}
	if !strings.HasPrefix(got, "...") {
		t.Fatal("truncTail marks the elided head with ... (#4690)")
	}
	// Short input passes through unchanged.
	if got := truncTail("short", 2000); got != "short" {
		t.Fatalf("short input must pass through, got %q (#4690)", got)
	}
}
