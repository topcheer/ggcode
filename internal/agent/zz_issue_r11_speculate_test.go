package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// r11 speculative argument-layer tests (PASTE arXiv:2603.18897):
// name-only bigram predictions previously never executed for tools without
// an argument model — predArgs==nil → skip. These tests pin the expansion.

func TestPredictArgs_EditFileToLspDiagnostics(t *testing.T) {
	prevArgs := json.RawMessage(`{"file_path":"/src/app.go"}`)
	result := predictArgs("lsp_diagnostics", "edit_file", prevArgs)
	if result == nil {
		t.Fatal("expected predicted args for lsp_diagnostics after edit_file")
	}
	var fields map[string]string
	if err := json.Unmarshal(result, &fields); err != nil {
		t.Fatalf("failed to unmarshal predicted args: %v", err)
	}
	if fields["path"] != "/src/app.go" {
		t.Errorf("expected path=/src/app.go, got %s", fields["path"])
	}
}

func TestPredictArgs_WriteFileToLspSymbols(t *testing.T) {
	prevArgs := json.RawMessage(`{"path":"/src/sym.go"}`)
	result := predictArgs("lsp_symbols", "write_file", prevArgs)
	if result == nil {
		t.Fatal("expected predicted args for lsp_symbols after write_file")
	}
	var fields map[string]string
	if err := json.Unmarshal(result, &fields); err != nil {
		t.Fatalf("failed to unmarshal predicted args: %v", err)
	}
	if fields["path"] != "/src/sym.go" {
		t.Errorf("expected path=/src/sym.go, got %s", fields["path"])
	}
}

func TestPredictArgs_NoArgTemplateServesAnyPrev(t *testing.T) {
	// git_status has no linkage pattern, but the constant template must
	// serve it regardless of the previous tool — and without needing
	// prevArgs at all.
	for _, prev := range []string{"edit_file", "run_command", "read_file"} {
		result := predictArgs("git_status", prev, json.RawMessage(`{"irrelevant":true}`))
		if result == nil {
			t.Fatalf("expected template args for git_status after %s", prev)
		}
		if strings.TrimSpace(string(result)) != "{}" {
			t.Errorf("expected {} template for git_status after %s, got %s", prev, result)
		}
	}
	// Same for the other two template tools.
	for _, tool := range []string{"git_branch_list", "git_log"} {
		if got := predictArgs(tool, "edit_file", nil); got == nil {
			t.Errorf("expected template args for %s with nil prevArgs", tool)
		}
	}
}

func TestPredictArgs_LspUnlinkedPrevStillNil(t *testing.T) {
	// Negative control: lsp_diagnostics after a non-linked previous tool
	// (and with no template) must stay nil — linkage still gates path tools.
	result := predictArgs("lsp_diagnostics", "run_command", json.RawMessage(`{"file_path":"/x.go"}`))
	if result != nil {
		t.Fatalf("expected nil for unlinked run_command→lsp_diagnostics, got %s", result)
	}
}
