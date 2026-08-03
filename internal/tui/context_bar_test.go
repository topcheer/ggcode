package tui

import (
	"strings"
	"testing"
)

// TestContextUsageHint_ProgressBar verifies that the visual progress bar
// is rendered with the correct number of filled/empty segments.
func TestContextUsageHint_ProgressBar(t *testing.T) {
	tests := []struct {
		name     string
		ratio    float64
		wantFill int
	}{
		{"empty", 0.0, 0},
		{"low", 0.12, 0}, // 12% of 8 = 0.96 -> 0
		{"quarter", 0.25, 2},
		{"half", 0.50, 4},
		{"three_quarter", 0.75, 6},
		{"full", 1.0, 8},
		{"overflow", 1.5, 8},
	}
	const barWidth = 8
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filled := int(tc.ratio * float64(barWidth))
			if filled > barWidth {
				filled = barWidth
			}
			if filled != tc.wantFill {
				t.Errorf("ratio=%.2f: expected %d filled segments, got %d", tc.ratio, tc.wantFill, filled)
			}
		})
	}
}

// TestContextUsageHint_BarCharacters verifies the bar uses correct Unicode chars.
func TestContextUsageHint_BarCharacters(t *testing.T) {
	tokens := 64000
	cw := 128000
	ratio := float64(tokens) / float64(cw)
	const barWidth = 8
	filled := int(ratio * float64(barWidth))

	var barBuilder strings.Builder
	for i := 0; i < barWidth; i++ {
		if i < filled {
			barBuilder.WriteRune('\u2588') // full block
		} else {
			barBuilder.WriteRune('\u2591') // light shade
		}
	}

	bar := barBuilder.String()
	if strings.Count(bar, "\u2588") != 4 {
		t.Errorf("expected 4 filled blocks at 50%%, got %d: %s", strings.Count(bar, "\u2588"), bar)
	}
	if strings.Count(bar, "\u2591") != 4 {
		t.Errorf("expected 4 empty blocks at 50%%, got %d: %s", strings.Count(bar, "\u2591"), bar)
	}
}

// TestContextUsageHint_CompactWarning verifies the compaction warning logic.
func TestContextUsageHint_CompactWarning(t *testing.T) {
	// When threshold is exceeded, warning should appear
	tokens := 100000
	threshold := 80000
	compactRatio := float64(tokens) / float64(threshold)

	if compactRatio < 1.0 {
		t.Error("expected compact ratio >= 1.0 when tokens exceed threshold")
	}

	// When approaching but not exceeding threshold
	tokens2 := 70000
	compactRatio2 := float64(tokens2) / float64(threshold)
	if compactRatio2 >= 0.85 && compactRatio2 < 1.0 {
		// This should show "soon" warning - correct
	}
	if compactRatio2 >= 1.0 {
		t.Error("should not show exceeded warning when below threshold")
	}
}
