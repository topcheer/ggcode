package agent

import (
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
