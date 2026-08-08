package agent

import (
	"testing"
)

func TestCVExtractConstraints_ScopeDeclaration(t *testing.T) {
	text := "I'll only modify files in the auth/ directory for this task."
	constraints := cvExtractConstraints(text, 1)
	if len(constraints) == 0 {
		t.Fatal("expected at least 1 constraint, got 0")
	}
	found := false
	for _, c := range constraints {
		if c.constraintT == "scope" {
			found = true
			if c.pattern == "" {
				t.Errorf("scope constraint has empty pattern")
			}
		}
	}
	if !found {
		t.Errorf("expected a scope constraint, got: %+v", constraints)
	}
}

func TestCVExtractConstraints_AvoidDeclaration(t *testing.T) {
	text := "I should not touch the database/ schema files."
	constraints := cvExtractConstraints(text, 2)
	found := false
	for _, c := range constraints {
		if c.constraintT == "avoid" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an avoid constraint, got: %+v", constraints)
	}
}

func TestCVCheckViolation_AvoidPattern(t *testing.T) {
	c := cvConstraint{
		constraintT: "avoid",
		pattern:     "database",
	}
	violated, _ := cvCheckViolation(c, "internal/database/schema.go")
	if !violated {
		t.Error("expected violation for avoid pattern matching")
	}
	// Non-matching path should not violate
	violated2, _ := cvCheckViolation(c, "internal/auth/login.go")
	if violated2 {
		t.Error("expected no violation for non-matching path")
	}
}

func TestCVCheckViolation_ScopePattern(t *testing.T) {
	c := cvConstraint{
		constraintT: "scope",
		pattern:     "auth",
	}
	// Path within scope - no violation
	violated, _ := cvCheckViolation(c, "internal/auth/login.go")
	if violated {
		t.Error("expected no violation for path within scope")
	}
	// Path outside scope - violation
	violated2, _ := cvCheckViolation(c, "internal/database/schema.go")
	if !violated2 {
		t.Error("expected violation for path outside scope")
	}
}

func TestCVPathMatchesPattern(t *testing.T) {
	tests := []struct {
		path, pattern string
		want          bool
	}{
		{"internal/auth/login.go", "auth", true},
		{"internal/auth/login.go", "internal/auth", true},
		{"internal/database/db.go", "auth", false},
		{"config/settings.go", "config", true},
		{"", "auth", false},
		{"internal/auth/", "", false},
	}
	for _, tt := range tests {
		got := cvPathMatchesPattern(tt.path, tt.pattern)
		if got != tt.want {
			t.Errorf("cvPathMatchesPattern(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}

func TestConstraintViolation_RecordAndCheck(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in the auth/ directory.", 1)

	// Tool call within scope - no warning
	args := map[string]any{"file_path": "internal/auth/login.go"}
	msg := s.checkToolCall("edit_file", args, 2)
	if msg != "" {
		t.Errorf("expected no warning for in-scope edit, got: %s", msg)
	}

	// Tool call outside scope - should warn
	args2 := map[string]any{"file_path": "internal/database/schema.go"}
	msg2 := s.checkToolCall("edit_file", args2, 3)
	if msg2 == "" {
		t.Error("expected warning for out-of-scope edit")
	}
}

func TestConstraintViolation_AvoidConstraint(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I should not touch the test files.", 1)

	args := map[string]any{"file_path": "internal/agent/foo_test.go"}
	msg := s.checkToolCall("edit_file", args, 2)
	if msg == "" {
		t.Error("expected warning for editing avoided test file")
	}
}

func TestConstraintViolation_MaxWarnings(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in auth/.", 1)

	args := map[string]any{"file_path": "internal/database/db.go"}
	// First violation
	msg1 := s.checkToolCall("edit_file", args, 2)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	// Second violation
	msg2 := s.checkToolCall("edit_file", args, 3)
	if msg2 == "" {
		t.Fatal("expected second warning")
	}
	// Third should be suppressed
	msg3 := s.checkToolCall("edit_file", args, 4)
	if msg3 != "" {
		t.Error("expected third warning to be suppressed (max reached)")
	}
}

func TestConstraintViolation_Reset(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify auth/.", 1)
	if len(s.constraints) == 0 {
		t.Fatal("expected constraints after record")
	}
	s.reset()
	if len(s.constraints) != 0 {
		t.Errorf("expected 0 constraints after reset, got %d", len(s.constraints))
	}
	if s.warnings != 0 {
		t.Errorf("expected 0 warnings after reset, got %d", s.warnings)
	}
}

func TestConstraintViolation_NoConstraintsNoWarning(t *testing.T) {
	s := newConstraintViolationState()
	args := map[string]any{"file_path": "any/path.go"}
	msg := s.checkToolCall("edit_file", args, 1)
	if msg != "" {
		t.Errorf("expected no warning when no constraints tracked")
	}
}

func TestConstraintViolation_DedupConstraints(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify auth/.", 1)
	s.recordReasoning("I'll only modify auth/.", 2)
	if len(s.constraints) > 1 {
		t.Errorf("expected dedup, got %d constraints", len(s.constraints))
	}
}

func TestCVExtractPathFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"file_path", map[string]any{"file_path": "foo/bar.go"}, "foo/bar.go"},
		{"path", map[string]any{"path": "baz/qux.go"}, "baz/qux.go"},
		{"source", map[string]any{"source": "src/a.go"}, "src/a.go"},
		{"missing", map[string]any{"other": "val"}, ""},
	}
	for _, tt := range tests {
		got := cvExtractPath(tt.args)
		if got != tt.want {
			t.Errorf("cvExtractPath(%v) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestParseToolArgs(t *testing.T) {
	// Valid JSON
	args := parseToolArgs([]byte(`{"file_path": "test.go"}`))
	if args["file_path"] != "test.go" {
		t.Errorf("expected file_path=test.go, got %v", args["file_path"])
	}
	// Invalid JSON returns empty map
	args2 := parseToolArgs([]byte(`invalid`))
	if len(args2) != 0 {
		t.Errorf("expected empty map for invalid JSON, got %v", args2)
	}
}

func TestCVExtractConstraints_NoConstraints(t *testing.T) {
	text := "Let me look at the code and understand the structure."
	constraints := cvExtractConstraints(text, 1)
	if len(constraints) != 0 {
		t.Errorf("expected 0 constraints from neutral text, got %d", len(constraints))
	}
}
