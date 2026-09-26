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

func TestCVExtractConstraints_LeaveAlone(t *testing.T) {
	// "leaving X alone" should be detected as an avoid constraint
	text := "I'll focus on the auth handler, leaving the config module alone."
	constraints := cvExtractConstraints(text, 1)
	found := false
	for _, c := range constraints {
		if c.constraintT == "avoid" && c.pattern == "config module" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected avoid constraint for 'config module', got: %+v", constraints)
	}
}

func TestCVExtractConstraints_LeaveAloneVariant(t *testing.T) {
	// "leave X alone" (without -ing) should also work
	text := "Please leave the test suite alone for now."
	constraints := cvExtractConstraints(text, 1)
	found := false
	for _, c := range constraints {
		if c.constraintT == "avoid" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected avoid constraint from 'leave X alone', got: %+v", constraints)
	}
}

func TestCVExtractConstraints_LeaveAloneViolation(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'm leaving the database alone.", 1)

	args := map[string]any{"file_path": "internal/database/settings.go"}
	msg := s.checkToolCall("edit_file", args, 2)
	if msg == "" {
		t.Error("expected warning for editing file that was promised to be left alone")
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

// TestConstraintViolation_ReadOnlyToolsNotChecked locks the #2693/#2777
// contract: checkToolCall only evaluates mutating tools. Read-only tools
// (read_file, grep, search_files, ...) that carry path/file_path arguments
// must never trigger scope/avoid warnings -- reading a file neither expands
// scope nor touches what the agent said it would avoid, and read-only false
// positives would silently exhaust the cvMaxWarnings quota before a real
// out-of-scope edit lands.
func TestConstraintViolation_ReadOnlyToolsNotChecked(t *testing.T) {
	// #2777 scenario 1: scope declaration followed by an out-of-scope read_file.
	s := newConstraintViolationState()
	s.recordReasoning("I only modify files in the internal/auth directory", 1)
	if msg := s.checkToolCall("read_file", map[string]any{"file_path": "cmd/ggcode/root.go"}, 2); msg != "" {
		t.Errorf("read_file out of declared scope must not warn, got: %s", msg)
	}
	// #2777 scenario 2: grep with an out-of-scope path argument.
	if msg := s.checkToolCall("grep", map[string]any{"path": "internal/config", "pattern": "x"}, 3); msg != "" {
		t.Errorf("grep out of declared scope must not warn, got: %s", msg)
	}
	// Other read-only shapes: multi_file_read, search_files, list_directory, glob.
	for _, tool := range []string{"multi_file_read", "search_files", "list_directory", "glob"} {
		if msg := s.checkToolCall(tool, map[string]any{"path": "internal/config"}, 4); msg != "" {
			t.Errorf("%s must not warn (read-only), got: %s", tool, msg)
		}
	}
	// #2777 scenario 3 (avoid): "leave the database alone" then read schema.
	s2 := newConstraintViolationState()
	s2.recordReasoning("I leave the database alone for this task.", 1)
	if msg := s2.checkToolCall("read_file", map[string]any{"file_path": "internal/database/schema.go"}, 2); msg != "" {
		t.Errorf("read of avoided path must not warn, got: %s", msg)
	}
}

// TestConstraintViolation_ReadOnlyCallsDoNotExhaustQuota locks the flip side
// of #2777: read-only calls must not consume the cvMaxWarnings budget, so a
// real out-of-scope edit after several such reads still warns.
func TestConstraintViolation_ReadOnlyCallsDoNotExhaustQuota(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in auth/.", 1)
	// Two out-of-scope read-only calls -- would exhaust cvMaxWarnings if the
	// gate were missing (#2777 repro).
	if msg := s.checkToolCall("read_file", map[string]any{"file_path": "internal/tool/builtin.go"}, 2); msg != "" {
		t.Fatalf("read_file must not warn, got: %s", msg)
	}
	if msg := s.checkToolCall("grep", map[string]any{"path": "cmd"}, 3); msg != "" {
		t.Fatalf("grep must not warn, got: %s", msg)
	}
	// Real out-of-scope edit must still fire: quota untouched by the reads.
	if msg := s.checkToolCall("edit_file", map[string]any{"file_path": "internal/tool/builtin.go"}, 4); msg == "" {
		t.Fatal("expected warning for genuine out-of-scope edit after read-only calls")
	}
}

// TestConstraintViolation_MutatingToolsStillChecked guards against over-
// filtering: every entry in the canonical sourceMutatingTools map must still
// be evaluated (the #2693 gate uses exactly this map).
func TestConstraintViolation_MutatingToolsStillChecked(t *testing.T) {
	for tool := range sourceMutatingTools {
		s := newConstraintViolationState()
		s.recordReasoning("I'll only modify files in auth/.", 1)
		if msg := s.checkToolCall(tool, map[string]any{"file_path": "internal/tool/builtin.go"}, 2); msg == "" {
			t.Errorf("mutating tool %s out of scope must warn", tool)
		}
	}
}
