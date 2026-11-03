package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// issue4790WorktreeFixture creates a git repo with committed src+test, then a
// ggcode-style worktree branch carrying a NEW test file for a later source
// edit — the S-20 stale-provenance scenario.
func issue4790WorktreeFixture(t *testing.T) (mainDir, wtDir string) {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git not available")
	}

	mainDir = t.TempDir()
	gitInit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitInit(mainDir, "init", "-b", "main")
	os.WriteFile(filepath.Join(mainDir, "svc.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(mainDir, "svc_test.go"), []byte("package main\n"), 0o644)
	gitInit(mainDir, "add", "-A")
	gitInit(mainDir, "commit", "-m", "base")

	wtDir = filepath.Join(mainDir, ".ggcode", "worktrees", "fix-flow")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(mainDir, "worktree", "add", "-b", "fix-flow", wtDir, "main")

	// Earlier run of the flow authored a companion test on the branch.
	os.WriteFile(filepath.Join(wtDir, "handler.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(wtDir, "handler_test.go"), []byte("package main\n"), 0o644)
	gitInit(wtDir, "add", "-A")
	gitInit(wtDir, "commit", "-m", "add handler + self-authored test")
	return mainDir, wtDir
}

// Pre-fix pin: companion test authored earlier in the worktree flow (already
// committed on the branch) is reported as "not updated" — stale tracking state.
func TestIssue4790SelfAuthoredTestInWorktreeNotFlagged(t *testing.T) {
	_, wtDir := issue4790WorktreeFixture(t)

	g := newCompanionGuardState()
	g.refreshProvenance(wtDir)

	if len(g.selfAuthored) == 0 {
		t.Fatal("expected provenance refresh to record self-authored files")
	}

	stats := newRunStats("test")
	stats.recordFileEdit(filepath.Join(wtDir, "handler.go"))

	msg := g.checkCompanionFiles(stats, wtDir)
	if msg != "" {
		t.Fatalf("S-20 regression: self-authored worktree test flagged as not updated: %s", msg)
	}
}

// Control: a PRE-EXISTING companion test (committed on main, unchanged on the
// branch) must still be reported when only the source is edited this run.
func TestIssue4790PreExistingCompanionStillFlagged(t *testing.T) {
	_, wtDir := issue4790WorktreeFixture(t)

	g := newCompanionGuardState()
	g.refreshProvenance(wtDir)

	stats := newRunStats("test")
	stats.recordFileEdit(filepath.Join(wtDir, "svc.go"))

	msg := g.checkCompanionFiles(stats, wtDir)
	if msg == "" {
		t.Fatal("expected advisory for pre-existing companion test not edited this run")
	}
	if !strings.Contains(msg, "svc_test.go") {
		t.Fatalf("expected message to mention svc_test.go, got: %s", msg)
	}
}

// Untracked (not yet committed) self-authored tests must also be forgiven.
func TestIssue4790UntrackedSelfAuthoredTestForgiven(t *testing.T) {
	_, wtDir := issue4790WorktreeFixture(t)

	os.WriteFile(filepath.Join(wtDir, "calc.go"), []byte("package main\n"), 0o644)
	os.WriteFile(filepath.Join(wtDir, "calc_test.go"), []byte("package main\n"), 0o644)

	g := newCompanionGuardState()
	g.refreshProvenance(wtDir)

	stats := newRunStats("test")
	stats.recordFileEdit(filepath.Join(wtDir, "calc.go"))

	if msg := g.checkCompanionFiles(stats, wtDir); msg != "" {
		t.Fatalf("untracked self-authored test flagged: %s", msg)
	}
}

// Outside a ggcode worktree the refresh must be a no-op so legacy behavior is
// preserved exactly.
func TestIssue4790NonWorktreeNoProvenance(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	dir := t.TempDir()

	g := newCompanionGuardState()
	g.refreshProvenance(dir)
	if len(g.selfAuthored) != 0 || g.base != "" {
		t.Fatalf("expected no provenance outside a worktree, got base=%q selfAuthored=%v", g.base, g.selfAuthored)
	}
}
