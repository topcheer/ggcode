package agent

// Issue #739: tool_claim_verify semantic mismatch for content-bearing tools.
//
// The old implementation ran execution-status patterns ("exit code: 1",
// "does not exist", "fail:", "no matches found") over result.Content for
// content-bearing tools (grep/read_file/multi_file_read/...), whose Content
// is arbitrary file data or match lines, not the tool's execution status.
// Successful operations whose payload merely contained a status-like phrase
// were injected with advisories contradicting the actual outcome.
//
// Fix: status patterns now apply only to command tools (run_command /
// start_command). Content-bearing tools only match the tool's OWN zero-result
// meta-status line ("No matches found." as the whole result). See
// tool_claim_verify.go for the semantic boundary notes.

import (
	"strings"
	"testing"
)

// False positive 1: successful grep whose match lines contain "does not exist".
func TestIssue739_GrepFoundMatchesMentioningDoesNotExist(t *testing.T) {
	s := newClaimVerifyState()
	content := "./internal/tool/foo.go:42:\treturn fmt.Errorf(\"path does not exist\")\n./internal/tool/bar.go:10:\t// file does not exist fallback"
	if g := s.check("grep", content, false); g != "" {
		t.Fatalf("expected no guidance for successful grep with matches, got: %s", g)
	}
}

// False positive 2: successful grep for documentation wording "no matches found".
func TestIssue739_GrepFoundMatchesMentioningNoMatches(t *testing.T) {
	s := newClaimVerifyState()
	content := "./README.md:7:If the command prints no matches found, widen the directory."
	if g := s.check("grep", content, false); g != "" {
		t.Fatalf("expected no guidance for successful grep with matches, got: %s", g)
	}
}

// False positive 3: read_file of test source containing t.Fatal.
func TestIssue739_ReadFileContainingTFatal(t *testing.T) {
	s := newClaimVerifyState()
	content := "func TestFoo(t *testing.T) {\n\tif got != want {\n\t\tt.Fatal(\"mismatch\")\n\t}\n}"
	if g := s.check("read_file", content, false); g != "" {
		t.Fatalf("expected no guidance for read_file of test source, got: %s", g)
	}
}

// False positive 4: read_file of a shell script containing literal "exit code: 1".
func TestIssue739_ReadFileContainingExitCodeLiteral(t *testing.T) {
	s := newClaimVerifyState()
	content := "#!/bin/sh\nif rg -q foo ./src; then\n\techo found\nelse\n\techo \"exit code: 1 means no matches\"\nfi"
	if g := s.check("read_file", content, false); g != "" {
		t.Fatalf("expected no guidance for read_file of script, got: %s", g)
	}
}

// Meta-status phrase NOT at the start (payload mention) must not trigger either.
func TestIssue739_MetaStatusPhraseEmbeddedInMatchLines(t *testing.T) {
	s := newClaimVerifyState()
	content := "docs/guide.md:3:The tool replies: No matches found. when nothing hits."
	if g := s.check("grep", content, false); g != "" {
		t.Fatalf("expected no guidance for embedded meta-status mention, got: %s", g)
	}
}

// True positive preserved: grep zero result returns the tool's own
// meta-status line as the whole Content.
func TestIssue739_GrepZeroResultMetaStatusStillWarns(t *testing.T) {
	s := newClaimVerifyState()
	g := s.check("grep", "No matches found.", false)
	if g == "" {
		t.Fatal("expected guidance for grep zero-result meta-status")
	}
	if !strings.Contains(g, "no matches") {
		t.Fatalf("unexpected guidance: %s", g)
	}
	// With the Suggestions block appended (formatGrepOutput zero path).
	s2 := newClaimVerifyState()
	g = s2.check("grep", "No matches found.\nSuggestions:\n  Try case-insensitive search (-i).", false)
	if g == "" {
		t.Fatal("expected guidance for grep zero-result with suggestions")
	}
}

// True positive preserved: command tools keep full status-pattern scanning.
func TestIssue739_CommandToolsStillWarnOnStatusPatterns(t *testing.T) {
	cases := []struct {
		name, content string
	}{
		{"exit code", "go test ./...\nexit code: 1"},
		{"fail", "--- FAIL: TestFoo (0.00s)"},
		{"does not exist", "cat /nope\npath does not exist"},
	}
	for _, tc := range cases {
		s := newClaimVerifyState()
		if g := s.check("run_command", tc.content, false); g == "" {
			t.Fatalf("expected guidance for run_command %s case", tc.name)
		}
	}
	// start_command too.
	s := newClaimVerifyState()
	if g := s.check("start_command", "exit code: 1", false); g == "" {
		t.Fatal("expected guidance for start_command exit-code case")
	}
}

// All content-bearing tools exit the status-pattern check entirely.
func TestIssue739_ContentToolsNeverMatchStatusPatterns(t *testing.T) {
	for _, tool := range []string{
		"grep", "search_files", "glob", "read_file", "multi_file_read",
		"code_search", "lsp_definition", "lsp_references", "lsp_symbols",
		"lsp_hover", "lsp_diagnostics",
	} {
		s := newClaimVerifyState()
		if g := s.check(tool, "panic: boom exit code: 1 does not exist", false); g != "" {
			t.Fatalf("tool %s: expected no guidance for payload status phrases, got: %s", tool, g)
		}
	}
}
