package agent

import (
	"strings"
	"testing"
)

func TestExtractConstraints_NegationConstraints(t *testing.T) {
	text := "Don't modify the auth module. Also do not add any new dependencies."
	results := extractConstraints(text)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 constraints, got %d: %v", len(results), results)
	}
	// At least one should mention auth
	found := false
	for _, r := range results {
		if strings.Contains(strings.ToLower(r), "auth") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a constraint mentioning 'auth', got: %v", results)
	}
}

func TestExtractConstraints_ExclusivityConstraints(t *testing.T) {
	text := "Only use the repository pattern for data access. Must use PostgreSQL."
	results := extractConstraints(text)
	if len(results) < 1 {
		t.Fatalf("expected at least 1 constraint, got %d: %v", len(results), results)
	}
}

func TestExtractConstraints_ScopeRestrictions(t *testing.T) {
	text := "Don't touch the config files. Leave the migrations alone."
	results := extractConstraints(text)
	if len(results) < 1 {
		t.Fatalf("expected at least 1 scope restriction, got %d: %v", len(results), results)
	}
}

func TestExtractConstraints_EmptyText(t *testing.T) {
	results := extractConstraints("")
	if results != nil {
		t.Errorf("expected nil for empty text, got %v", results)
	}
}

func TestExtractConstraints_NoFalsePositives(t *testing.T) {
	text := "Please implement the user registration feature with email validation."
	results := extractConstraints(text)
	// This text has no explicit constraints - should return few or zero
	if len(results) > 1 {
		t.Errorf("expected at most 1 match for non-constraint text, got %d: %v", len(results), results)
	}
}

func TestExtractConstraints_Deduplication(t *testing.T) {
	text := "Don't modify auth.js. Don't modify auth.js again."
	results := extractConstraints(text)
	// Should deduplicate near-identical constraints
	if len(results) > 1 {
		t.Errorf("expected deduplication, got %d: %v", len(results), results)
	}
}

func TestConstraintAmnesiaState_RecordConstraints(t *testing.T) {
	s := newConstraintAmnesiaState()
	s.recordConstraints("Don't touch the auth module and only use PostgreSQL.", 1)
	if len(s.constraints) < 1 {
		t.Fatalf("expected at least 1 tracked constraint, got %d", len(s.constraints))
	}
	if s.constraints[0].messageIdx != 1 {
		t.Errorf("expected messageIdx=1, got %d", s.constraints[0].messageIdx)
	}
}

func TestConstraintAmnesiaState_MaxTracked(t *testing.T) {
	s := newConstraintAmnesiaState()
	// Add many constraints
	for i := 0; i < 20; i++ {
		s.recordConstraints("Don't modify file"+string(rune('A'+i)), 1)
	}
	if len(s.constraints) > constraintMaxTracked {
		t.Errorf("expected max %d tracked, got %d", constraintMaxTracked, len(s.constraints))
	}
}

func TestConstraintAmnesiaState_MaybeWarn_NoConstraints(t *testing.T) {
	s := newConstraintAmnesiaState()
	msg := s.maybeWarn(20)
	if msg != "" {
		t.Errorf("expected empty warning with no constraints, got: %s", msg)
	}
}

func TestConstraintAmnesiaState_MaybeWarn_TooEarly(t *testing.T) {
	s := newConstraintAmnesiaState()
	s.recordConstraints("Don't modify the auth module", 1)
	msg := s.maybeWarn(5)
	if msg != "" {
		t.Errorf("expected no warning at iteration 5 (too early), got: %s", msg)
	}
}

func TestConstraintAmnesiaState_MaybeWarn_FiresAfterThreshold(t *testing.T) {
	s := newConstraintAmnesiaState()
	s.recordConstraints("Don't modify the auth module", 1)
	msg := s.maybeWarn(constraintReminderMinIterations + 1)
	if msg == "" {
		t.Error("expected warning after threshold, got empty string")
	}
	if !strings.Contains(msg, "constraint-reminder") {
		t.Errorf("expected constraint-reminder in message, got: %s", msg)
	}
	if !strings.Contains(msg, "auth module") {
		t.Errorf("expected constraint text in reminder, got: %s", msg)
	}
}

func TestConstraintAmnesiaState_MaybeWarn_MaxWarnings(t *testing.T) {
	s := newConstraintAmnesiaState()
	s.recordConstraints("Don't modify the auth module", 1)
	// First warning
	msg1 := s.maybeWarn(constraintReminderMinIterations + 1)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	// Second warning is suppressed (1 per run, batch 2 guidance-noise cleanup)
	msg2 := s.maybeWarn(constraintReminderMinIterations + 5)
	if msg2 != "" {
		t.Fatalf("expected second warning to be suppressed, got: %s", msg2)
	}
	// Third should also be suppressed
	msg3 := s.maybeWarn(constraintReminderMinIterations + 10)
	if msg3 != "" {
		t.Errorf("expected third warning to be suppressed, got: %s", msg3)
	}
}

func TestConstraintAmnesiaState_Reset(t *testing.T) {
	s := newConstraintAmnesiaState()
	s.recordConstraints("Don't modify auth", 1)
	s.warnings = 2
	s.reset()
	if len(s.constraints) != 0 {
		t.Errorf("expected constraints cleared after reset, got %d", len(s.constraints))
	}
	if s.warnings != 0 {
		t.Errorf("expected warnings cleared after reset, got %d", s.warnings)
	}
}

func TestConstraintAmnesiaState_MultipleConstraints(t *testing.T) {
	s := newConstraintAmnesiaState()
	s.recordConstraints("Don't modify the auth module. Only use PostgreSQL. Avoid touching the config files.", 1)
	msg := s.maybeWarn(constraintReminderMinIterations + 1)
	if msg == "" {
		t.Fatal("expected warning")
	}
	// Should list all tracked constraints
	for _, c := range s.constraints {
		if !strings.Contains(msg, c.excerpt) {
			t.Errorf("expected constraint '%s' in message", c.excerpt)
		}
	}
}

func TestConstraintAmnesiaState_MaybeWarn_NilSafe(t *testing.T) {
	var s *constraintAmnesiaState
	// Should not panic on nil receiver
	msg := s.maybeWarn(20)
	if msg != "" {
		t.Errorf("expected empty for nil state, got: %s", msg)
	}
}

func TestConstraintAmnesiaState_RecordConstraints_NilSafe(t *testing.T) {
	var s *constraintAmnesiaState
	// Should not panic on nil receiver
	s.recordConstraints("Don't modify auth", 1)
}
