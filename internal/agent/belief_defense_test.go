package agent

import (
	"strings"
	"testing"
)

func TestBeliefDefense_RootCauseEscalation(t *testing.T) {
	s := newBeliefDefenseState()

	// Iteration 1: agent states a root cause belief.
	hint := s.recordAssistantText("The bug is in the auth middleware. The issue is caused by a missing token check.", 1)
	if hint != "" {
		t.Fatalf("expected no warning on first belief statement, got: %s", hint)
	}

	// Iteration 2: tool output contradicts the belief (error elsewhere).
	s.recordToolResult("run_command", "auth middleware not found\nno such directory", 2, false)

	// Iteration 3: agent re-states the same root cause.
	hint = s.recordAssistantText("The bug is in the auth middleware, specifically the token check.", 3)
	if hint == "" {
		t.Fatal("expected belief defense escalation warning after contradiction + re-statement")
	}
	if !strings.Contains(hint, "Belief Defense Escalation") {
		t.Fatalf("hint should contain detector name, got: %s", hint)
	}
	if !strings.Contains(hint, "arXiv:2606.22936") {
		t.Fatalf("hint should cite the paper, got: %s", hint)
	}
}

func TestBeliefDefense_StatusEscalation(t *testing.T) {
	s := newBeliefDefenseState()

	// Iteration 1: agent claims tests pass.
	s.recordAssistantText("All tests pass. The build is clean.", 1)

	// Iteration 2: test output shows failures.
	s.recordToolResult("run_command", "--- FAIL: TestAuth\nFAILED", 2, false)

	// Iteration 3: agent re-claims tests pass.
	hint := s.recordAssistantText("All tests pass now, everything is green.", 3)
	if hint == "" {
		t.Fatal("expected escalation warning for status belief after test failure")
	}
}

func TestBeliefDefense_NoEscalationWithoutContradiction(t *testing.T) {
	s := newBeliefDefenseState()

	// Iteration 1: agent states belief.
	s.recordAssistantText("The issue is the race condition in the worker pool.", 1)

	// Iteration 2: no contradicting output (success).
	s.recordToolResult("run_command", "all good, no issues found", 2, false)

	// Iteration 3: agent re-states -- but no contradiction occurred.
	hint := s.recordAssistantText("The issue is the race condition in the worker pool.", 3)
	if hint != "" {
		t.Fatalf("expected no warning without contradicting evidence, got: %s", hint)
	}
}

func TestBeliefDefense_NoEscalationWhenUpdated(t *testing.T) {
	s := newBeliefDefenseState()

	// Iteration 1: agent states belief.
	s.recordAssistantText("The bug is in the auth middleware.", 1)

	// Iteration 2: contradiction.
	s.recordToolResult("run_command", "auth middleware not found", 2, false)

	// Iteration 3: agent UPDATES belief (different root cause).
	hint := s.recordAssistantText("The bug is actually in the config loader, not the auth.", 3)
	if hint != "" {
		t.Fatalf("expected no warning when agent updates belief, got: %s", hint)
	}
}

func TestBeliefDefense_MaxWarnings(t *testing.T) {
	s := newBeliefDefenseState()

	// Seed + contradict + re-state twice.
	s.recordAssistantText("The issue is the database connection pool.", 1)
	s.recordToolResult("run_command", "not found error", 2, false)
	hint1 := s.recordAssistantText("The issue is the database connection pool.", 3)
	if hint1 == "" {
		t.Fatal("expected first warning")
	}

	s.recordToolResult("run_command", "still not found", 4, false)
	hint2 := s.recordAssistantText("The issue is the database connection pool.", 5)
	if hint2 == "" {
		t.Fatal("expected second warning")
	}

	// Third escalation should be suppressed.
	s.recordToolResult("run_command", "not found again", 6, false)
	hint3 := s.recordAssistantText("The issue is the database connection pool.", 7)
	if hint3 != "" {
		t.Fatalf("expected third warning to be suppressed (max=2), got: %s", hint3)
	}
}

func TestBeliefDefense_ExistenceEscalation(t *testing.T) {
	s := newBeliefDefenseState()

	// Iteration 1: agent claims a function exists.
	s.recordAssistantText("The function processQueue is defined in the handler.", 1)

	// Iteration 2: search shows no match.
	s.recordToolResult("grep", "0 matches found", 2, false)

	// Iteration 3: agent re-asserts existence.
	hint := s.recordAssistantText("The function processQueue is defined there, let me check.", 3)
	if hint == "" {
		t.Fatal("expected escalation for existence belief after no-match")
	}
}

func TestBeliefDefense_Reset(t *testing.T) {
	s := newBeliefDefenseState()
	s.recordAssistantText("The bug is in the auth middleware.", 1)
	s.recordToolResult("run_command", "not found", 2, false)
	s.recordAssistantText("The bug is in the auth middleware.", 3)

	if s.warnings == 0 {
		t.Fatal("expected at least one warning before reset")
	}
	if len(s.seeds) == 0 {
		t.Fatal("expected seeds before reset")
	}

	s.reset()
	if s.warnings != 0 || len(s.seeds) != 0 || len(s.contradictions) != 0 {
		t.Fatal("reset should clear all state")
	}
}

func TestBeliefDefense_SeedDeduplication(t *testing.T) {
	s := newBeliefDefenseState()
	// Same text in same iteration should not create duplicate seeds.
	s.recordAssistantText("The issue is the database connection pool.", 1)
	count1 := len(s.seeds)
	s.recordAssistantText("The issue is the database connection pool.", 1)
	count2 := len(s.seeds)
	if count2 != count1 {
		t.Fatalf("dedup within same iteration failed: %d -> %d", count1, count2)
	}
}

func TestBeliefDefense_NormalizeBeliefKey(t *testing.T) {
	cases := []struct {
		input    string
		expected string
		empty    bool
	}{
		{"the auth middleware", "auth middleware", false},
		{"", "", true},
		{"ab", "", true}, // too short
		{"This is a very long belief that exceeds the maximum allowed length for normalization and should be truncated to empty because it is way too long to be useful as a belief key", "", true},
	}
	for _, c := range cases {
		got := normalizeBeliefKey(c.input)
		if c.empty && got != "" {
			t.Errorf("normalizeBeliefKey(%q) = %q, want empty", c.input, got)
		}
		if !c.empty && got != c.expected {
			t.Errorf("normalizeBeliefKey(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestBeliefDefense_ContradictionTemporalOrdering(t *testing.T) {
	s := newBeliefDefenseState()

	// Contradiction comes BEFORE the belief seed (temporal order wrong).
	s.recordToolResult("run_command", "not found error", 1, false)
	s.recordAssistantText("The issue is the auth middleware.", 2)

	// Re-state: contradiction was BEFORE the seed, so no escalation.
	hint := s.recordAssistantText("The issue is the auth middleware.", 3)
	if hint != "" {
		t.Fatalf("should not escalate when contradiction precedes seed, got: %s", hint)
	}
}
