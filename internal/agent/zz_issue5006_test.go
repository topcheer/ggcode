package agent

import "testing"

// #5006: cvExtractPathAfter's directory-suffix fallback loop (formerly after
// the token-terminating scan) was unreachable because that scan cuts at the
// first whitespace, so rest[:end] could never contain the leading space of
// " module"/" directory"/" layer"/" package"/" folder". Constraints phrased as
// "X module" with X outside the moduleKeywords list were silently dropped.

func TestIssue5006ExtractPathAfterDirectorySuffixes(t *testing.T) {
	tests := []struct {
		name   string
		lower  string
		offset int
		want   string
	}{
		{"billing module", "i will only modify the billing module.", len("i will only modify"), "billing"},
		{"billing directory", "i will only modify the billing directory.", len("i will only modify"), "billing"},
		{"billing layer", "i will only modify the billing layer.", len("i will only modify"), "billing"},
		{"billing package", "i will only modify the billing package.", len("i will only modify"), "billing"},
		{"billing folder", "i will only modify the billing folder.", len("i will only modify"), "billing"},
		{"avoid billing module", "i should not touch the billing module.", len("i should not touch"), "billing"},
		{"multiword phrase takes final word", "only modify the new billing module", len("only modify"), "billing"},
		{"keyword suffix still works", "i will only modify the config directory.", len("i will only modify"), "config"},
		{"trailing punctuation", "only modify the billing module.", len("only modify"), "billing"},
	}
	for _, tt := range tests {
		got := cvExtractPathAfter(tt.lower, tt.offset)
		if got != tt.want {
			t.Errorf("%s: cvExtractPathAfter(%q, %d) = %q, want %q", tt.name, tt.lower, tt.offset, got, tt.want)
		}
	}
}

func TestIssue5006ScopeConstraintFromModulePhrase(t *testing.T) {
	constraints := cvExtractConstraints("I will only modify the billing module.", 1)
	found := false
	for _, c := range constraints {
		if c.constraintT == "scope" && c.pattern == "billing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected scope constraint pattern 'billing' from 'the billing module', got: %+v", constraints)
	}
}

func TestIssue5006ScopeViolationWarnsOnOutOfModuleEdit(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I will only modify the billing module.", 1)

	inScope := s.checkToolCall("edit_file", map[string]any{"file_path": "internal/billing/invoice.go"}, 2)
	if inScope != "" {
		t.Errorf("expected no warning for in-scope billing edit, got: %s", inScope)
	}

	outOfScope := s.checkToolCall("edit_file", map[string]any{"file_path": "internal/database/schema.go"}, 3)
	if outOfScope == "" {
		t.Error("expected warning for out-of-scope edit after declaring 'only modify the billing module'")
	}
}

func TestIssue5006AvoidConstraintFromModulePhrase(t *testing.T) {
	s := newConstraintViolationState()
	s.recordReasoning("I should not touch the billing module.", 1)

	msg := s.checkToolCall("edit_file", map[string]any{"file_path": "internal/billing/invoice.go"}, 2)
	if msg == "" {
		t.Error("expected warning for editing avoided 'billing module'")
	}
}

func TestIssue5006ControlCasesUnchanged(t *testing.T) {
	cases := []struct {
		text  string
		ctype string
		want  string
	}{
		{"I will only modify the config directory.", "scope", "config"},
		{"I will only modify internal/agent files.", "scope", "internal/agent"},
		{"I'll only modify files in the auth/ directory for this task.", "scope", "auth/"},
	}
	for _, tc := range cases {
		constraints := cvExtractConstraints(tc.text, 1)
		found := false
		for _, c := range constraints {
			if c.constraintT == tc.ctype && c.pattern == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: expected %s constraint %q, got: %+v", tc.text, tc.ctype, tc.want, constraints)
		}
	}

	if got := cvExtractPathAfter("i will avoid changing config files", len("i will avoid changing")); got != "config" {
		t.Errorf("files-suffix strip regression: got %q, want %q", got, "config")
	}
}
