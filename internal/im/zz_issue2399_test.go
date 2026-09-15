package im

// #2399: the IM /usage path picks the probe BY URL (owner ruling), 15s
// outer budget (#2400).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2399IMSlashPicksProbeByURL(t *testing.T) {
	b, err := os.ReadFile("slash_agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "imUsageService().Resolve(baseURL)") {
		t.Fatal("IM /usage must Resolve the probe from the endpoint URL")
	}
	if strings.Contains(src, "Get(ctx, cfg.Vendor,") {
		t.Fatal("IM /usage must never Get by config vendor name")
	}
	if !strings.Contains(src, "15*time.Second") {
		t.Fatal("#2400: outer budget must be 15s (TUI baseline)")
	}
}
