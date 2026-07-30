package agent

import (
	"strings"
	"testing"
)

func TestCompressRepetitiveLines_ExactDuplicates(t *testing.T) {
	input := strings.Repeat("go: downloading example.com/pkg v1.0.0\n", 10)
	input += "BUILD SUCCEEDED\n"
	got := compressRepetitiveLines(input)
	if len(got) >= len(input) {
		t.Errorf("expected compression, got len=%d input=%d", len(got), len(input))
	}
	if !strings.Contains(got, "9 identical lines omitted") {
		t.Errorf("expected duplicate marker, got:\n%s", got)
	}
	if !strings.Contains(got, "BUILD SUCCEEDED") {
		t.Errorf("expected non-duplicate line preserved, got:\n%s", got)
	}
}

func TestCompressRepetitiveLines_BelowThreshold(t *testing.T) {
	input := "line1\nline1\n"
	got := compressRepetitiveLines(input)
	if got != input {
		t.Errorf("expected no compression for 2 identical lines, got:\n%s", got)
	}
}

func TestCompressRepetitiveLines_NoChange(t *testing.T) {
	input := "line1\nline2\nline3\nline4\nline5\n"
	got := compressRepetitiveLines(input)
	if got != input {
		t.Errorf("expected no change for unique short lines, got:\n%s", got)
	}
}

func TestCompressRepetitiveLines_EmptyAndSingleLine(t *testing.T) {
	if got := compressRepetitiveLines(""); got != "" {
		t.Errorf("expected empty string unchanged, got %q", got)
	}
	if got := compressRepetitiveLines("single line"); got != "single line" {
		t.Errorf("expected single line unchanged, got %q", got)
	}
}

func TestCompressRepetitiveLines_PrefixSimilar(t *testing.T) {
	// Lines with a shared 10+ char prefix that differ in suffixes.
	lines := []string{
		"Compiling module alpha (step 1 of 8)...",
		"Compiling module beta (step 2 of 8)...",
		"Compiling module gamma (step 3 of 8)...",
		"Compiling module delta (step 4 of 8)...",
		"Compiling module epsilon (step 5 of 8)...",
		"Compiling module zeta (step 6 of 8)...",
	}
	input := strings.Join(lines, "\n")
	got := compressRepetitiveLines(input)
	if len(got) >= len(input) {
		t.Errorf("expected prefix compression, got len=%d input=%d", len(got), len(input))
	}
	if !strings.Contains(got, "similar lines omitted") {
		t.Errorf("expected similarity marker, got:\n%s", got)
	}
}

func TestCompressRepetitiveLines_PrefixBelowThreshold(t *testing.T) {
	// 4 similar lines (below threshold of 5) should NOT be compressed.
	lines := []string{
		"Compiling module alpha step 1...",
		"Compiling module beta step 2...",
		"Compiling module gamma step 3...",
		"Compiling module delta step 4...",
	}
	input := strings.Join(lines, "\n")
	got := compressRepetitiveLines(input)
	if got != input {
		t.Errorf("expected no compression for 4 similar lines (threshold=5), got:\n%s", got)
	}
}

func TestCompressRepetitiveLines_PrefixTooShort(t *testing.T) {
	// Lines with short prefix (< minPrefixLen) should not trigger prefix compression.
	lines := []string{
		"short x1",
		"short x2",
		"short x3",
		"short x4",
		"short x5",
	}
	input := strings.Join(lines, "\n")
	got := compressRepetitiveLines(input)
	if got != input {
		t.Errorf("expected no compression for short-prefix lines, got:\n%s", got)
	}
}

func TestCompressRepetitiveLines_MixedContent(t *testing.T) {
	input := "Starting build...\n" +
		strings.Repeat("go: downloading dependency\n", 5) +
		"Compiling main package...\n" +
		"Error: undefined variable at line 42\n" +
		"Error: undefined variable at line 42\n" +
		"Error: undefined variable at line 42\n" +
		"DONE\n"

	got := compressRepetitiveLines(input)
	if !strings.Contains(got, "4 identical lines omitted") {
		t.Errorf("expected 4-line dup marker for downloads, got:\n%s", got)
	}
	if !strings.Contains(got, "2 identical lines omitted") {
		t.Errorf("expected 2-line dup marker for errors, got:\n%s", got)
	}
	if !strings.Contains(got, "DONE") {
		t.Errorf("expected DONE preserved, got:\n%s", got)
	}
}

func TestCompressRepetitiveLines_BlankLines(t *testing.T) {
	input := "header\n\n\n\n\n\nfooter\n"
	got := compressRepetitiveLines(input)
	if !strings.Contains(got, "blank lines omitted") {
		t.Errorf("expected blank line marker, got:\n%s", got)
	}
	if !strings.Contains(got, "header") || !strings.Contains(got, "footer") {
		t.Errorf("expected header and footer preserved, got:\n%s", got)
	}
}

func TestCompressRepetitiveLines_LargeInputSkipped(t *testing.T) {
	var sb strings.Builder
	for i := 0; i <= maxCompressInputLines; i++ {
		sb.WriteString("line\n")
	}
	input := sb.String()
	got := compressRepetitiveLines(input)
	if got != input {
		t.Errorf("expected large input returned unchanged, got len=%d input=%d", len(got), len(input))
	}
}

func TestCompressRepetitiveLines_RealWorldBuildLog(t *testing.T) {
	// Simulates a realistic go build output with repeated download lines.
	var sb strings.Builder
	sb.WriteString("# project/module\n")
	for i := 0; i < 8; i++ {
		sb.WriteString("go: downloading github.com/some/dependency v1.2.3\n")
	}
	sb.WriteString("go: found github.com/some/dependency in github.com/some/dependency v1.2.3\n")
	sb.WriteString("# project/module/cmd\n")
	sb.WriteString("BUILD SUCCESSFUL\n")

	input := sb.String()
	got := compressRepetitiveLines(input)
	if len(got) >= len(input) {
		t.Errorf("expected compression of build log, got len=%d input=%d", len(got), len(input))
	}
	if !strings.Contains(got, "BUILD SUCCESSFUL") {
		t.Errorf("expected BUILD SUCCESSFUL preserved, got:\n%s", got)
	}
	// Compression ratio should be significant.
	ratio := float64(len(got)) / float64(len(input))
	if ratio > 0.5 {
		t.Errorf("expected >50%% reduction, got %.0f%% remaining, len=%d input=%d", ratio*100, len(got), len(input))
	}
}

func TestFormatDupMarker(t *testing.T) {
	got := formatDupMarker(5, "some line here")
	if !strings.Contains(got, "5") || !strings.Contains(got, "some line here") {
		t.Errorf("unexpected marker: %s", got)
	}
}

func TestFormatDupMarker_BlankLine(t *testing.T) {
	got := formatDupMarker(3, "   ")
	if !strings.Contains(got, "blank") {
		t.Errorf("expected blank line marker, got: %s", got)
	}
}

func TestFormatDupMarker_LongSample(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := formatDupMarker(5, long)
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncation of long sample, got: %s", got)
	}
}
