package agent

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

	if toolFallbackHint("grep", "No matches found for pattern") == "" {
		t.Fatal("empty grep result must yield a broaden-the-search hint")
	}

	if toolFallbackHint("grep", "internal/agent/agent.go:42: func Run(") != "" {
		t.Fatal("non-empty grep result must not be hinted")
	}
}
