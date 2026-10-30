package tool

// #4548 companion tests - scanForExternalChanges 50k truncation must exempt
// the deletion detection: a truncated walk has not seen every directory, so
// absence from found is not evidence of deletion. Before the fix, indexed
// files beyond the cut were marked deleted and persisted, then re-added the
// next tick - index membership oscillated add/remove across the 50k boundary
// every 5 minutes.
//
// Reproducing >50k code files in-process is impractical for a unit test, so
// the invariants are pinned structurally (source scan): the SkipAll branch
// must set the truncation flag, and the deletion loop must be gated on it.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4548_TruncationExemptsDeletionDetection(t *testing.T) {
	src, err := os.ReadFile("code_index_manager.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(src)

	scanStart := strings.Index(s, "func (m *CodeIndexManager) scanForExternalChanges(")
	if scanStart < 0 {
		t.Fatal("scanForExternalChanges not found")
	}
	scanEnd := strings.Index(s[scanStart:], "\nfunc ")
	if scanEnd < 0 {
		t.Fatal("scanForExternalChanges end not found")
	}
	body := s[scanStart : scanStart+scanEnd]

	// The SkipAll branch must record truncation before returning.
	if !strings.Contains(body, "walkTruncated = true") {
		t.Fatal("#4548: the file-cap SkipAll branch must set walkTruncated - otherwise the walk's early stop is indistinguishable from deletions beyond the cut")
	}

	// The deletion loop must be gated: "dirtyFiles[p] = -1" only reachable
	// when the walk completed.
	delim := "// Check for deleted files"
	idx := strings.Index(body, delim)
	if idx < 0 {
		t.Fatal("deletion-detection comment not found")
	}
	tail := body[idx:]
	if !strings.Contains(tail, "if walkTruncated") {
		t.Fatal("#4548: deletion detection must be skipped when the walk was truncated (found set is partial; absence is not deletion)")
	}
	// And the skip must actually guard the -1 write, not sit after it.
	if strings.Index(tail, "walkTruncated") > strings.Index(tail, "-1") {
		t.Fatalf("#4548: truncation check must precede the -1 deletion marking, got flag check after write: %q", tail[:120])
	}
}
