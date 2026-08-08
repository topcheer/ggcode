package agent

import (
	"strings"
	"testing"
)

func TestScanSatisficing_HighLevelTemporaryFix(t *testing.T) {
	text := "I implemented a temporary fix for the issue. We should revisit later."
	hits := scanSatisficing(text)
	if len(hits) == 0 {
		t.Fatal("expected satisficing hits for temporary fix language")
	}
	foundHigh := false
	for _, h := range hits {
		if h.level == "HIGH" {
			foundHigh = true
		}
	}
	if !foundHigh {
		t.Fatal("expected at least one HIGH level hit")
	}
}

func TestScanSatisficing_NotIdealBut(t *testing.T) {
	text := "It's not ideal but it works for now."
	hits := scanSatisficing(text)
	if len(hits) == 0 {
		t.Fatal("expected satisficing hits for 'not ideal but' language")
	}
}

func TestScanSatisficing_GoodEnough(t *testing.T) {
	text := "The solution is good enough for the current use case. It's a quick workaround."
	hits := scanSatisficing(text)
	if len(hits) < 2 {
		t.Fatalf("expected >=2 hits for 'good enough' + 'quick workaround', got %d", len(hits))
	}
}

func TestScanSatisficing_NoSettlement(t *testing.T) {
	text := "I implemented the feature as specified. All tests pass and the build is green."
	hits := scanSatisficing(text)
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits for clean solution, got %d: %v", len(hits), hits)
	}
}

func TestScanSatisficing_EmptyText(t *testing.T) {
	hits := scanSatisficing("")
	if hits != nil {
		t.Fatal("expected nil for empty text")
	}
}

func TestScanSatisficing_Workaround(t *testing.T) {
	text := "I used a workaround to bypass the limitation."
	hits := scanSatisficing(text)
	if len(hits) == 0 {
		t.Fatal("expected hit for 'workaround'")
	}
}

func TestScanSatisficing_ShouldBeFine(t *testing.T) {
	text := "This should be fine for the current use case."
	hits := scanSatisficing(text)
	if len(hits) == 0 {
		t.Fatal("expected hit for 'should be fine'")
	}
}

func TestScanSatisficing_Dedup(t *testing.T) {
	text := "This is a temporary fix. The temporary fix handles the edge case."
	hits := scanSatisficing(text)
	// Same excerpt should be deduplicated
	seen := make(map[string]bool)
	for _, h := range hits {
		key := h.level + ":" + h.excerpt
		if seen[key] {
			t.Fatalf("duplicate hit found: %s", key)
		}
		seen[key] = true
	}
}

func TestMaybeWarnSatisficing_BelowThreshold(t *testing.T) {
	a := &Agent{
		satisficingSettle: newSatisficingSettleState(),
	}
	// Only 1 hit -- below threshold of 2
	text := "I used a workaround here."
	msg := a.maybeWarnSatisficing(text)
	if msg != "" {
		t.Fatal("expected no warning below threshold")
	}
}

func TestMaybeWarnSatisficing_AtThreshold(t *testing.T) {
	a := &Agent{
		satisficingSettle: newSatisficingSettleState(),
	}
	text := "This is a temporary fix. It's not ideal but it works."
	msg := a.maybeWarnSatisficing(text)
	if msg == "" {
		t.Fatal("expected warning at threshold")
	}
	if !strings.Contains(msg, "satisficing") {
		t.Fatal("warning should mention satisficing")
	}
}

func TestMaybeWarnSatisficing_MaxWarningsCap(t *testing.T) {
	a := &Agent{
		satisficingSettle: &satisficingSettleState{warnings: satisficingMaxWarnings},
	}
	text := "This is a temporary fix. It's not ideal but it works. It's a quick workaround."
	msg := a.maybeWarnSatisficing(text)
	if msg != "" {
		t.Fatal("expected no warning when max warnings reached")
	}
}

func TestMaybeWarnSatisficing_NilState(t *testing.T) {
	a := &Agent{
		satisficingSettle: nil,
	}
	msg := a.maybeWarnSatisficing("temporary fix workaround")
	if msg != "" {
		t.Fatal("expected no warning when state is nil")
	}
}

func TestMaybeWarnSatisficing_HighSeverity(t *testing.T) {
	a := &Agent{
		satisficingSettle: newSatisficingSettleState(),
	}
	// 2+ HIGH level hits should produce WARNING severity
	text := "This is a temporary fix. It's not ideal but it works. A temporary workaround."
	msg := a.maybeWarnSatisficing(text)
	if msg == "" {
		t.Fatal("expected warning")
	}
	if !strings.Contains(msg, "WARNING") {
		t.Fatalf("expected WARNING severity for 2+ HIGH hits, got: %s", msg)
	}
}

func TestSatisficingSettleState_Reset(t *testing.T) {
	s := &satisficingSettleState{warnings: 3}
	s.reset()
	if s.warnings != 0 {
		t.Fatalf("expected warnings=0 after reset, got %d", s.warnings)
	}
}

func TestScanSatisficing_LowLevelRevisitLater(t *testing.T) {
	text := "We can improve this later if needed."
	hits := scanSatisficing(text)
	found := false
	for _, h := range hits {
		if h.level == "LOW" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected LOW level hit for 'we can improve later'")
	}
}
