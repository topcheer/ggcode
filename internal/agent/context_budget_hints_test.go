package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/context"
)

func TestBudgetHintLevel_String(t *testing.T) {
	tests := []struct {
		level contextBudgetHintLevel
		want  string
	}{
		{budgetHintNone, "none"},
		{budgetHintModerate, "moderate(70%)"},
		{budgetHintHigh, "high(85%)"},
		{contextBudgetHintLevel(99), "level(99)"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("level %d: got %q, want %q", int(tt.level), got, tt.want)
		}
	}
}

func TestMaybeInjectBudgetHint_NoHintBelowThreshold(t *testing.T) {
	a := newTestAgentWithCtxWindow(t, 128000)
	// 69% fill = 88320 tokens (below 70% threshold of 89600)
	setTokenCount(t, a.contextManager.(*context.Manager), 88320)

	level := budgetHintNone
	injected := a.maybeInjectBudgetHint(&level)
	if injected {
		t.Fatal("should not inject hint below 70% threshold")
	}
	if level != budgetHintNone {
		t.Fatalf("level should remain none, got %s", level)
	}
}

func TestMaybeInjectBudgetHint_ModerateAt70Percent(t *testing.T) {
	a := newTestAgentWithCtxWindow(t, 128000)
	// 70% fill = 89600 tokens
	setTokenCount(t, a.contextManager.(*context.Manager), 89600)

	level := budgetHintNone
	injected := a.maybeInjectBudgetHint(&level)
	if !injected {
		t.Fatal("should inject moderate hint at 70% threshold")
	}
	if level != budgetHintModerate {
		t.Fatalf("level should be moderate, got %s", level)
	}

	// Verify a user message was added
	msgs := a.contextManager.Messages()
	if len(msgs) == 0 {
		t.Fatal("expected at least one message after injection")
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" {
		t.Fatalf("expected user role, got %s", last.Role)
	}
}

func TestMaybeInjectBudgetHint_HighAt85Percent(t *testing.T) {
	a := newTestAgentWithCtxWindow(t, 128000)
	// 85% fill = 108800 tokens
	setTokenCount(t, a.contextManager.(*context.Manager), 108800)

	level := budgetHintNone
	// Should jump straight to high (skipping moderate) since 85% >= high threshold
	injected := a.maybeInjectBudgetHint(&level)
	if !injected {
		t.Fatal("should inject high hint at 85%+ threshold")
	}
	if level != budgetHintHigh {
		t.Fatalf("level should be high, got %s", level)
	}
}

func TestMaybeInjectBudgetHint_ProgressionFromModerateToHigh(t *testing.T) {
	a := newTestAgentWithCtxWindow(t, 128000)

	// First: inject moderate at 70%
	setTokenCount(t, a.contextManager.(*context.Manager), 89600)
	level := budgetHintNone
	if !a.maybeInjectBudgetHint(&level) {
		t.Fatal("should inject moderate at 70%")
	}
	if level != budgetHintModerate {
		t.Fatalf("expected moderate, got %s", level)
	}

	// Should NOT re-inject moderate (level already at moderate)
	if a.maybeInjectBudgetHint(&level) {
		t.Fatal("should not re-inject at same level")
	}

	// Now: progress to high at 85%
	setTokenCount(t, a.contextManager.(*context.Manager), 108800)
	if !a.maybeInjectBudgetHint(&level) {
		t.Fatal("should inject high at 85%")
	}
	if level != budgetHintHigh {
		t.Fatalf("expected high, got %s", level)
	}
}

func TestMaybeInjectBudgetHint_NoReinject(t *testing.T) {
	a := newTestAgentWithCtxWindow(t, 128000)
	// 75% fill — above moderate threshold but below high
	setTokenCount(t, a.contextManager.(*context.Manager), 96000)

	level := budgetHintModerate // already at moderate
	injected := a.maybeInjectBudgetHint(&level)
	if injected {
		t.Fatal("should not re-inject when already at moderate level and below high threshold")
	}
	if level != budgetHintModerate {
		t.Fatalf("level should remain moderate, got %s", level)
	}
}

func TestMaybeInjectBudgetHint_ZeroContextWindow(t *testing.T) {
	a := newTestAgentWithCtxWindow(t, 0)

	level := budgetHintNone
	injected := a.maybeInjectBudgetHint(&level)
	if injected {
		t.Fatal("should not inject when context window is 0")
	}
}

// --- helpers ---

func newTestAgentWithCtxWindow(t *testing.T, window int) *Agent {
	t.Helper()
	a := NewAgent(nil, nil, "", 10)
	cm := context.NewManager(window)
	a.contextManager = cm
	return a
}

// setTokenCount sets a known token count on the context manager for testing.
// Uses SetCheckpointBaseline to establish a deterministic token count.
func setTokenCount(t *testing.T, cm *context.Manager, tokens int) {
	t.Helper()
	cm.SetCheckpointBaseline(tokens)
}
