package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3984: --no-commit cannot stop a fast-forward; the completed ff used to be
// routed into doFinalize, whose MERGE_HEAD guard misreported it as "no merge
// in progress" - a dead loop over an already-successful merge.
func TestIssue3984FastForwardCompletesCleanly(t *testing.T) {
	parent, wt := newMergeWorktreeFixture(t)
	// The ff scenario forbids parent-side commits (the parent HEAD must stay
	// an ancestor of the branch tip), so the fixture's untracked .ggcode/
	// directory cannot be absorbed by a parent commit like the other merge
	// tests do - hide untracked files instead (tracked-file dirtiness, the
	// thing the gate exists for, stays visible).
	mwGit(t, parent, "config", "status.showUntrackedFiles", "no")
	// Single commit on the worktree branch (direct descendant of main HEAD)
	// makes the parent-side merge a fast-forward.
	if err := os.WriteFile(filepath.Join(wt, "child.txt"), []byte("child\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mwCommit(t, wt, "ff-able child change")
	res := mwCall(t, parent, wt, "merge", "")
	if res.IsError {
		t.Fatalf("fast-forward merge must succeed, got error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "fast-forward merge complete") {
		t.Fatalf("ff must be reported as complete, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "no merge in progress") {
		t.Fatalf("ff misrouted into the finalize guard (pre-#3984 bug): %s", res.Content)
	}
	// The ff actually happened: parent HEAD is the branch tip.
	if out := mwGit(t, parent, "log", "-1", "--format=%s"); out != "ff-able child change" {
		t.Fatalf("parent HEAD did not advance to the branch tip: %s", out)
	}
}

// #3985: when verify_command fails, the abort's ACTUAL outcome must be
// reported; the "parent tree restored" claim may only appear when the abort
// succeeded. (The abort-failure path is defensive and needs an artificially
// broken mid-merge state, so this probe pins the success path's honesty plus
// the restored-state invariants behind the claim.)
func TestIssue3985VerifyFailAbortRestoresParent(t *testing.T) {
	parent, wt := newMergeWorktreeFixture(t)
	// #3985's real trigger chain: conflicting merge -> manual resolve ->
	// action=finalize with verify_command (verify only takes effect on the
	// explicit finalize path; clean merges auto-finalize with verify="").
	if err := os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("line1\nline2-wt\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mwCommit(t, wt, "worktree-side change")
	if err := os.WriteFile(filepath.Join(parent, "hello.txt"), []byte("line1\nline2-parent\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mwCommit(t, parent, "parent-side divergence")
	res := mwCall(t, parent, wt, "merge", "")
	if res.IsError {
		t.Fatalf("conflict must yield an arbitration report, got: %s", res.Content)
	}
	// Resolve the conflict in the parent tree, then finalize with a failing
	// verify command.
	if err := os.WriteFile(filepath.Join(parent, "hello.txt"), []byte("line1\nline2-resolved\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mwGit(t, parent, "add", "hello.txt")
	fin := mwCall(t, parent, wt, "finalize", "exit 1")
	if !fin.IsError {
		t.Fatalf("verify_command exit 1 must fail finalize, got: %s", fin.Content)
	}
	if !strings.Contains(fin.Content, "verify_command failed") {
		t.Fatalf("verify failure must be named, got: %s", fin.Content)
	}
	if !strings.Contains(fin.Content, "merge aborted, parent tree restored") {
		t.Fatalf("successful abort must report restored state, got: %s", fin.Content)
	}
	// Invariants behind the claim: no mid-merge residue, tracked tree clean.
	if ok, out := mwGitOK(parent, "rev-parse", "-q", "--verify", "MERGE_HEAD"); ok {
		t.Fatalf("MERGE_HEAD must be gone after successful abort, got: %s", out)
	}
	if st := mwGit(t, parent, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Fatalf("parent tree must be clean after abort, got: %s", st)
	}
}
