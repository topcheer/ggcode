package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDetectDestructiveInShellCommand(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		wantPat string // expected pattern name
		wantSev string // expected severity
	}{
		{"reset_hard", "git reset --hard HEAD~1", "reset_hard", "critical"},
		{"force_push", "git push origin main --force", "force_push", "critical"},
		{"force_push_short", "git push -f origin main", "force_push", "critical"},
		{"force_with_lease", "git push --force-with-lease origin main", "force_with_lease", "warning"},
		{"clean_fd", "git clean -fd", "clean_force", "critical"},
		{"clean_f", "git clean -f", "clean_force", "critical"},
		{"branch_delete", "git branch -D feature-x", "branch_force_delete", "critical"},
		{"stash_drop", "git stash drop", "stash_drop", "warning"},
		{"stash_clear", "git stash clear", "stash_drop", "warning"},
		{"rebase", "git rebase main", "rebase", "warning"},
		{"filter_branch", "git filter-branch --tree-filter 'rm secrets.txt' HEAD", "filter_branch", "critical"},
		{"rm_rf", "rm -rf /tmp/build", "rm_rf", "critical"},
		{"checkout_discard", "git checkout -- .", "discard_all", "critical"},
		{"restore_discard", "git restore -- .", "discard_all", "critical"},
		{"safe_command", "git status", "", ""},
		{"safe_push", "git push origin main", "", ""},
		{"safe_reset_soft", "git reset --soft HEAD~1", "", ""},
		{"safe_stash", "git stash push -m 'wip'", "", ""},
		{"empty", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns := detectDestructiveInShellCommand(tt.cmd)
			if tt.wantPat == "" {
				if len(patterns) != 0 {
					t.Errorf("expected no patterns, got %d: %+v", len(patterns), patterns)
				}
				return
			}
			found := false
			for _, p := range patterns {
				if p.name == tt.wantPat {
					found = true
					if p.severity != tt.wantSev {
						t.Errorf("pattern %s: severity = %s, want %s", p.name, p.severity, tt.wantSev)
					}
				}
			}
			if !found {
				t.Errorf("expected pattern %s not found in results: %+v", tt.wantPat, patterns)
			}
		})
	}
}

func TestDetectDestructiveInGitTool(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]any
		wantPat  string
		wantSev  string
	}{
		{"reset_hard", "git_reset", map[string]any{"mode": "hard"}, "reset_hard", "critical"},
		{"reset_soft", "git_reset", map[string]any{"mode": "soft"}, "", ""},
		{"reset_default", "git_reset", map[string]any{"mode": "mixed"}, "", ""},
		{"stash_drop", "git_stash", map[string]any{"action": "drop"}, "stash_drop", "warning"},
		{"stash_clear", "git_stash", map[string]any{"action": "clear"}, "stash_drop", "warning"},
		{"stash_push", "git_stash", map[string]any{"action": "push"}, "", ""},
		{"checkout_safe", "git_checkout", map[string]any{"branch": "main"}, "", ""},
		{"empty_args", "git_reset", nil, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rawArgs json.RawMessage
			if tt.args != nil {
				rawArgs, _ = json.Marshal(tt.args)
			}
			patterns := detectDestructiveInGitTool(tt.toolName, rawArgs)
			if tt.wantPat == "" {
				if len(patterns) != 0 {
					t.Errorf("expected no patterns, got %d: %+v", len(patterns), patterns)
				}
				return
			}
			found := false
			for _, p := range patterns {
				if p.name == tt.wantPat {
					found = true
					if p.severity != tt.wantSev {
						t.Errorf("pattern %s: severity = %s, want %s", p.name, p.severity, tt.wantSev)
					}
				}
			}
			if !found {
				t.Errorf("expected pattern %s not found in results: %+v", tt.wantPat, patterns)
			}
		})
	}
}

func TestCheckGitDestructiveOncePerRun(t *testing.T) {
	a := &Agent{
		destructiveGuard: newGitDestructiveState(),
	}

	// First call for reset_hard should produce a warning
	args, _ := json.Marshal(map[string]any{"command": "git reset --hard HEAD"})
	w1 := a.checkGitDestructive("run_command", args)
	if w1 == "" {
		t.Fatal("expected warning on first reset --hard detection")
	}
	if !strings.Contains(w1, "reset_hard") {
		t.Errorf("warning should mention reset_hard: %s", w1)
	}

	// Second call for the same pattern should NOT produce a warning
	w2 := a.checkGitDestructive("run_command", args)
	if w2 != "" {
		t.Errorf("expected no repeated warning, got: %s", w2)
	}

	// Different pattern should still warn
	args2, _ := json.Marshal(map[string]any{"command": "git clean -fd"})
	w3 := a.checkGitDestructive("run_command", args2)
	if w3 == "" {
		t.Fatal("expected warning for clean_force (different pattern)")
	}
}

