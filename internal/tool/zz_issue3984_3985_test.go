package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3984FastForwardCompletesCleanly(t *testing.T) {
	parent, wt := newMergeWorktreeFixture(t)

	mwGit(t, parent, "config", "status.showUntrackedFiles", "no")

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

	if out := mwGit(t, parent, "log", "-1", "--format=%s"); out != "ff-able child change" {
		t.Fatalf("parent HEAD did not advance to the branch tip: %s", out)
	}
}

func TestIssue3985VerifyFailAbortRestoresParent(t *testing.T) {
	parent, wt := newMergeWorktreeFixture(t)

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

	if ok, out := mwGitOK(parent, "rev-parse", "-q", "--verify", "MERGE_HEAD"); ok {
		t.Fatalf("MERGE_HEAD must be gone after successful abort, got: %s", out)
	}
	if st := mwGit(t, parent, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Fatalf("parent tree must be clean after abort, got: %s", st)
	}
}
