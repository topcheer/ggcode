package agent

import (
	"strings"
	"testing"
)

func TestScanAssumptions_HighConfidence(t *testing.T) {
	text := `I'll go ahead and implement this. I assume the database is PostgreSQL.
	I'll assume the default port is 3000. Assuming that auth is handled by middleware,
	I'll skip that part.`

	hits := scanAssumptions(text)
	if len(hits) == 0 {
		t.Fatal("expected assumption hits, got none")
	}

	highCount := 0
	for _, h := range hits {
		if h.level == "HIGH" {
			highCount++
		}
	}
	if highCount < 3 {
		t.Errorf("expected at least 3 HIGH hits, got %d", highCount)
	}
}

func TestScanAssumptions_MediumConfidence(t *testing.T) {
	text := `I think this should go in the domain layer. This probably needs a migration.
	It seems like the API expects JSON. My best guess is a race condition.`

	hits := scanAssumptions(text)
	if len(hits) < 3 {
		t.Fatalf("expected at least 3 medium hits, got %d", len(hits))
	}

	// All should be MEDIUM level
	for _, h := range hits {
		if h.level != "MEDIUM" {
			t.Errorf("expected MEDIUM, got %s for: %s", h.level, h.excerpt)
		}
	}
}

func TestScanAssumptions_NoAssumptions(t *testing.T) {
	text := `I've read the file and confirmed the function signature. The test passes
	with the expected output. The build succeeds with no errors.`

	hits := scanAssumptions(text)
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits for clean text, got %d: %v", len(hits), hits)
	}
}

func TestScanAssumptions_EmptyText(t *testing.T) {
	hits := scanAssumptions("")
	if hits != nil {
		t.Fatal("expected nil for empty text")
	}
}

func TestScanAssumptions_Deduplication(t *testing.T) {
	text := `I assume this works. I assume this works. I assume this works.`

	hits := scanAssumptions(text)
	// "I assume" should match 3 times, but "I'll assume" might also hit
	// All 3 occurrences of "I assume" have the same pattern but different excerpts
	// (different surrounding context), so they won't be deduped
	if len(hits) < 3 {
		t.Errorf("expected at least 3 hits (different context), got %d", len(hits))
	}
}

func TestScanAssumptions_BeliefPattern(t *testing.T) {
	// "I believe the port is 8080" should match
	text := `I believe the port is 8080, so I'll use that.`
	hits := scanAssumptions(text)
	found := false
	for _, h := range hits {
		if strings.Contains(h.excerpt, "believe") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find 'I believe the' pattern")
	}

	// "I believe we should refactor" should NOT match (no decision-oriented word)
	text2 := `I believe we should refactor this.`
	hits2 := scanAssumptions(text2)
	for _, h := range hits2 {
		if strings.Contains(h.excerpt, "believe") {
			t.Error("should not match 'I believe we should' - not a factual claim")
		}
	}
}

func TestMaybeWarnAssumptions_BelowThreshold(t *testing.T) {
	a := &Agent{
		assumptionTracker: newAssumptionTrackerState(),
	}
	defer a.assumptionTracker.reset()

	// Only 2 assumptions - below threshold of 3
	text := `I assume X is true. I'll assume Y is also true.`
	hint := a.maybeWarnAssumptions(text)
	if hint != "" {
		t.Errorf("expected no warning for 2 assumptions, got: %s", hint)
	}
}

func TestMaybeWarnAssumptions_AtThreshold(t *testing.T) {
	a := &Agent{
		assumptionTracker: newAssumptionTrackerState(),
	}
	defer a.assumptionTracker.reset()

	text := `I assume the DB is PostgreSQL. I'll assume port 3000.
		Assuming that auth is middleware. This probably needs tests too.`

	hint := a.maybeWarnAssumptions(text)
	if hint == "" {
		t.Fatal("expected warning for 4+ assumptions, got empty")
	}

	if !strings.Contains(hint, "[") {
		t.Error("hint should contain severity tag")
	}
	if !strings.Contains(hint, "assumption") {
		t.Error("hint should mention assumptions")
	}
}

func TestMaybeWarnAssumptions_MaxWarnings(t *testing.T) {
	a := &Agent{
		assumptionTracker: newAssumptionTrackerState(),
	}
	defer a.assumptionTracker.reset()

	text := `I assume A. I assume B. I assume C. I assume D.`

	// First call should warn
	hint1 := a.maybeWarnAssumptions(text)
	if hint1 == "" {
		t.Fatal("expected first warning")
	}

	// Second call should also warn (max=2)
	hint2 := a.maybeWarnAssumptions(text)
	if hint2 == "" {
		t.Fatal("expected second warning")
	}

	// Third call should NOT warn (exceeded max)
	hint3 := a.maybeWarnAssumptions(text)
	if hint3 != "" {
		t.Error("expected no third warning (max exceeded)")
	}
}

func TestMaybeWarnAssumptions_NilTracker(t *testing.T) {
	a := &Agent{
		assumptionTracker: nil,
	}
	hint := a.maybeWarnAssumptions("I assume X. I assume Y. I assume Z.")
	if hint != "" {
		t.Error("expected empty hint when tracker is nil")
	}
}

func TestAssumptionTrackerState_Reset(t *testing.T) {
	s := newAssumptionTrackerState()
	s.warnings = 5
	s.reset()
	if s.warnings != 0 {
		t.Errorf("expected warnings=0 after reset, got %d", s.warnings)
	}
}

func TestScanAssumptions_HighSeverityFlag(t *testing.T) {
	a := &Agent{
		assumptionTracker: newAssumptionTrackerState(),
	}
	defer a.assumptionTracker.reset()

	// 3+ HIGH assumptions should trigger WARNING severity
	text := `I assume A is correct. I'll assume B is correct. Assuming that C is correct.`
	hint := a.maybeWarnAssumptions(text)
	if hint == "" {
		t.Fatal("expected warning")
	}
	if !strings.Contains(hint, "WARNING") {
		t.Errorf("expected WARNING severity for 3+ HIGH, got: %s", hint)
	}
}
