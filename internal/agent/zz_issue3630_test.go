package agent

// #3630 probes: the fallback chain must also run for zero-result SUCCESS
// payloads. grep/glob/code_search/lsp_* return "No matches found" as
// IsError=false success, and the agent-loop gate was IsError-only, so the
// zero-result fallback rules in fallbackRules were deterministic dead code
// - the exact search-spiral scenario they were written for never reached
// them.

import (
	"strings"
	"testing"
)

func TestIssue3630_ZeroResultSuccessGetsFallback(t *testing.T) {
	// The real zero-result payloads the tool layer emits (internal/tool:
	// grep.go "No matches found", lsp.go "No definition found.").
	cases := map[string]string{
		"grep":                  "No matches found for pattern \"foo\" in path bar",
		"search_files":          "No matches found",
		"glob":                  "No files found matching pattern",
		"code_search":           "No results found. Try a different query.",
		"lsp_definition":        "No definition found.",
		"lsp_references":        "No references found.",
		"lsp_hover":             "No hover information available",
		"lsp_workspace_symbols": "No symbols matching query",
	}
	for tool, content := range cases {
		if !fallbackCheckApplies(tool, false) {
			t.Fatalf("zero-result tool %s must qualify for fallback check on success", tool)
		}
		s := newToolFallbackState()
		if hint := s.maybeFallbackSuggestion(tool, content); hint == "" {
			t.Fatalf("zero-result payload for %s got no fallback suggestion", tool)
		} else if !strings.Contains(hint, "[fallback]") {
			t.Fatalf("unexpected suggestion format for %s: %q", tool, hint)
		}
	}
}

func TestIssue3630_NormalResultsStillSkipFallback(t *testing.T) {
	// Non-empty successful results must not match the zero-result rules.
	s := newToolFallbackState()
	hint := s.maybeFallbackSuggestion("grep", "file.go:12:matched text here")
	if strings.Contains(hint, "no matches") || (hint != "" && !strings.Contains(hint, "[fallback]")) {
		t.Fatalf("normal grep hit misrouted: %q", hint)
	}
	// The always-matching _default fires only under IsError in the loop;
	// successful grep content reaching it means rule matching is broken.
	if hint != "" && strings.Contains(hint, "no matches") {
		t.Fatalf("zero-result rule matched non-empty result: %q", hint)
	}
	// Non-search tools on success never qualify (gate stays IsError-only).
	if fallbackCheckApplies("edit_file", false) {
		t.Fatal("non-zero-result tool must not qualify on success")
	}
}

func TestIssue3630_ErrorGateUnchanged(t *testing.T) {
	if !fallbackCheckApplies("edit_file", true) {
		t.Fatal("error path must keep qualifying")
	}
	if !fallbackCheckApplies("grep", true) {
		t.Fatal("error path must keep qualifying for search tools")
	}
}
