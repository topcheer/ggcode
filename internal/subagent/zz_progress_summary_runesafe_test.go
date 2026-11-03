package subagent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

func TestFormatProgressSummaryRuneSafeTruncation(t *testing.T) {

	long := strings.Repeat("进度", 50)
	snap := Snapshot{Status: StatusRunning, ProgressSummary: long}
	got := formatProgressSummary(snap)
	if !utf8.ValidString(got) {
		t.Fatalf("summary must be valid UTF-8 after truncation, got invalid string: %q", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "...") {
		t.Fatalf("truncated summary must end with ..., got %q", got)
	}

	lines := strings.SplitN(got, "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("summary line missing: %q", got)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(lines[1])); n > 80 {
		t.Fatalf("summary must be at most 80 runes, got %d", n)
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[1]), "...") {
		t.Fatalf("truncated summary must end with ..., got %q", lines[1])
	}
	if !utf8.ValidString(lines[1]) {
		t.Fatalf("summary segment must be valid UTF-8: %q", lines[1])
	}

	short := strings.Repeat("进度", 10)
	snap2 := Snapshot{Status: StatusRunning, ProgressSummary: short}
	if !strings.Contains(formatProgressSummary(snap2), short) {
		t.Fatal("a summary that fits must not be truncated")
	}

	if got := util.Truncate(strings.Repeat("a", 81), 80); got != strings.Repeat("a", 77)+"..." {
		t.Fatalf("ASCII parity broken: %q", got)
	}
}
