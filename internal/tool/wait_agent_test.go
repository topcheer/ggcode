package tool

import (
	"strings"
	"testing"
)

// r388 (FastContext Kim et al. 2026): Explore sub-agent results carry a
// structured "## Regions" section that wait_agent converts into explicit
// targeted-read lines for the parent.
func TestFormatExploreRegions(t *testing.T) {
	result := `Explored the retry logic. The backoff loop lives in one place.

## Regions
internal/retry/backoff.go:40-72 - computes the exponential delay
internal/retry/policy.go:12-19 - default policy constants`
	out := formatExploreRegions(result)
	if out == "" {
		t.Fatal("expected targeted-read block")
	}
	if !strings.Contains(out, "read_file internal/retry/backoff.go (offset 40, limit 33)") {
		t.Errorf("wrong first region line: %s", out)
	}
	if !strings.Contains(out, "read_file internal/retry/policy.go (offset 12, limit 8)") {
		t.Errorf("wrong second region line: %s", out)
	}
}

func TestFormatExploreRegionsNoMarker(t *testing.T) {
	if formatExploreRegions("plain result with internal/retry/backoff.go:40-72 inline") != "" {
		t.Error("no ## Regions marker: result must pass through untouched")
	}
}

func TestFormatExploreRegionsBadRanges(t *testing.T) {
	// Malformed ranges are skipped, not fatal.
	result := "## Regions\na.go:0-9 - zero start\nb.go:30-10 - inverted\nc.go:5-9 - valid"
	out := formatExploreRegions(result)
	if !strings.Contains(out, "c.go (offset 5, limit 5)") {
		t.Errorf("valid region lost: %s", out)
	}
	if strings.Contains(out, "a.go") || strings.Contains(out, "b.go") {
		t.Errorf("malformed ranges leaked: %s", out)
	}
}
