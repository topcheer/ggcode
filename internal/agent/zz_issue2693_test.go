package agent

// #2693: checkToolCall must only evaluate mutating tools against self-declared
// constraints. cvExtractPath keys on "path"/"file_path", which read-only tools
// (read_file, grep, glob) also use -- before the sourceMutatingTools gate,
// harmless exploration (1) falsely accused the agent of violating its own
// boundary and (2) exhausted the cvMaxWarnings=2 quota so the REAL
// out-of-scope edit that followed was silently suppressed.

import "testing"

// TestIssue2693ReadOnlyToolsDoNotTrigger: after a scope declaration, ordinary
// read_file/grep exploration outside the scope must NOT produce a violation
// warning -- reading is not modifying.
func TestIssue2693ReadOnlyToolsDoNotTrigger(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in the auth directory", 1)

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"read_file", map[string]any{"path": "internal/config/config.go"}},
		{"grep", map[string]any{"path": "internal/config/"}},
		{"glob", map[string]any{"pattern": "**/*.go"}},
		{"lsp_diagnostics", map[string]any{"path": "internal/config/config.go"}},
		{"list_directory", map[string]any{"path": "internal/config"}},
	} {
		if msg := s.checkToolCall(tc.tool, tc.args, 2); msg != "" {
			t.Errorf("%s (read-only) must not trigger constraint violation, got: %s", tc.tool, msg)
		}
	}
	if s.warnings != 0 {
		t.Errorf("read-only calls must not consume warning quota, warnings=%d", s.warnings)
	}
}

// TestIssue2693QuotaPreservedForRealViolation: the issue's core harm -- two
// harmless reads exhausted cvMaxWarnings=2 so the genuine out-of-scope
// edit_file was suppressed. After the fix the real edit must still warn.
func TestIssue2693QuotaPreservedForRealViolation(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in the auth directory", 1)

	// Harmless exploration first (previously consumed both warnings).
	_ = s.checkToolCall("read_file", map[string]any{"path": "internal/config/config.go"}, 2)
	_ = s.checkToolCall("read_file", map[string]any{"path": "internal/tool/tool.go"}, 3)

	// The real violation: an edit outside the declared scope must still fire.
	if msg := s.checkToolCall("edit_file", map[string]any{"file_path": "internal/config/config.go"}, 4); msg == "" {
		t.Fatal("real out-of-scope edit_file must trigger violation warning after read-only calls")
	}
}

// TestIssue2693MutatingToolsStillChecked: the gate must not over-suppress --
// every canonical mutating tool with a path arg still gets checked.
func TestIssue2693MutatingToolsStillChecked(t *testing.T) {
	for _, tool := range []string{"edit_file", "write_file", "multi_edit_file", "multi_file_edit", "multi_file_write", "batch_replace", "lsp_rename"} {
		s := newConstraintViolationState()
		s.recordReasoning("I'll only modify files in the auth directory", 1)
		if msg := s.checkToolCall(tool, map[string]any{"file_path": "internal/config/config.go"}, 2); msg == "" {
			t.Errorf("%s (mutating) targeting out-of-scope path must trigger warning", tool)
		}
	}
}
