package wailskit

// #2399/#2400: the desktop + IM /usage paths must pick probes BY URL
// (owner ruling) with a 15s outer budget, and the never-wired
// FormatUsageForIM dead code is gone.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2399DesktopPicksProbeByURL(t *testing.T) {
	b, _ := os.ReadFile("usage.go")
	src := string(b)
	if !strings.Contains(src, "usageService.Resolve(ep.baseURL)") {
		t.Fatal("desktop /usage must Resolve the probe from the endpoint URL")
	}
	if strings.Contains(src, "Get(ctx, cfg.Vendor,") {
		t.Fatal("desktop /usage must never Get by config vendor name")
	}
	if !strings.Contains(src, "15*time.Second") {
		t.Fatal("#2400: outer budget must be 15s (TUI baseline), not the 5s clamp")
	}
	if strings.Contains(src, "FormatUsageForIM") {
		t.Fatal("#2400: never-wired FormatUsageForIM must be deleted")
	}
}