func TestCheckGitDestructiveSafeCommand(t *testing.T) {
	a := &Agent{
		destructiveGuard: newGitDestructiveState(),
	}

	args, _ := json.Marshal(map[string]any{"command": "go test ./..."})
	w := a.checkGitDestructive("run_command", args)
	if w != "" {
		t.Errorf("expected no warning for safe command, got: %s", w)
	}
}

func TestCheckGitDestructiveNilGuard(t *testing.T) {
	a := &Agent{}
	args, _ := json.Marshal(map[string]any{"command": "git reset --hard"})
	w := a.checkGitDestructive("run_command", args)
	if w != "" {
		t.Errorf("expected empty warning when guard is nil, got: %s", w)
	}
}

func TestGitDestructiveStateReset(t *testing.T) {
	s := newGitDestructiveState()
	s.warned["reset_hard"] = true
	s.warned["force_push"] = true

	s.reset()

	if len(s.warned) != 0 {
		t.Errorf("expected empty warned map after reset, got %d entries", len(s.warned))
	}
}

func TestCheckGitDestructiveCriticalVsWarning(t *testing.T) {
	a := &Agent{
		destructiveGuard: newGitDestructiveState(),
	}

	// Critical should have CRITICAL prefix
	args, _ := json.Marshal(map[string]any{"command": "git push --force origin main"})
	w := a.checkGitDestructive("run_command", args)
	if !strings.Contains(w, "CRITICAL") {
		t.Errorf("critical pattern should contain CRITICAL marker: %s", w)
	}

	// Reset for next test
	a.destructiveGuard.reset()

	// Warning severity should not have CRITICAL
	args2, _ := json.Marshal(map[string]any{"command": "git stash drop"})
	w2 := a.checkGitDestructive("run_command", args2)
	if strings.Contains(w2, "CRITICAL") {
		t.Errorf("warning pattern should not contain CRITICAL marker: %s", w2)
	}
}

// TestGitDestructiveBranchForceMoveWarning pins #1464-B: git branch -f
// re-points a branch pointer - warning severity (reflog keeps commits
// reachable ~90 days, unlike reset --hard's unrecoverable losses), not
// critical.
func TestGitDestructiveBranchForceMoveWarning(t *testing.T) {
	patterns := detectDestructiveInShellCommand("git branch -f main HEAD~5")
	if len(patterns) == 0 {
		t.Fatal("branch -f uncovered")
	}
	for _, p := range patterns {
		if p.name == "branch_force_move" && p.severity != "warning" {
			t.Fatalf("branch -f severity = %q, want warning", p.severity)
		}
	}
	// Safe forms stay quiet.
	if got := detectDestructiveInShellCommand("git branch -d merged-feature"); len(got) != 0 {
		t.Fatalf("safe lowercase -d flagged: %+v", got)
	}
}

// Regression for #1569-D/E: discard-form blind spots and flag variants.
func TestDestructiveDiscardFormsAndVariants(t *testing.T) {
	hit := []struct{ cmd, want string }{
		{"git checkout .", "discard_all"},              // bare shortest form
		{"git restore .", "discard_all"},               // modern equivalent
		{"git checkout -f main", "discard_all"},        // force checkout
		{"git switch -f main", "discard_all"},          // force switch
		{"git checkout -B main HEAD~5", "discard_all"}, // semantic branch -f
		{"git checkout -- .", "discard_all"},           // legacy pin shape
		{"git reset -q --hard", "reset_hard"},          // flag between reset and --hard
		{"rm -Rf tmp", "rm_rf"},                        // BSD/macOS capital
		{"rm -r -f tmp", "rm_rf"},                      // separated flags
		{"rm --recursive --force tmp", "rm_rf"},        // long options
	}
	for _, h := range hit {
		pats := detectDestructiveInShellCommand(h.cmd)
		found := false
		for _, p := range pats {
			if p.name == h.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q must fire %s, got %+v", h.cmd, h.want, pats)
		}
	}
	// Safe shapes must stay silent.
	for _, cmd := range []string{
		"git checkout main", "git checkout -b feature", "git restore --staged file.go",
		"git reset --soft HEAD~1", "rm file.txt",
	} {
		if pats := detectDestructiveInShellCommand(cmd); len(pats) != 0 {
			t.Errorf("%q must not fire, got %+v", cmd, pats)
		}
	}
}

// Regression for #1600-C: wrapper/subshell forms must still be detected -
// the token adjacency check skipped quoted invocations the old \b-regex
// matched.
func TestForcePushQuotedWrapperForm(t *testing.T) {
	if !isForcePushCommand(`sh -c "git push --force origin main"`) {
		t.Fatal("quoted wrapper force push must be detected")
	}
	if !isForcePushCommand("bash -c 'git push -f origin main'") {
		t.Fatal("single-quoted wrapper force push must be detected")
	}
	// Plain and negative controls keep their behavior.
	if !isForcePushCommand("git push -f origin main") {
		t.Fatal("plain force push must stay detected")
	}
	if isForcePushCommand("git push origin main") {
		t.Fatal("plain push must stay clean")
	}
}

