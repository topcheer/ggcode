package agent

// #3670/#3673 probes: the zero-match SUCCESS gate must use the result's own
// sentinel line, not bare substrings, and both call sites must agree.
//
// #3670: a rich grep result whose hit lines QUOTE the literal "no match"
// source text must not be mistaken for a zero match.
// #3673-A: "not found"/"empty" substrings on ordinary file content must not
// trip isEmptyToolResult either.
// #3673-B/C: coverage sets of the two engines must match.

import "strings"
import "testing"

func Test3670_RichResultQuotingSentinelIsNotZeroMatch(t *testing.T) {
	// Hit lines quoting the literal phrase (e.g. grepping for the detector
	// itself) - a rich, multi-line result.
	rich := "internal/agent/tool_fallback_hints.go:143: strings.Contains(lower, \"no match\")\n" +
		"internal/agent/tool_fallback_hints.go:150: no match found in this line too\n" +
		"internal/tool/grep.go:390: exit 1: no matches; fall through"
	if isZeroMatchSuccess("grep", rich) {
		t.Fatal("rich grep result quoting the 'no match' literal must not be judged zero-match (#3670)")
	}
	if toolFallbackHint("grep", rich) != "" {
		t.Fatal("rich grep result must not get a No-matches hint (#3673-A)")
	}
	// "not found"/"empty" on ordinary content (#3673-A, agent_tool.go path).
	if toolFallbackHint("grep", "handler.go:12: // err is not found here\nhandler.go:30: // list is empty after drain") != "" {
		t.Fatal("ordinary 'not found'/'empty' content must not be judged zero-match (#3673-A)")
	}
}

func Test3670_RealZeroMatchSentinelsStillFire(t *testing.T) {
	// grep.go contract shape: sentinel first line + suggestions block.
	if !isZeroMatchSuccess("grep", "No matches found.\nSuggestions:\n  use a broader pattern") {
		t.Fatal("grep's canonical zero-match payload must be detected")
	}
	// search_files shape: sentinel + pattern.
	if !isZeroMatchSuccess("search_files", "No matches found for pattern \"foo\"") {
		t.Fatal("search_files zero-match payload must be detected")
	}
	if !isZeroMatchSuccess("code_search", "") {
		t.Fatal("empty content is a zero match")
	}
	if toolFallbackHint("grep", "No matches found.\nSuggestions:\n  use a broader pattern") == "" {
		t.Fatal("canonical zero-match must still yield the broaden hint")
	}
	// Sentinel NOT on the first line (a hit line precedes it) -> rich result.
	if isZeroMatchSuccess("grep", "a.go:1: real hit\nNo matches found.") {
		t.Fatal("sentinel below a hit line means the result is not empty")
	}
}

func Test3673_CoverageSetsMatch(t *testing.T) {
	for _, name := range []string{"grep", "search_files", "code_search",
		"lsp_workspace_symbols", "lsp_references", "lsp_implementation"} {
		if !toolFallbackHintOnSuccess(name) {
			t.Fatalf("%s missing from the success gate", name)
		}
	}
	for _, name := range []string{"read_file", "edit_file", "run_command", "glob", "web_search"} {
		if toolFallbackHintOnSuccess(name) {
			t.Fatalf("%s must not gain success-result hints (scope creep)", name)
		}
	}
}

func Test3673_LongPayloadIsRich(t *testing.T) {
	// Even if the first line is the sentinel, a payload over the byte cap is
	// by definition rich (defensive: sentinel + huge trailing content).
	var b strings.Builder
	b.WriteString("No matches found.\n")
	for i := 0; i < 60; i++ {
		b.WriteString("padding-line-that-makes-this-result-rich-aaaaaaaaaaaaaaaaaaaaaaaaaa\n")
	}
	if isZeroMatchSuccess("grep", b.String()) {
		t.Fatal("payload over zeroMatchMaxBytes must be treated as rich")
	}
}
