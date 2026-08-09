package agent

import "testing"

func TestGuidanceBudget_AllowWithinLimit(t *testing.T) {
	var g guidanceBudget
	g.reset()

	// First N messages should be allowed
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		if !g.allow("[ADVISORY] hint " + string(rune('A'+i))) {
			t.Fatalf("message %d should be allowed within budget", i)
		}
	}
}

func TestGuidanceBudget_SuppressesBeyondLimit(t *testing.T) {
	var g guidanceBudget
	g.reset()

	// Exhaust budget with advisory messages
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		g.allow("[ADVISORY] hint")
	}

	// Next advisory should be suppressed
	if g.allow("[ADVISORY] over budget") {
		t.Fatal("advisory message beyond budget should be suppressed")
	}

	if g.suppressed != 1 {
		t.Fatalf("expected 1 suppressed, got %d", g.suppressed)
	}

	// Another one
	if g.allow("[ADVISORY] still over") {
		t.Fatal("second advisory beyond budget should be suppressed")
	}
	if g.suppressed != 2 {
		t.Fatalf("expected 2 suppressed, got %d", g.suppressed)
	}
}

func TestGuidanceBudget_CriticalBypassesCap(t *testing.T) {
	var g guidanceBudget
	g.reset()

	// Exhaust budget
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		g.allow("[ADVISORY] hint")
	}

	// Critical message should pass through even when budget exhausted
	if !g.allow("[CRITICAL] this is important") {
		t.Fatal("critical message should bypass budget cap")
	}
	if !g.allow("[SECURITY] vulnerability found") {
		t.Fatal("security message should bypass budget cap")
	}
}

func TestGuidanceBudget_ResetClearsCounters(t *testing.T) {
	var g guidanceBudget
	g.reset()

	for i := 0; i < guidanceBudgetPerTurn+3; i++ {
		g.allow("[ADVISORY] hint")
	}

	if g.suppressed != 3 {
		t.Fatalf("expected 3 suppressed before reset, got %d", g.suppressed)
	}

	g.reset()

	if g.injected != 0 {
		t.Fatalf("expected injected=0 after reset, got %d", g.injected)
	}
	if g.suppressed != 0 {
		t.Fatalf("expected suppressed=0 after reset, got %d", g.suppressed)
	}

	// Budget should be available again
	if !g.allow("[ADVISORY] post-reset hint") {
		t.Fatal("advisory message after reset should be allowed")
	}
}

func TestIsCriticalGuidance(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"[CRITICAL] something", true},
		{"[PERMISSION] needed", true},
		{"[DESTRUCTIVE] action", true},
		{"[IRREVERSIBLE] change", true},
		{"[SAFETY] issue", true},
		{"[SECURITY] concern", true},
		{"[BLOCKED] by policy", true},
		{"[FATAL] error", true},
		{"[pre-commit-build-gate] failed", true},
		{"[hardcoded-secret] detected", true},
		{"[path-traversal] risk", true},
		{"[git-destructive] force push", true},
		{"[ADVISORY] mild suggestion", false},
		{"[Tool Call Storm] too many calls", false},
		{"[Analysis Paralysis] overthinking", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isCriticalGuidance(tt.text)
		if got != tt.want {
			t.Errorf("isCriticalGuidance(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestGuidanceBudget_MixedCriticalAndAdvisory(t *testing.T) {
	var g guidanceBudget
	g.reset()

	// Critical messages don't consume the budget
	g.allow("[CRITICAL] first critical")
	g.allow("[SECURITY] second critical")

	// Budget should still be full for advisory
	if g.injected != 0 {
		t.Fatalf("critical messages should not consume advisory budget, got injected=%d", g.injected)
	}

	// Advisory messages use the budget
	for i := 0; i < guidanceBudgetPerTurn; i++ {
		if !g.allow("[ADVISORY] hint") {
			t.Fatalf("advisory %d should be allowed", i)
		}
	}

	// Critical still passes
	if !g.allow("[FATAL] crash") {
		t.Fatal("critical should still pass after advisory budget exhausted")
	}
}
