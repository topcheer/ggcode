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
		wantPat string
		wantSev string
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

	args, _ := json.Marshal(map[string]any{"command": "git reset --hard HEAD"})
	w1 := a.checkGitDestructive("run_command", args)
	if w1 == "" {
		t.Fatal("expected warning on first reset --hard detection")
	}
	if !strings.Contains(w1, "reset_hard") {
		t.Errorf("warning should mention reset_hard: %s", w1)
	}

	w2 := a.checkGitDestructive("run_command", args)
	if w2 != "" {
		t.Errorf("expected no repeated warning, got: %s", w2)
	}

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

	args, _ := json.Marshal(map[string]any{"command": "git push --force origin main"})
	w := a.checkGitDestructive("run_command", args)
	if !strings.Contains(w, "CRITICAL") {
		t.Errorf("critical pattern should contain CRITICAL marker: %s", w)
	}

	a.destructiveGuard.reset()

	args2, _ := json.Marshal(map[string]any{"command": "git stash drop"})
	w2 := a.checkGitDestructive("run_command", args2)
	if strings.Contains(w2, "CRITICAL") {
		t.Errorf("warning pattern should not contain CRITICAL marker: %s", w2)
	}
}

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

	if got := detectDestructiveInShellCommand("git branch -d merged-feature"); len(got) != 0 {
		t.Fatalf("safe lowercase -d flagged: %+v", got)
	}
}

func TestDestructiveDiscardFormsAndVariants(t *testing.T) {
	hit := []struct{ cmd, want string }{
		{"git checkout .", "discard_all"},
		{"git restore .", "discard_all"},
		{"git checkout -f main", "discard_all"},
		{"git switch -f main", "discard_all"},
		{"git checkout -B main HEAD~5", "discard_all"},
		{"git checkout -- .", "discard_all"},
		{"git reset -q --hard", "reset_hard"},
		{"rm -Rf tmp", "rm_rf"},
		{"rm -r -f tmp", "rm_rf"},
		{"rm --recursive --force tmp", "rm_rf"},
		{"sudo rm -rf tmp", "rm_rf"},
		{"nohup rm -rf tmp", "rm_rf"},
		{"xargs rm -rf", "rm_rf"},
		{"timeout 30 rm -rf tmp", "rm_rf"},
		{"sudo -u root rm -rf tmp", "rm_rf"},
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

	for _, cmd := range []string{
		"git checkout main", "git checkout -b feature", "git restore --staged file.go",
		"git reset --soft HEAD~1", "rm file.txt",
	} {
		if pats := detectDestructiveInShellCommand(cmd); len(pats) != 0 {
			t.Errorf("%q must not fire, got %+v", cmd, pats)
		}
	}
}

func TestForcePushQuotedWrapperForm(t *testing.T) {
	if !isForcePushCommand(`sh -c "git push --force origin main"`) {
		t.Fatal("quoted wrapper force push must be detected")
	}
	if !isForcePushCommand("bash -c 'git push -f origin main'") {
		t.Fatal("single-quoted wrapper force push must be detected")
	}

	if !isForcePushCommand("git push -f origin main") {
		t.Fatal("plain force push must stay detected")
	}
	if isForcePushCommand("git push origin main") {
		t.Fatal("plain push must stay clean")
	}
}

func TestForcePushNewlineSeparatedSecondCommand(t *testing.T) {
	if isForcePushCommand("git push origin main\nmake -f Makefile.build") {
		t.Fatal("second-line -f after a newline must not fire force_push")
	}
	if !isForcePushCommand("git push --force origin main") {
		t.Fatal("single-line force push must stay detected")
	}
}

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

func TestGitDiscardAllWorktreeSemicolonBranchCreate(t *testing.T) {
	mustMatch := []string{

		"git restore --worktree .",
		"git restore --staged --worktree .",
		"git restore --source=HEAD --worktree .",

		"git checkout . ; git add -A",
		"git restore . ; echo done",
	}
	for _, cmd := range mustMatch {
		if !reGitDiscardAll.MatchString(cmd) {
			t.Errorf("must match: %q", cmd)
		}
	}

	mustStayClean := []string{

		"git restore --staged file.go",
		"git restore --staged .",
	}
	for _, cmd := range mustStayClean {
		if reGitDiscardAll.MatchString(cmd) {
			t.Errorf("must stay clean: %q", cmd)
		}
	}

	if got := detectDestructiveInShellCommand("git checkout -b feature ."); hasPattern(got, "discard_all") {
		t.Error("checkout -b feature . must not fire discard_all (git rejects -b with a pathspec)")
	}

	if !hasPattern(detectDestructiveInShellCommand("git restore --worktree ."), "discard_all") {
		t.Error("restore --worktree . must fire discard_all")
	}
	if !hasPattern(detectDestructiveInShellCommand("git checkout -f ."), "discard_all") {
		t.Error("checkout -f . must keep firing discard_all")
	}

	for _, cmd := range []string{
		"git checkout -f . && git checkout -b fix",
		"git checkout -- . && git checkout -b fix",
		"git checkout -f .; git checkout -b x",
		"git checkout -f . || git checkout -b x",
		"git checkout -f . && git switch -c fix",
	} {
		if !hasPattern(detectDestructiveInShellCommand(cmd), "discard_all") {
			t.Errorf("chained real discard must keep firing discard_all: %q", cmd)
		}
	}

	for _, cmd := range []string{
		"git checkout -b feature .",
		"echo hi && git checkout -b feature .",
	} {
		if hasPattern(detectDestructiveInShellCommand(cmd), "discard_all") {
			t.Errorf("same-segment -b + pathspec must stay exempt: %q", cmd)
		}
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
