package agent

// #3641 probes: toolFallbackHint's stated main scenario (grep zero matches
// -> suggest broader pattern) never fired because both call sites gated on
// result.IsError, and zero matches return as success. Same root cause as
// #3630, second parallel implementation (agent_tool.go pathway).

import (
	"strings"
	"testing"
)

func TestIssue3641_GrepZeroMatchSuccessGetsHint(t *testing.T) {
	// The exact payload internal/tool/grep.go emits on zero matches.
	content := "No matches found for pattern \"zzz_nonexistent\""
	hint := toolFallbackHint("grep", content)
	if !strings.Contains(hint, "broaden the pattern") {
		t.Fatalf("grep zero-match success must get the broaden-pattern hint, got: %q", hint)
	}
}

func TestIssue3641_OtherZeroResultToolsGetHints(t *testing.T) {
	cases := map[string]string{
		"search_files":   "No matches found",
		"code_search":    "No results found for the query",
		"lsp_definition": "No definition found.",
	}
	for tool, content := range cases {
		hint := toolFallbackHint(tool, content)
		if hint == "" {
			t.Fatalf("%s zero-match success must get a hint", tool)
		}
	}
}

func TestIssue3641_NormalHitNoHint(t *testing.T) {
	if hint := toolFallbackHint("grep", "file.go:12:matched text"); hint != "" {
		t.Fatalf("non-empty grep hit must not get a zero-result hint: %q", hint)
	}
}
