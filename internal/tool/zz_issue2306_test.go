package tool

// #2306: the #1698 case-6 early break made totalLines == the last shown
// line, so a truncated range reported "[Showing lines 1-2000 of ~2000]" -
// the fake total equaled the displayed count and agents stopped
// paginating. Truncated output must not fabricate a total.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue2306TruncatedRangeNoFakeTotal(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	p := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := readFileRangeStreaming(p, 1, 2000, readFileRangeOptions{defaultLimit: 2000, moreHint: "use offset to page"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "of ~2000") {
		t.Fatal("truncated range must not report the displayed count as the total")
	}
	if !strings.Contains(out, "More lines exist below") {
		t.Fatalf("truncated range must hint at continuation, got tail: %q", lastLine2306(out))
	}
	// And an untruncated read (EOF reached) must not claim more below.
	out2, err := readFileRangeStreaming(p, 2900, 2000, readFileRangeOptions{defaultLimit: 2000, moreHint: "use offset to page"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "More lines exist below") {
		t.Fatal("EOF-reaching read must not claim more lines below")
	}
}

func lastLine2306(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}
