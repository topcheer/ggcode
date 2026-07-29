package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestGuardToolOutput_NoTruncationLowFill(t *testing.T) {
	content := strings.Repeat("x", 50000) // 50KB
	result := guardToolOutput(content, 0.30)
	if len(result) != len(content) {
		t.Errorf("at 30%% fill, should not truncate: got %d bytes, want %d", len(result), len(content))
	}
}

func TestGuardToolOutput_NoTruncationSmallOutput(t *testing.T) {
	content := "small output"
	result := guardToolOutput(content, 0.80)
	if result != content {
		t.Errorf("small output should not be truncated even at high fill")
	}
}

func TestGuardToolOutput_ModerateFill(t *testing.T) {
	content := strings.Repeat("x", 60000) // 60KB > 40KB limit at moderate fill
	result := guardToolOutput(content, 0.55)
	if len(result) >= len(content) {
		t.Errorf("at 55%% fill, 60KB should be truncated: got %d bytes", len(result))
	}
	if len(result) > 45000 { // limit + some headroom for marker
		t.Errorf("truncated result too large: got %d bytes, expected ~40KB", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("truncated result should contain truncation marker")
	}
}

func TestGuardToolOutput_HighFill(t *testing.T) {
	content := strings.Repeat("x", 30000) // 30KB > 20KB limit at high fill
	result := guardToolOutput(content, 0.70)
	if len(result) >= len(content) {
		t.Errorf("at 70%% fill, 30KB should be truncated")
	}
	if len(result) > 25000 {
		t.Errorf("truncated result too large at high fill: got %d", len(result))
	}
}

func TestGuardToolOutput_CriticalFill(t *testing.T) {
	content := strings.Repeat("x", 15000) // 15KB > 10KB limit at critical fill
	result := guardToolOutput(content, 0.85)
	if len(result) >= len(content) {
		t.Errorf("at 85%% fill, 15KB should be truncated")
	}
	if len(result) > 12000 {
		t.Errorf("truncated result too large at critical fill: got %d", len(result))
	}
}

func TestGuardToolOutput_ThresholdBoundary(t *testing.T) {
	// Exactly at moderate threshold (40KB = 40960 bytes) — should not truncate yet
	content := strings.Repeat("x", outputLimitModerate)
	result := guardToolOutput(content, 0.50)
	if len(result) != len(content) {
		t.Errorf("exactly at limit should not truncate: got %d, want %d", len(result), len(content))
	}

	// One byte over at moderate fill
	content = strings.Repeat("x", outputLimitModerate+1)
	result = guardToolOutput(content, 0.50)
	if len(result) >= len(content) {
		t.Errorf("one byte over limit should truncate")
	}
}

func TestGuardToolOutput_PreservesHeadAndTail(t *testing.T) {
	// Create content where head and tail are distinguishable
	head := "HEAD_MARKER_" + strings.Repeat("H", 20000)
	middle := strings.Repeat("M", 30000)
	tail := strings.Repeat("T", 20000) + "_TAIL_MARKER"
	content := head + middle + tail // ~70KB

	result := guardToolOutput(content, 0.55) // moderate fill, should truncate to ~40KB

	if !strings.HasPrefix(result, "HEAD_MARKER_") {
		t.Errorf("truncated result should preserve head")
	}
	if !strings.HasSuffix(result, "_TAIL_MARKER") {
		t.Errorf("truncated result should preserve tail")
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("should contain truncation marker")
	}
}

func TestTruncateHeadTail_LineSnapping(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = strings.Repeat("x", 100) // 100 chars per line
	}
	content := strings.Join(lines, "\n") // ~20KB

	result := truncateHeadTail(content, 10000)

	// Result should not start or end with partial lines (after snapping)
	// The content starts with "xxx..." so head should be a prefix
	if !strings.HasPrefix(result, strings.Repeat("x", 100)) {
		t.Errorf("head should start with full line content")
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("should contain truncation marker")
	}
}

