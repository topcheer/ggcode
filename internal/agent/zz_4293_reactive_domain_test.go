package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4293 regression tests for the over-loose reactive rule-injection path.
// Live case: a legitimate `git worktree remove ... && git branch -D ...`
// cleanup command failed with git's own "use --force to delete it" hint, the
// harness's DESTRUCTIVE-operation banner echoed "rm -rf recursively force-
// deletes files", and the rm-rule injected its advice onto a command that
// never ran rm at all.

// gitWorktreeFailure is the actual failure output shape from the live case
// (git hint + harness banner quoting danger keywords).
const gitWorktreeFailure = `STDERR:
fatal: '/tmp/wt-4291b' contains modified or untracked files, use --force to delete it

Command failed: exit status 128

DESTRUCTIVE GIT OPERATION DETECTED
CRITICAL: [rm_rf] Risk: rm -rf recursively force-deletes files without confirmation
Safer alternative: Be very specific about paths
If this operation is intentional and necessary, proceed.`

func Test4293_ReactiveToolPatternRequiresArgsMatch(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	rs.AddRule(Rule{
		Category:     "security",
		Rule:         "Never run rm -rf or recursive force-delete on root or critical directories",
		MatchPattern: `rm -rf recursively force-deletes|Command blocked.*force delete`,
		ToolPattern:  `rm\s+-rf`,
		FixHint:      "Use explicit file paths instead of wildcards",
	})

	// The live false positive: output quotes the banner, but the command
	// was git cleanup - no rm -rf anywhere in the args.
	matched := rs.MatchingRulesForResult(gitWorktreeFailure, `git worktree remove /tmp/wt && git branch -D pr-x`)
	if len(matched) != 0 {
		t.Fatalf("command-shaped rule must not fire without matching args, got %d", len(matched))
	}

	// The genuine case: the gate actually blocked an rm -rf run; the block
	// text is real payload (not advisory wrapping) and the args contain rm -rf.
	gateBlocked := "Error: Command blocked: recursive force delete of critical directory /"
	matched = rs.MatchingRulesForResult(gateBlocked, `rm -rf /tmp/wt`)
	if len(matched) != 1 {
		t.Fatalf("command-shaped rule must fire when args match, got %d", len(matched))
	}
}

func Test4293_HarnessBannerDoesNotFeedRuleMatching(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	// No ToolPattern: output-only matching stays available, but only
	// against the payload, not the advisory wrapping.
	rs.AddRule(Rule{
		Category:     "security",
		Rule:         "Never run rm -rf on root or critical directories",
		MatchPattern: `rm -rf recursively force-deletes`,
		FixHint:      "Use explicit paths",
	})

	// Banner-only output (nothing else in the payload is an rm error):
	// after stripping, no error marker or pattern content remains that
	// justifies the injection.
	matched := rs.MatchingRulesForResult(gitWorktreeFailure, `git branch -D pr-x`)
	if len(matched) != 0 {
		t.Fatalf("banner text must not feed rule matching, got %d matches", len(matched))
	}
}

func Test4293_RealErrorStillReachesStripper(t *testing.T) {
	if got := stripHarnessAdvisories("STDERR:\nfatal: cannot find package olm\n[Verify] PASSED: 1 package(s)\n"); !strings.Contains(got, "cannot find package olm") {
		t.Fatalf("real error line must survive stripping, got %q", got)
	}
	if got := stripHarnessAdvisories(gitWorktreeFailure); strings.Contains(got, "force-deletes files") {
		t.Fatalf("harness banner must be stripped, got %q", got)
	}
}

func Test4293_ShortLearnedPatternSkipped(t *testing.T) {
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	rs.AddRule(Rule{
		Category:     "security",
		Rule:         "keyword-fragment rule",
		MatchPattern: `force`,
		FixHint:      "n/a",
	})
	// "force" appears in real error prose constantly ("use --force");
	// fragments below the quality floor must never inject.
	matched := rs.MatchingRulesForResult("fatal: use --force to delete it", `git branch -D x`)
	if len(matched) != 0 {
		t.Fatalf("sub-floor pattern must be skipped, got %d", len(matched))
	}
}

func Test4293_SecurityCategoryToolScope(t *testing.T) {
	if !categoryMatchesTool("security", "run_command") {
		t.Error("security must cover run_command")
	}
	if !categoryMatchesTool("security", "git_commit") {
		t.Error("security must cover git_* tools")
	}
	if !categoryMatchesTool("security", "edit_file") {
		t.Error("security must cover file-edit tools")
	}
	for _, tool := range []string{"read_file", "grep", "lsp_references", "web_fetch"} {
		if categoryMatchesTool("security", tool) {
			t.Errorf("security must not fire on %s", tool)
		}
	}
}

func Test4293_PerInjectionCap(t *testing.T) {
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, ".ggcode", "agent-rules.json")
	if err := os.MkdirAll(filepath.Dir(rulesPath), 0755); err != nil {
		t.Fatal(err)
	}
	// Five rules that all reactively match the same result.
	var rules []string
	for i := 0; i < 5; i++ {
		rules = append(rules, `{"id":"r-c`+string(rune('a'+i))+`","category":"build","rule":"rule `+string(rune('a'+i))+`","match_pattern":"cannot find package olm","fix_hint":"hint","hit_count":1,"created_at":"2025-01-01T00:00:00Z","last_seen":"2025-01-01T00:00:00Z"}`)
	}
	ruleJSON := `{"version":1,"rules":[` + strings.Join(rules, ",") + `]}`
	if err := os.WriteFile(rulesPath, []byte(ruleJSON), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{}
	a.SetWorkingDir(dir)
	result := a.injectRulesIntoResult("run_command", []byte(`{"command":"go build ./..."}`),
		"Error: cannot find package olm")
	if n := strings.Count(result, "\u26a0"); n > maxRulesPerInjection {
		t.Fatalf("per-injection cap breached: %d rule lines > %d", n, maxRulesPerInjection)
	}
}