// Regression for #1609-A: newline separators - Fields splits on \n, so a
// multi-line script's second line reached the -f scan with no separator
// token at all.
func TestForcePushNewlineSeparatedSecondCommand(t *testing.T) {
	if isForcePushCommand("git push origin main\nmake -f Makefile.build") {
		t.Fatal("second-line -f after a newline must not fire force_push")
	}
	if !isForcePushCommand("git push --force origin main") {
		t.Fatal("single-line force push must stay detected")
	}
}

// TestGitDiscardAllRefForms pins #1754 case 1: the restore-to-HEAD
// spellings agents actually write (ref/tree-ish token before the dot,
// --source=, chains) all previously slipped the single-dash flag slot.
func TestGitDiscardAllRefForms(t *testing.T) {
	mustMatch := []string{
		"git checkout HEAD -- .",
		"git checkout main .",
		"git checkout origin/main .",
		"git restore --source=HEAD .",
		"git checkout . && git add -A",
		"git switch -C main",
		"git checkout -f HEAD .",
	}
	for _, cmd := range mustMatch {
		if !reGitDiscardAll.MatchString(cmd) {
			t.Errorf("must match: %q", cmd)
		}
	}
	// Non-destructive look-alikes stay clean.
	for _, cmd := range []string{
		"git checkout --staged .",
		"git checkout -b feature-x",
		"git checkout --orphan fresh",
		"git restore --staged file.go",
		"git switch feature-x",
	} {
		if reGitDiscardAll.MatchString(cmd) {
			t.Errorf("false positive on: %q", cmd)
		}
	}
}

// #1886: the discard-all detector missed the LONG flag spelling of the
// worktree restore, semicolon-chained discards, and false-fired on
// branch-create + pathspec (a combination git itself rejects).
func TestGitDiscardAllWorktreeSemicolonBranchCreate(t *testing.T) {
	mustMatch := []string{
		// Long flag spelling (the -W abbreviation already matched).
		"git restore --worktree .",
		"git restore --staged --worktree .",
		"git restore --source=HEAD --worktree .",
		// Semicolon chain anchor (was $|&&|-- only).
		"git checkout . ; git add -A",
		"git restore . ; echo done",
	}
	for _, cmd := range mustMatch {
		if !reGitDiscardAll.MatchString(cmd) {
			t.Errorf("must match: %q", cmd)
		}
	}

	mustStayClean := []string{
		// Legal unstage: --staged alone is NOT a worktree discard.
		"git restore --staged file.go",
		"git restore --staged .",
	}
	for _, cmd := range mustStayClean {
		if reGitDiscardAll.MatchString(cmd) {
			t.Errorf("must stay clean: %q", cmd)
		}
	}

	// -b with a pathspec is rejected by git itself - no advisory.
	if got := detectDestructiveInShellCommand("git checkout -b feature ."); hasPattern(got, "discard_all") {
		t.Error("checkout -b feature . must not fire discard_all (git rejects -b with a pathspec)")
	}
	// ...while the equivalent real discards still fire.
	if !hasPattern(detectDestructiveInShellCommand("git restore --worktree ."), "discard_all") {
		t.Error("restore --worktree . must fire discard_all")
	}
	if !hasPattern(detectDestructiveInShellCommand("git checkout -f ."), "discard_all") {
		t.Error("checkout -f . must keep firing discard_all")
	}
}

func hasPattern(pats []destructivePattern, name string) bool {
	for _, p := range pats {
		if p.name == name {
			return true
		}
	}
	return false
}

// TestIsCleanForceCommandSeparatorAndGlobalFlags pins #1774 cases 1+2:
// a second command's -f must not fire on a force-free clean (separator
// truncation, #1600-A for push only), and 'git -C <dir> clean -fd' must
// still detect (global flags between git and the subcommand).
func TestIsCleanForceCommandSeparatorAndGlobalFlags(t *testing.T) {
	if isCleanForceCommand("git clean . && rm -f x") {
		t.Fatal("second command's -f must not fire on a force-free clean")
	}
	if !isCleanForceCommand("git -C /repo clean -fd") {
		t.Fatal("git -C <dir> clean -fd must detect")
	}
	if !isCleanForceCommand("git --no-pager clean --force") {
		t.Fatal("global flag --no-pager must not hide clean --force")
	}
	if !isCleanForceCommand("git clean -fd") {
		t.Fatal("plain clean -fd must still detect")
	}
	if isCleanForceCommand("git clean -n") {
		t.Fatal("dry-run must still suppress")
	}
}