func TestTruncateHeadTail_SmallInput(t *testing.T) {
	content := "small"
	result := truncateHeadTail(content, 10000)
	if result != content {
		t.Errorf("small input should not be truncated")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{500, "500B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1.0MB"},
		{1024*1024 + 512*1024, "1.5MB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGuardToolOutput_Escalation(t *testing.T) {
	// Same content, different fill levels should produce progressively smaller results
	content := strings.Repeat("x", 50000) // 50KB

	r1 := guardToolOutput(content, 0.55) // moderate → ~40KB
	r2 := guardToolOutput(content, 0.70) // high → ~20KB
	r3 := guardToolOutput(content, 0.85) // critical → ~10KB

	if len(r1) <= len(r2) {
		t.Errorf("moderate fill should produce larger output than high fill: %d vs %d", len(r1), len(r2))
	}
	if len(r2) <= len(r3) {
		t.Errorf("high fill should produce larger output than critical fill: %d vs %d", len(r2), len(r3))
	}
}

func TestExtractCriticalLines_Basic(t *testing.T) {
	content := strings.Join([]string{
		"Running tests...",
		"test_a ... ok",
		"test_b ... ok",
		"FAIL: test_c - expected 42 got 41",
		"test_d ... ok",
		"panic: runtime error: index out of range",
		"goroutine 1 [running]:",
		"error: undefined variable foo",
	}, "\n")

	critical := extractCriticalLines(content)
	if len(critical) < 3 {
		t.Errorf("expected at least 3 critical lines, got %d: %v", len(critical), critical)
	}

	found := make(map[string]bool)
	for _, c := range critical {
		found[c] = true
	}
	if !found["FAIL: test_c - expected 42 got 41"] {
		t.Errorf("expected FAIL line in critical lines: %v", critical)
	}
	if !found["panic: runtime error: index out of range"] {
		t.Errorf("expected panic line in critical lines: %v", critical)
	}
	if !found["error: undefined variable foo"] {
		t.Errorf("expected error line in critical lines: %v", critical)
	}
}

func TestExtractCriticalLines_Deduplication(t *testing.T) {
	content := strings.Repeat("error: same problem here\n", 10)
	critical := extractCriticalLines(content)
	if len(critical) != 1 {
		t.Errorf("expected 1 deduplicated line, got %d: %v", len(critical), critical)
	}
}

func TestExtractCriticalLines_Empty(t *testing.T) {
	content := strings.Repeat("just normal output\n", 100)
	critical := extractCriticalLines(content)
	if len(critical) != 0 {
		t.Errorf("expected 0 critical lines for normal content, got %d", len(critical))
	}
}

func TestExtractCriticalLines_MaxLines(t *testing.T) {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("error: problem number %d", i))
	}
	critical := extractCriticalLines(strings.Join(lines, "\n"))
	if len(critical) > maxCriticalLines {
		t.Errorf("expected at most %d critical lines, got %d", maxCriticalLines, len(critical))
	}
}

func TestTruncateHeadTail_PreservesCriticalLines(t *testing.T) {
	// Build content where the error is in the middle and would be lost
	// with simple head-tail truncation. Uses newline-separated lines to
	// simulate real tool output (build logs, test results).
	head := strings.Repeat("normal build line\n", 400) // ~7.2KB
	middle := "FAIL: test_critical - expected success got failure\n" +
		"error: undefined reference to 'calculate'\n" +
		"panic: segment violation at 0x1234\n"
	tail := strings.Repeat("normal tail line\n", 400) // ~7.2KB
	content := head + middle + tail

	result := truncateHeadTail(content, 8000)

	// The critical lines should appear in the output
	if !strings.Contains(result, "FAIL: test_critical") {
		t.Errorf("truncated output should preserve FAIL line from middle")
	}
	if !strings.Contains(result, "undefined reference") {
		t.Errorf("truncated output should preserve error line from middle")
	}
	if !strings.Contains(result, "panic:") {
		t.Errorf("truncated output should preserve panic line from middle")
	}
	// Should still contain the truncation marker
	if !strings.Contains(result, "truncated") {
		t.Errorf("should contain truncation marker")
	}
}

func TestTruncateHeadTail_NoHighlightsForRepetitiveContent(t *testing.T) {
	// Pure repetitive content (no critical lines) should behave same as before
	content := strings.Repeat("x", 50000)
	result := truncateHeadTail(content, 10000)
	if strings.Contains(result, "key lines") {
		t.Errorf("should not contain highlight section for non-critical content")
	}
}

func TestGuardToolOutput_PreservesCriticalLinesInMiddle(t *testing.T) {
	// Integration test: large output with errors buried in the middle
	var parts []string
	parts = append(parts, strings.Repeat("build output line\n", 500)) // ~9.5KB head
	parts = append(parts, "main.go:42:5: error: cannot use foo (type int) as type string\n")
	parts = append(parts, "main.go:50:3: FAIL: TestExample - assertion failed\n")
	parts = append(parts, strings.Repeat("more output\n", 500)) // ~6.5KB tail
	content := strings.Join(parts, "")

	result := guardToolOutput(content, 0.55) // moderate fill → ~40KB limit

	if !strings.Contains(result, "error: cannot use foo") {
		t.Errorf("guardToolOutput should preserve error line from middle section")
	}
	if !strings.Contains(result, "FAIL: TestExample") {
		t.Errorf("guardToolOutput should preserve FAIL line from middle section")
	}
}

func TestFormatHighlights_Basic(t *testing.T) {
	lines := []string{"error: foo", "FAIL: bar", "panic: baz"}
	result := formatHighlights(lines, 1000)
	if !strings.Contains(result, "error: foo") {
		t.Errorf("should contain first line")
	}
	if !strings.Contains(result, "FAIL: bar") {
		t.Errorf("should contain second line")
	}
	if !strings.Contains(result, "3 key lines") {
		t.Errorf("should contain count in header")
	}
}

func TestFormatHighlights_BudgetLimit(t *testing.T) {
	lines := []string{"error: line 1", "error: line 2", "error: line 3", "error: line 4"}
	result := formatHighlights(lines, 40) // very small budget
	if !strings.Contains(result, "more") {
		t.Errorf("should indicate truncated highlights when budget exceeded")
	}
}

func TestFormatHighlights_Empty(t *testing.T) {
	result := formatHighlights(nil, 1000)
	if result != "" {
		t.Errorf("empty input should return empty string, got %q", result)
	}
}
