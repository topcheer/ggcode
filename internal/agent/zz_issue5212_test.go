package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #5212: raw `git branch -D` bypassed every tool-seam guard and silently
// deleted a completed unpushed fix branch. The pre-flight probe must block
// while unpushed commits exist, name SHAs, and honor an explicit-intent
// escape; fully-pushed branches and probe failures must not block.

func setupProbeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	write("a.txt", "one\n")
	run("add", ".")
	run("commit", "-q", "-m", "c1")
	run("branch", "feature")
	run("remote", "add", "origin", dir)    // self-remote: only feature is pushed
	run("push", "-q", "origin", "feature") // c1 reachable via origin/feature
	write("b.txt", "two\n")
	run("add", ".")
	run("commit", "-q", "-m", "c2") // unpushed commit on main
	return dir
}

func TestIssue5212_BlocksForceDeleteOfUnpushedBranch(t *testing.T) {
	dir := setupProbeRepo(t) // main has 1 unpushed commit (c2)
	msg := branchDeleteGuardBlock(dir, "git branch -D feature-does-not-exist && git branch -D main")
	if msg == "" {
		t.Fatal("expected block: main carries unpushed commit c2")
	}
	for _, pin := range []string{"branch-delete-guard", "unpushed commit(s)", "GGCODE_ALLOW_UNPUSHED_DELETE=1", "reflog"} {
		if !strings.Contains(msg, pin) {
			t.Errorf("block message missing %q:\n%s", pin, msg)
		}
	}
}

func TestIssue5212_PushedBranchNotBlocked(t *testing.T) {
	dir := setupProbeRepo(t)
	// feature == origin/feature, no unpushed commits
	if msg := branchDeleteGuardBlock(dir, "git branch -D feature"); msg != "" {
		t.Errorf("fully-pushed branch must not block, got:\n%s", msg)
	}
}

func TestIssue5212_ExplicitIntentEscapeHonored(t *testing.T) {
	dir := setupProbeRepo(t)
	msg := branchDeleteGuardBlock(dir, "GGCODE_ALLOW_UNPUSHED_DELETE=1 git branch -D main")
	if msg != "" {
		t.Errorf("explicit-intent escape must pass, got:\n%s", msg)
	}
}

func TestIssue5212_SafeDeleteNotProbed(t *testing.T) {
	// -d (safe) on an unmerged branch is refused by git itself; the probe
	// must not fire for it (no -D/--delete --force present).
	if got := extractForceDeletedBranches("git branch -d main"); len(got) != 0 {
		t.Errorf("safe delete must not be treated as force delete, got %v", got)
	}
	if got := extractForceDeletedBranches("git branch --delete main"); len(got) != 0 {
		t.Errorf("plain --delete must not be treated as force delete, got %v", got)
	}
	if got := extractForceDeletedBranches("git branch --delete --force main"); len(got) != 1 || got[0] != "main" {
		t.Errorf("--delete --force must parse, got %v", got)
	}
	if got := extractForceDeletedBranches("cd /tmp && git branch -df foo bar | head -1"); len(got) != 2 || got[0] != "foo" || got[1] != "bar" {
		t.Errorf("-df with chain must parse both branches, got %v", got)
	}
}

func TestIssue5212_ProbeFailureFailsSafe(t *testing.T) {
	// nonexistent workDir: rev-list probe fails -> no block (fail-safe)
	msg := branchDeleteGuardBlock(filepath.Join(t.TempDir(), "nope"), "git branch -D main")
	if msg != "" {
		t.Errorf("probe failure must not block, got:\n%s", msg)
	}
}

func TestIssue5212_MultipleOffendersAllNamed(t *testing.T) {
	dir := setupProbeRepo(t)
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("branch", "second-dirty")
	run("commit", "-q", "--allow-empty", "-m", "c3") // second unpushed commit on main
	msg := branchDeleteGuardBlock(dir, fmt.Sprintf("git branch -D main second-dirty feature"))
	if !strings.Contains(msg, "main") || !strings.Contains(msg, "second-dirty") {
		t.Errorf("both offenders must be named:\n%s", msg)
	}
	if strings.Contains(msg, "- feature:") {
		t.Errorf("pushed branch must not be listed as offender:\n%s", msg)
	}
}

// #5212 ownership hardening: a force-delete naming more branches than the
// probe budget must FAIL CLOSED (a silent cap is the same silent-loss
// failure mode), and the explicit-intent escape must be an env assignment
// prefixing a git command - not a bare substring anywhere in the command.
func TestIssue5212_ProbeBudgetFailsClosed(t *testing.T) {
	names := []string{}
	for i := 0; i < 13; i++ {
		names = append(names, fmt.Sprintf("b%d", i))
	}
	cmd := "git branch -D " + strings.Join(names, " ")
	if got := extractForceDeletedBranches(cmd); len(got) != 13 || got[12] != probeBudgetMarker {
		t.Fatalf("expected 12 branches + sentinel, got %d: %v", len(got), got)
	}
	msg := branchDeleteGuardBlock(t.TempDir(), cmd)
	for _, pin := range []string{"exceeds the probe budget", "GGCODE_ALLOW_UNPUSHED_DELETE=1"} {
		if !strings.Contains(msg, pin) {
			t.Errorf("budget block missing %q:\n%s", pin, msg)
		}
	}
	// The escape still works for the oversized delete.
	if msg := branchDeleteGuardBlock(t.TempDir(), "GGCODE_ALLOW_UNPUSHED_DELETE=1 "+cmd); msg != "" {
		t.Errorf("escape over budget-delete must pass, got:\n%s", msg)
	}
}

func TestIssue5212_EscapeMustBeCommandPrefixNotSubstring(t *testing.T) {
	dir := setupProbeRepo(t)
	// The token merely quoted/echoed does NOT authorize a dirty delete.
	if msg := branchDeleteGuardBlock(dir, `echo "GGCODE_ALLOW_UNPUSHED_DELETE=1" && git branch -D main`); msg == "" {
		t.Fatal("quoted-token echo must NOT count as explicit intent")
	}
	// A real env-prefix assignment (possibly after a benign chain) does.
	if msg := branchDeleteGuardBlock(dir, "cd /tmp && GGCODE_ALLOW_UNPUSHED_DELETE=1 git branch -D main"); msg != "" {
		t.Errorf("chained env-prefix escape must pass, got:\n%s", msg)
	}
}
