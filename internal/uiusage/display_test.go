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
	if display.UsedLabel != "1500" {
		t.Fatalf("used_label = %q, want 1500", display.UsedLabel)
	}
	if display.MaxLabel != "2k" {
		t.Fatalf("max_label = %q, want 2k", display.MaxLabel)
	}
}

func TestBuildSessionUsageDisplayMatchesTUIFormatting(t *testing.T) {
	display := BuildSessionUsageDisplay(provider.TokenUsage{
		InputTokens:  1000,
		OutputTokens: 340,
		CacheRead:    800,
		CacheWrite:   64,
	})
	if display.TotalLabel != "1340" {
		t.Fatalf("total_label = %q, want 1340", display.TotalLabel)
	}
	if display.InputLabel != "1k" {
		t.Fatalf("input_label = %q, want 1k", display.InputLabel)
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
