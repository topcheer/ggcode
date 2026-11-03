package agent

import "testing"

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

func TestIssue2693QuotaPreservedForRealViolation(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I'll only modify files in the auth directory", 1)

	_ = s.checkToolCall("read_file", map[string]any{"path": "internal/config/config.go"}, 2)
	_ = s.checkToolCall("read_file", map[string]any{"path": "internal/tool/tool.go"}, 3)

	if msg := s.checkToolCall("edit_file", map[string]any{"file_path": "internal/config/config.go"}, 4); msg == "" {
		t.Fatal("real out-of-scope edit_file must trigger violation warning after read-only calls")
	}
}

func TestIssue2693MutatingToolsStillChecked(t *testing.T) {
	for _, tool := range []string{"edit_file", "write_file", "multi_edit_file", "multi_file_edit", "multi_file_write", "batch_replace", "lsp_rename"} {
		s := newConstraintViolationState()
		s.recordReasoning("I'll only modify files in the auth directory", 1)
		if msg := s.checkToolCall(tool, map[string]any{"file_path": "internal/config/config.go"}, 2); msg == "" {
			t.Errorf("%s (mutating) targeting out-of-scope path must trigger warning", tool)
		}
	}
}
