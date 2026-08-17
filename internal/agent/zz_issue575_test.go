package agent

import (
	"testing"
)

// TestIssue575_ConfigSyntaxRegistered verifies that config-syntax check
// is registered in allChecks (Bug A: dead code - never called).
func TestIssue575_ConfigSyntaxRegistered(t *testing.T) {
	registerAllChecks()
	found := false
	for _, check := range allChecks {
		if check.Name == "config-syntax" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("config-syntax check not registered in allChecks")
	}
}

// TestIssue575_UnclosedBlockComment verifies that unclosed /* */ block
// comments in JSONC files are detected (Bug A.1: NOFLAG probe verified
// that "{ /* unclosed\n\"x\": 1 }" was stripped to valid "{}").
func TestIssue575_UnclosedBlockComment(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name:    "valid JSONC with closed block comment",
			content: `{ "x": 1 /* comment */, "y": 2 }`,
			wantErr: false,
		},
		{
			name:    "unclosed block comment at end",
			content: `{ /* unclosed\n"x": 1 }`,
			wantErr: true,
		},
		{
			name:    "unclosed block comment in middle",
			content: `{ "x": 1, /* unclosed "y": 2, "z": 3 }`,
			wantErr: true,
		},
		{
			name:    "valid line comment",
			content: `{ "x": 1, // line comment\n"y": 2 }`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripped, err := stripJSONComments(tt.content)
			if tt.wantErr && err == nil {
				t.Errorf("stripJSONComments() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("stripJSONComments() unexpected error: %v", err)
			}
			if !tt.wantErr && stripped == "" {
				t.Errorf("stripJSONComments() returned empty string for valid input")
			}
		})
	}
}

// TestIssue575_YAML_DuplicateKeys verifies that YAML files with duplicate
// mapping keys are detected (Bug A.2: yaml.Node does not check duplicate keys).
func TestIssue575_YAML_DuplicateKeys(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantDup bool
	}{
		{
			name: "valid YAML",
			content: `name: test
value: 123`,
			wantDup: false,
		},
		{
			name: "duplicate keys at root level",
			content: `name: test
name: duplicate`,
			wantDup: true,
		},
		{
			name: "duplicate keys in nested mapping",
			content: `config:
  key: value1
  key: value2`,
			wantDup: true,
		},
		{
			name: "unique keys in nested mappings",
			content: `config:
  key1: value1
  key2: value2`,
			wantDup: false,
		},
		{
			name: "duplicate keys across different sections",
			content: `section1:
  key: value1
section2:
  key: value2`,
			wantDup: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warning := validateYAML("test.yaml", tt.content)
			hasDup := warning != "" && len(warning) > 0
			if tt.wantDup && !hasDup {
				t.Errorf("validateYAML() expected duplicate key warning, got none")
			}
			if !tt.wantDup && hasDup {
				t.Errorf("validateYAML() unexpected warning: %s", warning)
			}
		})
	}
}
