package tool

import (
	"strings"
	"testing"
)

// #5274: CLI semantics preservation - two defenses against
// complete-looking-but-truncated results.
//
// (A) gh run watch false-success: the repo has ZERO code call sites of
// `gh run watch` (repo-wide grep: the only hit is docs/release-process.md,
// which now carries the mandatory post-watch final-state recheck). This
// test file pins the tool-side equivalent defense: output that ends on a
// segment header with no body is flagged instead of trusted.
//
// (B) Mid/late-segment swallowing in compound commands: the snapshot
// assembler detects the orphan-header tail shape and appends a warning.

func TestSegmentHeaderLike(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"══CI══", true},
		{"════快照══", true},
		{"────────", true},
		{"── 终态 ──", true},
		{"plain output", false},
		{"══", false}, // fewer than 3 rule chars
		{"", false},
		{"1 == 2", false},
		{"a=b", false},
	}
	for _, tc := range cases {
		if got := segmentHeaderLike(tc.line); got != tc.want {
			t.Errorf("segmentHeaderLike(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestDetectOrphanSegmentHeaders(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  int
	}{
		{"healthy body after header", []string{"══CI══", "ok"}, 0},
		{"healthy multi-section", []string{"══A══", "x", "══B══", "y"}, 0},
		{"single orphan tail", []string{"══A══", "x", "══B══"}, 1},
		{"two orphan tails", []string{"══A══", "x", "══B══", "══C══"}, 2},
		{"orphan after blanks", []string{"══A══", "x", "", "══B══", ""}, 1},
		{"no headers at all", []string{"a", "b"}, 0},
		{"empty", nil, 0},
	}
	for _, tc := range cases {
		if got := detectOrphanSegmentHeaders(tc.lines); got != tc.want {
			t.Errorf("%s: detectOrphanSegmentHeaders = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Acceptance (A)+(B): the formatted snapshot carries the truncation warning
// exactly when the tail is an orphan header; healthy output stays clean.
func TestFormatCommandJobSnapshotOrphanHeaderWarning(t *testing.T) {
	orphan := CommandJobSnapshot{
		ID: "cmd-1", Status: "completed", TotalLines: 3,
		Lines: []string{"══CI══", "all green", "══TestJob══"},
	}
	out := formatCommandJobSnapshot(orphan, true)
	if !strings.Contains(out, "[warning: output ends on 1 segment header(s) with no body") {
		t.Errorf("orphan-header tail not flagged:\n%s", out)
	}
	if !strings.Contains(out, "re-read with read_command_output/since_line") {
		t.Error("warning must tell the model how to verify")
	}

	healthy := CommandJobSnapshot{
		ID: "cmd-2", Status: "completed", TotalLines: 3,
		Lines: []string{"══CI══", "all green", "══Test══", "pass"},
	}
	out2 := formatCommandJobSnapshot(healthy, true)
	if strings.Contains(out2, "[warning: output ends on") {
		t.Errorf("healthy output falsely flagged:\n%s", out2)
	}

	// includeLines=false must not emit the warning (no lines shown, nothing
	// to verify against).
	out3 := formatCommandJobSnapshot(orphan, false)
	if strings.Contains(out3, "[warning: output ends on") {
		t.Errorf("warning emitted with includeLines=false:\n%s", out3)
	}
}

// Watch false-success shape (run 42415985612): a compound status pull that
// ends right after the section header - the tool result must look
// incomplete to the model, not like a clean finish.
func TestWatchFalseSuccessShapeFlagged(t *testing.T) {
	// Shape observed on run 42415985612: the "══TestJob══" section header
	// arrived, its body (the api status JSON) did not, exit was 0.
	snap := CommandJobSnapshot{
		ID: "cmd-3", Status: "completed", TotalLines: 4,
		Lines: []string{"date -u", "21:38:44Z 2026-11-07", "══TestJob══"},
	}
	out := formatCommandJobSnapshot(snap, true)
	if !strings.Contains(out, "tail may be truncated") {
		t.Errorf("false-success shape not flagged:\n%s", out)
	}
}
