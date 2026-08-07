package agent

import (
	"strings"
	"testing"
)

func TestScanPrematureAbstraction_Empty(t *testing.T) {
	hits := scanPrematureAbstraction("")
	if hits != nil {
		t.Errorf("expected nil for empty text, got %v", hits)
	}
}

func TestScanPrematureAbstraction_NoMatch(t *testing.T) {
	text := "I fixed the bug by changing line 42 in parser.go."
	hits := scanPrematureAbstraction(text)
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for simple text, got %d", len(hits))
	}
}

func TestScanPrematureAbstraction_FactoryPattern(t *testing.T) {
	text := "I created a Factory pattern to handle different request types. " +
		"Let me implement a Builder class to construct the objects."
	hits := scanPrematureAbstraction(text)
	if len(hits) < 2 {
		t.Errorf("expected >=2 hits for factory+builder, got %d", len(hits))
	}
	found := false
	for _, h := range hits {
		if h.category == "pattern_inflation" {
			found = true
		}
	}
	if !found {
		t.Error("expected pattern_inflation category in hits")
	}
}

func TestScanPrematureAbstraction_InterfaceHierarchy(t *testing.T) {
	text := "I've defined an interface so that we can swap implementations later. " +
		"For future extensibility, I have abstracted the data layer."
	hits := scanPrematureAbstraction(text)
	if len(hits) < 2 {
		t.Errorf("expected >=2 hits for interface+extensibility, got %d", len(hits))
	}
}

func TestScanPrematureAbstraction_ConfigSystem(t *testing.T) {
	text := "I created a configurable system for the timeout values. " +
		"I also added a plugin mechanism so that users can extend the parser."
	hits := scanPrematureAbstraction(text)
	if len(hits) < 2 {
		t.Errorf("expected >=2 hits for config+plugin, got %d", len(hits))
	}
}

func TestScanPrematureAbstraction_GenericFramework(t *testing.T) {
	text := "I've created a generic handler framework for all the endpoints."
	hits := scanPrematureAbstraction(text)
	if len(hits) < 1 {
		t.Errorf("expected >=1 hit for generic handler, got %d", len(hits))
	}
}

func TestScanPrematureAbstraction_Dedup(t *testing.T) {
	text := "I created a Factory pattern. I created a Factory pattern again."
	hits := scanPrematureAbstraction(text)
	// Both should match but excerpt dedup should reduce
	// (different surrounding context may still produce different excerpts)
	for _, h := range hits {
		if h.category != "pattern_inflation" {
			t.Errorf("expected pattern_inflation, got %s", h.category)
		}
	}
}

func TestMaybeWarnPrematureAbstraction_BelowThreshold(t *testing.T) {
	a := &Agent{prematureAbstr: newPrematureAbstrState()}
	// Only 1 signal - below threshold of 2
	text := "I created a Factory pattern for this."
	hint := a.maybeWarnPrematureAbstraction(text)
	if hint != "" {
		t.Error("expected no hint below threshold")
	}
}

func TestMaybeWarnPrematureAbstraction_AtThreshold(t *testing.T) {
	a := &Agent{prematureAbstr: newPrematureAbstrState()}
	text := "I created a Factory pattern for this. " +
		"I've defined an interface so that we can swap implementations later."
	hint := a.maybeWarnPrematureAbstraction(text)
	if hint == "" {
		t.Error("expected hint at threshold")
	}
	if !strings.Contains(hint, "[over-engineering]") {
		t.Error("hint should contain [over-engineering] tag")
	}
	if !strings.Contains(hint, "YAGNI") {
		t.Error("hint should contain YAGNI guidance")
	}
}

func TestMaybeWarnPrematureAbstraction_MaxWarnings(t *testing.T) {
	a := &Agent{prematureAbstr: newPrematureAbstrState()}
	text := "I created a Factory pattern. I've defined an interface so that we can swap implementations."
	// First call should warn
	hint1 := a.maybeWarnPrematureAbstraction(text)
	if hint1 == "" {
		t.Fatal("expected first warning")
	}
	// Second call should warn (max=2)
	hint2 := a.maybeWarnPrematureAbstraction(text)
	if hint2 == "" {
		t.Fatal("expected second warning")
	}
	// Third call should NOT warn (exceeded max)
	hint3 := a.maybeWarnPrematureAbstraction(text)
	if hint3 != "" {
		t.Error("expected no third warning (max exceeded)")
	}
}

func TestMaybeWarnPrematureAbstraction_NilState(t *testing.T) {
	a := &Agent{prematureAbstr: nil}
	hint := a.maybeWarnPrematureAbstraction("I created a Factory pattern.")
	if hint != "" {
		t.Error("expected no hint with nil state")
	}
}

func TestPrematureAbstrState_Reset(t *testing.T) {
	s := newPrematureAbstrState()
	s.warnings = 2
	s.reset()
	if s.warnings != 0 {
		t.Errorf("expected 0 warnings after reset, got %d", s.warnings)
	}
}

func TestScanPrematureAbstraction_ExcerptTruncation(t *testing.T) {
	// Long text around the match should be truncated to 80 chars
	longPrefix := strings.Repeat("This is a very long preamble. ", 10)
	text := longPrefix + "I created a Factory pattern to handle different request types."
	hits := scanPrematureAbstraction(text)
	for _, h := range hits {
		if len(h.excerpt) > 84 { // 80 + "..."
			t.Errorf("excerpt too long: %d chars: %s", len(h.excerpt), h.excerpt)
		}
	}
}

func TestScanPrematureAbstraction_RealisticOverEngineer(t *testing.T) {
	text := `I've implemented the solution with the following architecture:

1. I created a Factory pattern to handle different request types.
2. I've defined an interface so that we can swap implementations later.
3. I created a configurable system for the timeout values.
4. For future extensibility, I have abstracted the data layer.

This makes the code fully extensible and maintainable.`
	hits := scanPrematureAbstraction(text)
	if len(hits) < 4 {
		t.Errorf("expected >=4 hits for realistic over-engineering text, got %d", len(hits))
	}
}
