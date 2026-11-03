package agent

import (
	"strings"
	"testing"
)

func Test1819GuidanceRoutedThroughBudget(t *testing.T) {
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

	shrunkTokens := estimateTokensLen(strings.Repeat("a", shrunk), -1)
	viaOverride := estimateTokensLen(strings.Repeat("a", shrunk), shrunk)
	if viaOverride != shrunkTokens {
		t.Fatalf("override with actual length must equal plain estimate: %d vs %d", viaOverride, shrunkTokens)
	}

	stale := estimateTokensLen(strings.Repeat("a", shrunk), 60*1024)
	if stale <= shrunkTokens {
		t.Fatalf("stale override should meter higher: %d vs %d", stale, shrunkTokens)
	}
}
