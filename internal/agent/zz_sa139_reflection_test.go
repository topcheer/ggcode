package agent

import (
	"strings"
	"testing"
)

func TestGenerateInsightsFailedRunCarriesUnverifiedMarker(t *testing.T) {
	stats := RunStats{
		Success:    false,
		Iterations: 5,
		ToolCalls:  map[string]int{"edit_file": 3},
	}
	out := GenerateInsights(stats)
	if out == "" {
		t.Fatal("non-trivial failed run must still generate insights")
	}
	if !strings.Contains(out, "verified: no") || !strings.Contains(out, "FAILED") {
		t.Fatalf("failed-run insights missing unverified marker:\n%s", out[:min(300, len(out))])
	}

	ok := RunStats{Success: true, Iterations: 3, ToolCalls: map[string]int{"read_file": 2}}
	if out2 := GenerateInsights(ok); strings.Contains(out2, "verified: no") {
		t.Fatal("successful run must not carry the unverified disclaimer")
	}
}
