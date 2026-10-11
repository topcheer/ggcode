package agent

// #3641 probe: grep/search_files/code_search render zero matches as a
// SUCCESS result (grep.go contract), so the fallback-hint path gated on
// IsError alone never fired - the module's headline scenario ("When grep
// returns nothing...") was unreachable. Successful empty results of the
// three search tools must now produce a hint; successful non-empty results
// and other tools' successes must not.

import "testing"

func TestIssue3641_SuccessGateCoversSearchTools(t *testing.T) {
	if !toolFallbackHintOnSuccess("grep") || !toolFallbackHintOnSuccess("search_files") || !toolFallbackHintOnSuccess("code_search") {
		t.Fatal("the three search tools must flow success results through the hint path")
	}
	for _, name := range []string{"read_file", "edit_file", "run_command", "glob"} {
		if toolFallbackHintOnSuccess(name) {
			t.Fatalf("%s must not gain success-result hints (scope creep)", name)
		}
	}
	// The hint itself still decides: empty success content -> hint.
	if toolFallbackHint("grep", "No matches found for pattern") == "" {
		t.Fatal("empty grep result must yield a broaden-the-search hint")
	}
	// Non-empty success content -> no hint (unchanged behavior).
	if toolFallbackHint("grep", "internal/agent/agent.go:42: func Run(") != "" {
		t.Fatal("non-empty grep result must not be hinted")
	}
}
