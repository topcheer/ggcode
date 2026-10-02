package agent

import "testing"

// #3102 probe: r394 token dimension was missing from perfMetricValue, so
// selectWorstPerfHit scored every candidate 0 and fell back to the FIRST
// hit run instead of the max-token one.
func TestPerfMetricValueTokensCase(t *testing.T) {
	e := perfBaselineEntry{Iterations: 3, DurationSec: 40, Tokens: 12000}
	if got := perfMetricValue(e, "tokens"); got != 12000 {
		t.Fatalf("perfMetricValue(tokens) = %d, want 12000", got)
	}
	// Other dimensions unaffected.
	if got := perfMetricValue(e, "iterations"); got != 3 {
		t.Fatalf("perfMetricValue(iterations) = %d, want 3", got)
	}
	if got := perfMetricValue(e, "unknown_metric"); got != 0 {
		t.Fatalf("perfMetricValue(unknown) = %d, want 0", got)
	}
}

func TestSelectWorstPerfHitPrefersMaxTokenRun(t *testing.T) {
	base := perfBaselineEntry{Iterations: 2, DurationSec: 20, Tokens: 1000, ToolCalls: 5}
	// Both runs hit the tokens threshold (> 2x baseline); the SECOND carries
	// the heavier spend and must be selected, not the first hit.
	first := perfBaselineEntry{Iterations: 2, DurationSec: 20, Tokens: 2100, ToolCalls: 5}
	heaviest := perfBaselineEntry{Iterations: 2, DurationSec: 20, Tokens: 9000, ToolCalls: 5}
	runs := []perfBaselineEntry{first, heaviest}
	got := selectWorstPerfHit(runs, base, "tokens")
	if got.Tokens != heaviest.Tokens {
		t.Fatalf("selectWorstPerfHit picked run with %d tokens, want %d", got.Tokens, heaviest.Tokens)
	}
}
