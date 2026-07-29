package tool

import (
	"testing"

	"github.com/topcheer/ggcode/internal/lsp"
)

func TestPostEditDiagnostics_Disabled(t *testing.T) {
	old := postEditDiagEnabled
	postEditDiagEnabled = false
	defer func() { postEditDiagEnabled = old }()

	result := postEditDiagnostics("/some/dir", "/some/dir/main.go")
	if result != "" {
		t.Fatalf("expected empty string when disabled, got %q", result)
	}
}

func TestPostEditDiagnostics_EmptyWorkingDir(t *testing.T) {
	result := postEditDiagnostics("", "/some/dir/main.go")
	if result != "" {
		t.Fatalf("expected empty string for empty working dir, got %q", result)
	}
}

func TestPostEditDiagnostics_NonSourceFile(t *testing.T) {
	// Even if LSP is "available", non-source files should be skipped.
	// Since this test doesn't have a real LSP server, it would return "" anyway,
	// but we test the isSourceFile guard explicitly.
	if isSourceFile("/path/to/readme.md") {
		t.Fatal("expected .md to not be a source file")
	}
	if isSourceFile("/path/to/config.yaml") {
		t.Fatal("expected .yaml to not be a source file")
	}
	if !isSourceFile("/path/to/main.go") {
		t.Fatal("expected .go to be a source file")
	}
	if !isSourceFile("/path/to/app.ts") {
		t.Fatal("expected .ts to be a source file")
	}
	if !isSourceFile("/path/to/index.py") {
		t.Fatal("expected .py to be a source file")
	}
}

func TestFormatDiagnostics_NoErrors(t *testing.T) {
	result := formatDiagnostics(nil)
	if result != "" {
		t.Fatalf("expected empty for nil diagnostics, got %q", result)
	}

	// Info and hints should be suppressed.
	result = formatDiagnostics([]lsp.Diagnostic{
		{Severity: 3, Message: "some info", Range: lsp.Range{Start: lsp.Position{Line: 0}}},
		{Severity: 4, Message: "some hint", Range: lsp.Range{Start: lsp.Position{Line: 1}}},
	})
	if result != "" {
		t.Fatalf("expected empty for info/hint diagnostics, got %q", result)
	}
}

func TestFormatDiagnostics_WithErrors(t *testing.T) {
	result := formatDiagnostics([]lsp.Diagnostic{
		{Severity: 1, Message: "undefined: foo", Range: lsp.Range{Start: lsp.Position{Line: 41}}},
		{Severity: 1, Message: "undefined: bar", Range: lsp.Range{Start: lsp.Position{Line: 52}}},
	})
	if result == "" {
		t.Fatal("expected non-empty result for error diagnostics")
	}
	if !contains(result, "undefined: foo") {
		t.Fatalf("expected result to contain 'undefined: foo', got %q", result)
	}
	if !contains(result, "L42:") {
		t.Fatalf("expected result to contain 'L42:', got %q", result)
	}
	if !contains(result, "Errors (2)") {
		t.Fatalf("expected error count, got %q", result)
	}
}

func TestFormatDiagnostics_WithWarnings(t *testing.T) {
	result := formatDiagnostics([]lsp.Diagnostic{
		{Severity: 2, Message: "unused variable", Range: lsp.Range{Start: lsp.Position{Line: 9}}},
	})
	if result == "" {
		t.Fatal("expected non-empty result for warning diagnostics")
	}
	if !contains(result, "Warnings (1)") {
		t.Fatalf("expected warning count, got %q", result)
	}
	if !contains(result, "L10:") {
		t.Fatalf("expected 'L10:' in result, got %q", result)
	}
}

func TestFormatDiagnostics_ErrorAndWarningCap(t *testing.T) {
	// Generate 15 errors and 8 warnings — should cap at 10 errors and 5 warnings.
	diags := make([]lsp.Diagnostic, 0, 23)
	for i := 0; i < 15; i++ {
		diags = append(diags, lsp.Diagnostic{Severity: 1, Message: "err", Range: lsp.Range{Start: lsp.Position{Line: i}}})
	}
	for i := 0; i < 8; i++ {
		diags = append(diags, lsp.Diagnostic{Severity: 2, Message: "warn", Range: lsp.Range{Start: lsp.Position{Line: i}}})
	}
	result := formatDiagnostics(diags)
	if !contains(result, "... and 5 more") {
		t.Fatalf("expected error cap message, got %q", result)
	}
	if !contains(result, "... and 3 more") {
		t.Fatalf("expected warning cap message, got %q", result)
	}
}

func TestSetPostEditDiagEnabled(t *testing.T) {
	old := postEditDiagEnabled
	defer func() { postEditDiagEnabled = old }()

	SetPostEditDiagEnabled(false)
	if postEditDiagEnabled {
		t.Fatal("expected postEditDiagEnabled to be false")
	}
	SetPostEditDiagEnabled(true)
	if !postEditDiagEnabled {
		t.Fatal("expected postEditDiagEnabled to be true")
	}
}
