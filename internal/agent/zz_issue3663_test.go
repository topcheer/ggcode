package agent

// #3663 probe: tools whose output ECHOES repository source text
// (git_show/git_diff patches, batch_replace dry-run previews, browser
// content fetches) must not be flagged poor when the echoed source embeds
// truncation-marker literals verbatim - the #3632 residual. Tools that
// genuinely append advisory markers keep firing.

import "testing"

func TestIssue3663_SourceEchoToolsNotFlagged(t *testing.T) {
	// git_show/git_diff of this repository's own tool source: the marker
	// literal is payload, not an advisory.
	src := "+	if strings.Contains(lower, \"[output truncated]\") {\n+\t\treturn true\n"
	for _, name := range []string{"git_show", "git_diff", "batch_replace", "browser"} {
		if isPoorResult(name, src) {
			t.Fatalf("%s flagged for echoed marker literal (payload, not advisory)", name)
		}
	}
	// Tools that DO append advisories keep firing.
	if !isPoorResult("some_mcp_tool", "result:\n[output truncated]\nsee docs") {
		t.Fatal("genuine advisory marker no longer flagged - scope crept too far")
	}
	if !isPoorResult("run_command", "head...\n[12 lines omitted - output truncated, showing tail]\n...tail") {
		t.Fatal("run_command anchored tail marker no longer flagged")
	}
	// Clean outputs are unaffected.
	if isPoorResult("git_diff", "diff --git a/x.go b/x.go\n+only real changes") {
		t.Fatal("clean git_diff flagged as poor")
	}
}
