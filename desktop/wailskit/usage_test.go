package wailskit

import (
	"strings"
	"testing"
)

// #2150 batch 3: the IM renderer must degrade gracefully and must not lose
// the balance/windows payload.
func TestFormatUsageForIM(t *testing.T) {
	bal := 12.5
	got := FormatUsageForIM(UsageInfoResult{
		Vendor:  "zai",
		Source:  "billing",
		Balance: &bal,
		Windows: []UsageWindowInfo{{Label: "5h", UsedPercent: 42.3, ResetsAt: "2026-09-26T12:00:00Z"}},
	})
	if !strings.Contains(got, "zai") || !strings.Contains(got, "12.50") {
		t.Fatalf("vendor/balance missing: %q", got)
	}
	if !strings.Contains(got, "5h") || !strings.Contains(got, "42%") {
		t.Fatalf("window missing: %q", got)
	}
	if !strings.Contains(got, "resets") {
		t.Fatalf("reset hint missing: %q", got)
	}
	errCase := FormatUsageForIM(UsageInfoResult{Vendor: "kimi", Error: "unsupported vendor"})
	if !strings.Contains(errCase, "kimi") || !strings.Contains(errCase, "unsupported") {
		t.Fatalf("error case must degrade gracefully: %q", errCase)
	}
}

// GetUsageInfo with no config resolves to a soft error result, not a panic
// or a Wails error - usage is ambient information.
func TestGetUsageInfoNoConfig(t *testing.T) {
	res := (&ChatBridge{}).GetUsageInfo()
	if res.Error == "" {
		t.Fatalf("expected soft error for empty config, got %+v", res)
	}
	if res.Vendor != "" && res.Error == "" {
		t.Fatalf("unexpected vendor resolution without config: %+v", res)
	}
}
