package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		input time.Duration
		want  string
	}{
		{0, "-"},
		{-1 * time.Second, "-"},
		{500 * time.Millisecond, "500ms"},
		{1500 * time.Millisecond, "1.5s"},
		{5 * time.Second, "5.0s"},
		{9500 * time.Millisecond, "9.5s"},
		{15 * time.Second, "15s"},
		{90 * time.Second, "1m30s"},
	}
	for _, tt := range tests {
		got := FormatDuration(tt.input)
		if got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatTurnDigest(t *testing.T) {
	turn := TurnSummary{
		TurnIndex:           3,
		TTFT:                1200 * time.Millisecond,
		Duration:            5300 * time.Millisecond,
		ThinkTime:           800 * time.Millisecond,
		ToolCallCount:       2,
		SlowestTool:         "edit_file",
		SlowestToolDuration: 3 * time.Second,
		ToolFailureCount:    1,
	}

	t.Run("english", func(t *testing.T) {
		got := FormatTurnDigest("en", turn)
		if !strings.Contains(got, "Turn #3") {
			t.Errorf("expected 'Turn #3' in %q", got)
		}
		if !strings.Contains(got, "TTFT") {
			t.Errorf("expected 'TTFT' in %q", got)
		}
		if !strings.Contains(got, "edit_file") {
			t.Errorf("expected 'edit_file' in %q", got)
		}
		if !strings.Contains(got, "!") {
			t.Errorf("expected failure marker '!' in %q", got)
		}
	})

	t.Run("chinese", func(t *testing.T) {
		got := FormatTurnDigest("zh-CN", turn)
		if !strings.Contains(got, "第 3 轮") {
			t.Errorf("expected '第 3 轮' in %q", got)
		}
		if !strings.Contains(got, "首字") {
			t.Errorf("expected '首字' in %q", got)
		}
	})

	t.Run("unknown language falls back to english", func(t *testing.T) {
		got := FormatTurnDigest("fr", turn)
		if !strings.Contains(got, "Turn #3") {
			t.Errorf("expected english fallback 'Turn #3' in %q", got)
		}
	})

	t.Run("no slowest tool", func(t *testing.T) {
		minimal := TurnSummary{
			TurnIndex:     1,
			TTFT:          500 * time.Millisecond,
			Duration:      2 * time.Second,
			ThinkTime:     0,
			ToolCallCount: 0,
		}
		got := FormatTurnDigest("en", minimal)
		if strings.Contains(got, "Slowest") {
			t.Errorf("expected no 'Slowest' when empty, got %q", got)
		}
	})

	t.Run("no failures", func(t *testing.T) {
		noFail := turn
		noFail.ToolFailureCount = 0
		got := FormatTurnDigest("en", noFail)
		// Count exclamation marks — should not have the failure marker
		// The slowest tool duration still contains "3.0s" so just check
		// the digest doesn't end with "!"
		if strings.HasSuffix(got, "!") {
			t.Errorf("expected no failure marker, got %q", got)
		}
	})
}
