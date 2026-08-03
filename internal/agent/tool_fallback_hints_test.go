package agent

import (
	"strings"
	"testing"
)

func TestToolFallbackHint_LSPTimeout(t *testing.T) {
	hint := toolFallbackHint("lsp_definition", "lsp server not ready: timeout")
	if hint == "" {
		t.Fatal("expected hint for LSP timeout")
	}
	if !strings.Contains(hint, "grep") {
		t.Errorf("hint should suggest grep, got: %s", hint)
	}
	if !strings.Contains(hint, "code_search") {
		t.Errorf("hint should suggest code_search, got: %s", hint)
	}
}

func TestToolFallbackHint_LSPNoResults(t *testing.T) {
	hint := toolFallbackHint("lsp_references", "no results found")
	if hint == "" {
		t.Fatal("expected hint for LSP no results")
	}
	if !strings.Contains(hint, "lsp_workspace_symbols") {
		t.Errorf("hint should suggest workspace_symbols, got: %s", hint)
	}
}

func TestToolFallbackHint_GrepEmpty(t *testing.T) {
	hint := toolFallbackHint("grep", "no matches found")
	if hint == "" {
		t.Fatal("expected hint for empty grep")
	}
	if !strings.Contains(hint, "code_search") {
		t.Errorf("hint should suggest code_search, got: %s", hint)
	}
}

func TestToolFallbackHint_GrepTimeout(t *testing.T) {
	hint := toolFallbackHint("grep", "command timed out")
	if hint == "" {
		t.Fatal("expected hint for grep timeout")
	}
	if !strings.Contains(hint, "glob") {
		t.Errorf("hint should suggest glob, got: %s", hint)
	}
}

func TestToolFallbackHint_CodeSearchEmpty(t *testing.T) {
	hint := toolFallbackHint("code_search", "no matching files")
	if hint == "" {
		t.Fatal("expected hint for empty code_search")
	}
	if !strings.Contains(hint, "grep") {
		t.Errorf("hint should suggest grep, got: %s", hint)
	}
}

func TestToolFallbackHint_EditAnchorNotFound(t *testing.T) {
	hint := toolFallbackHint("edit_file", "old_text not found in file")
	if hint == "" {
		t.Fatal("expected hint for edit anchor not found")
	}
	if !strings.Contains(hint, "read_file") {
		t.Errorf("hint should suggest read_file, got: %s", hint)
	}
}

func TestToolFallbackHint_RunCommandTimeout(t *testing.T) {
	hint := toolFallbackHint("run_command", "command timed out after 120s")
	if hint == "" {
		t.Fatal("expected hint for command timeout")
	}
	if !strings.Contains(hint, "start_command") {
		t.Errorf("hint should suggest start_command, got: %s", hint)
	}
}

func TestToolFallbackHint_WebFetchNetwork(t *testing.T) {
	hint := toolFallbackHint("web_fetch", "connection refused")
	if hint == "" {
		t.Fatal("expected hint for web_fetch network error")
	}
	if !strings.Contains(hint, "web_search") {
		t.Errorf("hint should suggest web_search, got: %s", hint)
	}
}

func TestToolFallbackHint_NoHintForSuccess(t *testing.T) {
	// Should not produce hints for empty or irrelevant errors
	hint := toolFallbackHint("read_file", "")
	if hint != "" {
		t.Errorf("expected empty hint for empty error, got: %s", hint)
	}
	hint = toolFallbackHint("", "some error")
	if hint != "" {
		t.Errorf("expected empty hint for empty tool name, got: %s", hint)
	}
}

func TestToolFallbackHint_NoHintForUnknownTool(t *testing.T) {
	hint := toolFallbackHint("some_unknown_tool", "some error")
	if hint != "" {
		t.Errorf("expected empty hint for unknown tool, got: %s", hint)
	}
}

func TestToolFallbackHint_ReadFilePermission(t *testing.T) {
	hint := toolFallbackHint("read_file", "permission denied: /etc/shadow")
	if hint == "" {
		t.Fatal("expected hint for permission denied")
	}
	if !strings.Contains(hint, "cat") {
		t.Errorf("hint should suggest cat, got: %s", hint)
	}
}

func TestIsLSPTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"lsp_hover", true},
		{"lsp_definition", true},
		{"lsp_references", true},
		{"lsp_symbols", true},
		{"grep", false},
		{"read_file", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLSPTool(tt.name); got != tt.want {
				t.Errorf("isLSPTool(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
