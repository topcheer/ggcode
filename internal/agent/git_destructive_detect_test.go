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
