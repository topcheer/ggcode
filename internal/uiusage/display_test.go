package uiusage

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestBuildContextDisplayMatchesTUISemantics(t *testing.T) {
	display, ok := BuildContextDisplay(1500, 2000, 1300)
	if !ok {
		t.Fatal("expected display")
	}
	if display.UsagePercent != 75 {
		t.Fatalf("usage_percent = %d, want 75", display.UsagePercent)
	}
	if display.RemainingPercent != 0 {
		t.Fatalf("remaining_percent = %d, want 0", display.RemainingPercent)
	}
	if display.UsedLabel != "1.50K" {
		t.Fatalf("used_label = %q, want 1.50K", display.UsedLabel)
	}
	if display.MaxLabel != "2.00K" {
		t.Fatalf("max_label = %q, want 2.00K", display.MaxLabel)
	}
}

func TestBuildSessionUsageDisplayMatchesTUIFormatting(t *testing.T) {
	display := BuildSessionUsageDisplay(provider.TokenUsage{
		InputTokens:  1000,
		OutputTokens: 340,
		CacheRead:    800,
		CacheWrite:   64,
	})
	if display.TotalLabel != "1.34K" {
		t.Fatalf("total_label = %q, want 1.34K", display.TotalLabel)
	}
	if display.InputLabel != "1.00K" {
		t.Fatalf("input_label = %q, want 1.00K", display.InputLabel)
	}
	if display.OutputLabel != "340" {
		t.Fatalf("output_label = %q, want 340", display.OutputLabel)
	}
	if display.CacheReadLabel != "800" {
		t.Fatalf("cache_read_label = %q, want 800", display.CacheReadLabel)
	}
	if display.CacheWriteLabel != "64" {
		t.Fatalf("cache_write_label = %q, want 64", display.CacheWriteLabel)
	}
}
