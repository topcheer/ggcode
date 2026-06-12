package acp

import (
	"strings"
	"testing"
)

func TestActivityTrailKeepsRecentEntries(t *testing.T) {
	var trail activityTrail
	for i := 0; i < activityTrailLimit+3; i++ {
		trail.Add(strings.Repeat(" ", i%3) + "entry-" + strings.Repeat("x", activityTrailLineLimit))
	}

	snapshot := trail.Snapshot()
	lines := strings.Split(snapshot, "\n")
	if len(lines) != activityTrailLimit {
		t.Fatalf("expected %d lines, got %d", activityTrailLimit, len(lines))
	}
	if strings.Contains(snapshot, "entry-0") {
		t.Fatalf("expected oldest entries to be trimmed, got %q", snapshot)
	}
	if len([]rune(lines[0])) > activityTrailLineLimit {
		t.Fatalf("expected compacted line length <= %d, got %d", activityTrailLineLimit, len([]rune(lines[0])))
	}
	if strings.Contains(lines[0], "  ") {
		t.Fatalf("expected whitespace to be compacted, got %q", lines[0])
	}
}
