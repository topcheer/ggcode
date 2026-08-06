package agent

import (
	"strings"
	"testing"
)

func TestScanScopeCreep_BenignText(t *testing.T) {
	// Text that stays within scope - should not trigger.
	texts := []string{
		"I have completed the analysis using the three databases you specified. Here are the results.",
		"The sales data for Q4 shows a 15% increase. Here is the formatted report.",
		"Should I also check the Q3 data for comparison, or just focus on Q4?",
		"I noticed an anomaly in the dataset during processing. I'm flagging this for your review.",
	}
	for _, text := range texts {
		hits := scanScopeCreep(text)
		if len(hits) > 0 {
			t.Errorf("benign text triggered scope creep: %q -> %d hits", text, len(hits))
		}
	}
}

func TestScanScopeCreep_WhileImAtIt(t *testing.T) {
	text := "While I'm at it, let me also refactor the authentication module to use the new pattern."
	hits := scanScopeCreep(text)
	if len(hits) == 0 {
		t.Fatal("expected scope creep signals for 'while I'm at it' text")
	}
	found := false
	for _, h := range hits {
		if strings.Contains(strings.ToLower(h.excerpt), "while") {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'while I'm at it' pattern in hits")
	}
}

func TestScanScopeCreep_GoneAhead(t *testing.T) {
	text := "I've gone ahead and updated all the test files to match the new API signatures."
	hits := scanScopeCreep(text)
	if len(hits) == 0 {
		t.Fatal("expected scope creep signals for 'I've gone ahead' text")
	}
}

func TestScanScopeCreep_TakenLiberty(t *testing.T) {
	text := "I've taken the liberty to clean up the imports and reformat everything."
	hits := scanScopeCreep(text)
	if len(hits) == 0 {
		t.Fatal("expected scope creep signals for 'taken the liberty' text")
	}
}

func TestScanScopeCreep_Justification(t *testing.T) {
	text := "To fully fix this, I also need to update the migration scripts and the config files."
	hits := scanScopeCreep(text)
	if len(hits) == 0 {
		t.Fatal("expected scope creep signals for justification pattern")
	}
}

func TestScanScopeCreep_NoticedOtherIssues(t *testing.T) {
	// "Noticed other issues" justification pattern.
	text := "I noticed some other issues that I also fixed."
	hits := scanScopeCreep(text)
	if len(hits) < 1 {
		t.Fatalf("expected at least 1 scope creep signal, got %d", len(hits))
	}
	found := false
	for _, h := range hits {
		if h.category == "justification" {
			found = true
		}
	}
	if !found {
		t.Error("expected justification category hit")
	}
}

func TestScanScopeCreep_SinceAlreadyHere(t *testing.T) {
	text := "Since I'm already in this file, let me also fix the variable naming."
	hits := scanScopeCreep(text)
	if len(hits) == 0 {
		t.Fatal("expected scope creep signals for 'since I'm already here' pattern")
	}
}

func TestScanScopeCreep_ExpandingScope(t *testing.T) {
	text := "I'm expanding my scope to include the database layer as well."
	hits := scanScopeCreep(text)
	if len(hits) == 0 {
		t.Fatal("expected scope creep signal for 'expanding my scope' pattern")
	}
}

func TestScanScopeCreep_EmptyText(t *testing.T) {
	hits := scanScopeCreep("")
	if hits != nil {
		t.Error("expected nil hits for empty text")
	}
}

func TestMaybeWarnScopeCreep_BelowThreshold(t *testing.T) {
	a := &Agent{scopeCreep: newScopeCreepState()}
	// Single signal - below threshold of 2.
	text := "While I'm at it, this is one signal."
	msg := a.maybeWarnScopeCreep(text)
	if msg != "" {
		t.Error("expected no warning below threshold")
	}
}

func TestMaybeWarnScopeCreep_AtThreshold(t *testing.T) {
	a := &Agent{scopeCreep: newScopeCreepState()}
	// Two signals in one text.
	text := "While I'm at it, let me also clean up the imports. I've gone ahead and reformatted everything too."
	msg := a.maybeWarnScopeCreep(text)
	if msg == "" {
		t.Fatal("expected warning at threshold")
	}
	if !strings.Contains(msg, "[scope-creep]") {
		t.Error("expected [scope-creep] tag in message")
	}
}

func TestMaybeWarnScopeCreep_MaxWarnings(t *testing.T) {
	a := &Agent{scopeCreep: newScopeCreepState()}
	text := "While I'm at it, let me also fix this. I've gone ahead and cleaned up the imports too."
	// First two warnings should fire.
	msg1 := a.maybeWarnScopeCreep(text)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	msg2 := a.maybeWarnScopeCreep(text)
	if msg2 == "" {
		t.Fatal("expected second warning")
	}
	// Third should be suppressed.
	msg3 := a.maybeWarnScopeCreep(text)
	if msg3 != "" {
		t.Error("expected no warning after max warnings exceeded")
	}
}

func TestScopeCreepState_Reset(t *testing.T) {
	s := newScopeCreepState()
	s.warnings = 5
	s.reset()
	if s.warnings != 0 {
		t.Error("expected warnings to be 0 after reset")
	}
}

func TestScanScopeCreep_Deduplication(t *testing.T) {
	// Same pattern repeated should not produce duplicate hits.
	text := "While I'm at it, let me fix this. While I'm at it, let me fix that."
	hits := scanScopeCreep(text)
	// The "while I'm at it" pattern should match but excerpts may differ,
	// so we mainly verify it doesn't crash or produce excessive duplicates.
	if len(hits) > 10 {
		t.Errorf("too many hits (dedup not working): %d", len(hits))
	}
}
