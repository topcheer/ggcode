package agent

import (
	"strings"
	"testing"
)

func TestExtractSubgoals(t *testing.T) {
	// 3+ numbered items should be extracted
	text := `Here's my plan:
1. Fix the auth middleware
2. Update the test suite for auth
3. Add documentation for the new API
4. Update the changelog`
	subs := extractSubgoals(text)
	if len(subs) < 3 {
		t.Fatalf("expected at least 3 subgoals, got %d", len(subs))
	}
	if subs[0].number != 1 {
		t.Errorf("first subgoal number = %d, want 1", subs[0].number)
	}
	if len(subs[0].keywords) == 0 {
		t.Error("expected non-empty keywords for first subgoal")
	}
}

func TestExtractSubgoalsTooFew(t *testing.T) {
	// Only 2 items should not trigger
	text := "1. Do thing A\n2. Do thing B"
	subs := extractSubgoals(text)
	if subs != nil {
		t.Fatalf("expected nil for <3 items, got %d", len(subs))
	}
}

func TestSubgoalRecordAndWarn(t *testing.T) {
	s := newSubgoalState()
	// Record a plan with 4 subgoals
	planText := `Plan:
1. Fix auth middleware
2. Update test suite
3. Add API documentation
4. Update changelog`
	s.recordAssistantText(planText, 1)
	if len(s.subgoals) < 3 {
		t.Fatalf("expected >=3 subgoals tracked, got %d", len(s.subgoals))
	}

	// Simulate tool calls addressing some subgoals
	s.recordToolCall("edit_file", `{"path": "auth/middleware.go"}`)
	s.recordToolCall("edit_file", `{"path": "auth_test.go"}`)
	// Subgoals 3 and 4 unaddressed

	// Need sgMinIterGap iterations before warning
	warn := s.maybeWarn(1 + sgMinIterGap)
	if warn == "" {
		t.Fatal("expected warning for unaddressed subgoals")
	}
	if !strings.Contains(warn, "Subgoal Completion") {
		t.Errorf("warning missing expected prefix: %s", warn)
	}

	// Should fire only once
	warn2 := s.maybeWarn(1 + sgMinIterGap + 5)
	if warn2 != "" {
		t.Error("expected no second warning (maxWarns=1)")
	}
}

func TestSubgoalAllAddressed(t *testing.T) {
	s := newSubgoalState()
	planText := `Plan:
1. Fix auth middleware
2. Update test suite
3. Add API documentation`
	s.recordAssistantText(planText, 1)
	s.recordToolCall("edit_file", `{"path": "auth/middleware.go"}`)
	s.recordToolCall("edit_file", `{"path": "test_suite.go"}`)
	s.recordToolCall("edit_file", `{"path": "api_documentation.md"}`)

	warn := s.maybeWarn(1 + sgMinIterGap)
	if warn != "" {
		t.Errorf("expected no warning when all addressed, got: %s", warn)
	}
}

func TestSubgoalReset(t *testing.T) {
	s := newSubgoalState()
	s.recordAssistantText("1. A\n2. B\n3. C", 1)
	s.reset()
	if len(s.subgoals) != 0 {
		t.Error("expected empty subgoals after reset")
	}
}

func TestExtractSubgoalKeywords(t *testing.T) {
	kws := extractSubgoalKeywords("Fix the auth middleware")
	// Should include "auth" and "middleware" but not stop words
	has := func(w string) bool {
		for _, k := range kws {
			if k == w {
				return true
			}
		}
		return false
	}
	if !has("auth") {
		t.Error("expected keyword 'auth'")
	}
	if !has("middleware") {
		t.Error("expected keyword 'middleware'")
	}
}
