package tool

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #3191: searchFile used bufio.Scanner defaults (>64KB line → ErrTooLong →
// silent stop, all later matches missed). The fix raises the buffer cap to
// grepMaxLineLen, same form as the grep.go #3190 fix.
func TestIssue3191SearchFileLongLineSilentMiss(t *testing.T) {
	dir := t.TempDir()

	// File A: 100KB no-match line, then two matching lines.
	var sb strings.Builder
	sb.WriteString(strings.Repeat("a", 100*1024))
	sb.WriteString("\nconst needle = 42\n")
	sb.WriteString("needle again\n")
	pathA := filepath.Join(dir, "bundle.js")
	if err := os.WriteFile(pathA, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	re := regexp.MustCompile(`needle`)
	matches := searchFile(pathA, dir, re)
	if len(matches) != 2 {
		t.Fatalf("searchFile after >64KB line: got %d matches, want 2 -- silent ErrTooLong regression: %v", len(matches), matches)
	}
	// Line numbers must be the true post-long-line positions (2 and 3).
	if !strings.Contains(matches[0], "bundle.js:2:") {
		t.Errorf("first match line number wrong: %q", matches[0])
	}
	if !strings.Contains(matches[1], "bundle.js:3:") {
		t.Errorf("second match line number wrong: %q", matches[1])
	}

	// File B: match INSIDE a >64KB single line (minified bundle case).
	var sb2 strings.Builder
	sb2.WriteString(strings.Repeat("x", 70*1024) + " needle_inline " + strings.Repeat("y", 70*1024))
	sb2.WriteString("\ntail\n")
	pathB := filepath.Join(dir, "min.js")
	if err := os.WriteFile(pathB, []byte(sb2.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	matchesB := searchFile(pathB, dir, re)
	if len(matchesB) != 1 || !strings.Contains(matchesB[0], "needle_inline") {
		t.Fatalf("searchFile match inside >64KB line: got %v, want 1 match containing needle_inline", matchesB)
	}

	// Control: identical matching content without the long line.
	pathC := filepath.Join(dir, "ctrl.js")
	if err := os.WriteFile(pathC, []byte("const needle = 42\nneedle again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := len(searchFile(pathC, dir, re)); got != 2 {
		t.Fatalf("control file: got %d matches, want 2 -- test setup broken", got)
	}
}
