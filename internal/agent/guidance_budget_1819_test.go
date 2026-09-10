package agent

import (
	"strings"
	"testing"
)

// #1819 case 1: the five detector hints route through injectGuidance so
// the guidance budget's per-turn cap covers them. Source-level pin via
// build + the behavioral test in guidance_budget_test.go; here we pin the
// budget actually suppresses when exhausted (the property the direct-Add
// path bypassed).
func Test1819GuidanceRoutedThroughBudget(t *testing.T) {
	// Pin the budget property the direct contextManager.Add path bypassed:
	// after the advisory slots are drained, a further detector guidance
	// (e.g. a [token-waste] warning) must be suppressed by allow().
	var g guidanceBudget
	g.reset()
	sent := 0
	for i := 0; i < guidanceBudgetPerTurn+2; i++ {
		if g.allow("filler advisory message to drain the budget slot") {
			sent++
		}
	}
	if sent != guidanceBudgetPerTurn {
		t.Fatalf("expected exactly %d advisories to pass, got %d", guidanceBudgetPerTurn, sent)
	}
	if g.allow("[token-waste] budget-exceeded message") {
		t.Fatal("detector guidance after budget exhaustion must be suppressed")
	}
	if g.suppressed == 0 {
		t.Fatal("suppression counter must advance")
	}
}

func Test1819MeteringUsesPostShrinkLen(t *testing.T) {
	shrunk := 6 * 1024
	// The fix passes measuredLen (== len of shrunk content) as the
	// override, so metered tokens must equal tokens of the shrunk length.
	shrunkTokens := estimateTokensLen(strings.Repeat("a", shrunk), -1)
	viaOverride := estimateTokensLen(strings.Repeat("a", shrunk), shrunk)
	if viaOverride != shrunkTokens {
		t.Fatalf("override with actual length must equal plain estimate: %d vs %d", viaOverride, shrunkTokens)
	}
	// Sanity: a stale pre-shrink override would meter 10x the real cost -
	// exactly the inflation the fix removes.
	stale := estimateTokensLen(strings.Repeat("a", shrunk), 60*1024)
	if stale <= shrunkTokens {
		t.Fatalf("stale override should meter higher: %d vs %d", stale, shrunkTokens)
	}
}
