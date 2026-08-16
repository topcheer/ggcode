package diff

import (
	"strings"
	"testing"
)

// #555: trailing-newline-only changes must render consistently across
// UnifiedDiff / CountChanges / HasChanges.

func TestIssue555EOFNewlineOnlyDiffRenders(t *testing.T) {
	got := UnifiedDiff("a", "a\n", 3)
	if got == "" {
		t.Fatal("UnifiedDiff(\"a\",\"a\\n\") must not be empty when HasChanges=true")
	}
	if !strings.Contains(got, "- a") {
		t.Errorf("expected \"- a\" deletion line, got:\n%s", got)
	}
	if !strings.Contains(got, "+ a") {
		t.Errorf("expected \"+ a\" addition line, got:\n%s", got)
	}
	// The side lacking the trailing newline gets the standard marker.
	if !strings.Contains(got, "\\ No newline at end of file") {
		t.Errorf("expected no-newline marker, got:\n%s", got)
	}
	if !strings.Contains(got, "@@") {
		t.Errorf("expected a hunk header, got:\n%s", got)
	}
}

func TestIssue555EOFNewlineReverseDirection(t *testing.T) {
	// "a\n" -> "a": the OLD side had the newline, the NEW side lacks it.
	got := UnifiedDiff("a\n", "a", 3)
	if !strings.Contains(got, "\\ No newline at end of file") {
		t.Errorf("expected no-newline marker after +a, got:\n%s", got)
	}
}

func TestIssue555CountChangesEOFNewlineOnly(t *testing.T) {
	a, d := CountChanges("a", "a\n")
	if a != 1 || d != 1 {
		t.Errorf("CountChanges(\"a\",\"a\\n\") = +%d -%d, want +1 -1 (must match HasChanges=true)", a, d)
	}
	// Identical content with identical EOF-newline state stays zero.
	a, d = CountChanges("a", "a")
	if a != 0 || d != 0 {
		t.Errorf("CountChanges(\"a\",\"a\") = +%d -%d, want +0 -0", a, d)
	}
}

func TestIssue555ThreeFunctionsConsistent(t *testing.T) {
	cases := []struct{ old, new string }{
		{"a", "a\n"},
		{"a\n", "a"},
		{"a\nb", "a\nb\n"},
		{"", "\n"},
		{"a\nb\nc", "a\nb\nc\n"},
		{"same\n", "same\n"},
	}
	for _, c := range cases {
		has := HasChanges(c.old, c.new)
		adds, dels := CountChanges(c.old, c.new)
		diffText := UnifiedDiff(c.old, c.new, 3)
		if has {
			if adds == 0 && dels == 0 {
				t.Errorf("HasChanges=true but CountChanges=+0 -0 for %q -> %q", c.old, c.new)
			}
			if diffText == "" {
				t.Errorf("HasChanges=true but UnifiedDiff empty for %q -> %q", c.old, c.new)
			}
		} else {
			if adds != 0 || dels != 0 || diffText != "" {
				t.Errorf("HasChanges=false but stats/diff disagree for %q -> %q (+%d -%d, %q)",
					c.old, c.new, adds, dels, diffText)
			}
		}
	}
}

func TestIssue555StatsStillCountRealChanges(t *testing.T) {
	// Regression guard: real content changes must not be inflated by the
	// EOF-newline handling.
	a, d := CountChanges("hello\nworld", "hello\nworld\nfoo")
	if a != 1 || d != 0 {
		t.Errorf("CountChanges append = +%d -%d, want +1 -0", a, d)
	}
	a, d = CountChanges("hello\nworld\nfoo", "hello\nworld")
	if a != 0 || d != 1 {
		t.Errorf("CountChanges delete = +%d -%d, want +0 -1", a, d)
	}
}

func TestIssue555LargeFileEOFNewlineOnlyFallback(t *testing.T) {
	// >5000 combined lines triggers fastDiffFallback; EOF-newline-only change
	// must still render there too.
	oldText := strings.Repeat("x\n", 3000)
	newText := strings.Repeat("x\n", 2999) + "x"
	if UnifiedDiff(oldText, newText, 3) == "" {
		t.Error("large-file fallback must render EOF-newline-only change")
	}
	if a, d := CountChanges(oldText, newText); a != 1 || d != 1 {
		t.Errorf("large-file CountChanges = +%d -%d, want +1 -1", a, d)
	}
}

func TestIssue555MixedChangeKeepsNoNewlineMarker(t *testing.T) {
	// Content change in a file that (still) lacks a trailing newline: the
	// marker must appear after the final + line.
	got := UnifiedDiff("a\nb", "a\nx", 3)
	if !strings.Contains(got, "+ x") {
		t.Errorf("expected +x in:\n%s", got)
	}
	if !strings.Contains(got, "\\ No newline at end of file") {
		t.Errorf("expected no-newline marker for changed file ending without newline, got:\n%s", got)
	}
}
