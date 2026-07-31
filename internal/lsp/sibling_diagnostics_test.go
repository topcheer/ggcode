package lsp

import (
	"testing"
)

func TestSiblingDiagnostics_NoServer(t *testing.T) {
	// With no LSP server available, should return nil, nil (not error).
	diags, err := SiblingDiagnostics(t.Context(), "/nonexistent/workspace", "/nonexistent/workspace/foo.go")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if diags != nil {
		t.Fatalf("expected nil diagnostics, got %v", diags)
	}
}

func TestSiblingDiagnostics_NonGoFile(t *testing.T) {
	// Non-Go files should be skipped immediately.
	diags, err := SiblingDiagnostics(t.Context(), "/tmp", "/tmp/foo.ts")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if diags != nil {
		t.Fatalf("expected nil for non-Go file, got %v", diags)
	}
}

func TestCachedDiagnostics_Empty(t *testing.T) {
	s := &sessionClient{
		diagnostics: make(map[string]diagnosticsState),
	}
	diags, seen := s.cachedDiagnostics("file:///nonexistent.go")
	if seen {
		t.Fatal("expected seen=false for unknown URI")
	}
	if diags != nil {
		t.Fatal("expected nil diagnostics for unknown URI")
	}
}

func TestCachedDiagnostics_WithData(t *testing.T) {
	uri := "file:///tmp/test.go"
	testDiags := []Diagnostic{
		{Severity: 1, Message: "undefined: foo"},
	}
	s := &sessionClient{
		diagnostics: map[string]diagnosticsState{
			uri: {seen: true, diagnostics: testDiags},
		},
	}
	diags, seen := s.cachedDiagnostics(uri)
	if !seen {
		t.Fatal("expected seen=true")
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Message != "undefined: foo" {
		t.Fatalf("unexpected message: %s", diags[0].Message)
	}

	// Verify it's a copy (modifying the returned slice should not affect internal state)
	diags[0].Message = "modified"
	diags2, _ := s.cachedDiagnostics(uri)
	if diags2[0].Message != "undefined: foo" {
		t.Fatal("cachedDiagnostics should return a copy, not the internal slice")
	}
}

func TestCachedDiagnostics_EmptyButSeen(t *testing.T) {
	uri := "file:///tmp/clean.go"
	s := &sessionClient{
		diagnostics: map[string]diagnosticsState{
			uri: {seen: true, diagnostics: nil},
		},
	}
	diags, seen := s.cachedDiagnostics(uri)
	if !seen {
		t.Fatal("expected seen=true for file with no diagnostics")
	}
	if diags != nil {
		t.Fatal("expected nil diagnostics for clean file")
	}
}
