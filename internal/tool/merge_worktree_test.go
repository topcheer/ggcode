package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mwGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_PAGER=cat", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newMergeWorktreeFixture(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	mwGit(t, repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mwGit(t, repo, "add", "-A")
	mwGit(t, repo, "commit", "-m", "base: three lines")

	wt := filepath.Join(repo, ".ggcode", "worktrees", "task")
	mwGit(t, repo, "worktree", "add", "-b", "wt-task", wt, "main")
	return repo, wt
}

func mwCommit(t *testing.T, dir, subject string) {
	t.Helper()
	mwGit(t, dir, "add", "-A")
	mwGit(t, dir, "commit", "-m", subject)
}

func mwGitOK(dir string, args ...string) (bool, string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	return err == nil, strings.TrimSpace(string(out))
}

func mwCall(t *testing.T, workingDir, worktree, action, verify string) Result {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"worktree": worktree, "action": action, "verify_command": verify,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := MergeWorktree{WorkingDir: workingDir}.Execute(context.Background(), payload)
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	return res
}

func TestMergeWorktreeCleanAutoFinalize(t *testing.T) {

	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo, wt := newMergeWorktreeFixture(t)

	os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("from worktree\n"), 0644)
	mwCommit(t, wt, "task: add feature file")
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2-parent\nline3\n"), 0644)
	mwCommit(t, repo, "parent: tweak line2")

	res := mwCall(t, repo, wt, "merge", "")
	if res.IsError {
		t.Fatalf("clean merge failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Merged wt-task") {
		t.Fatalf("unexpected content: %s", res.Content)
	}

	log := mwGit(t, repo, "log", "--oneline", "-3")
	if !strings.Contains(log, "Merge branch") {
		t.Fatalf("no merge commit in log:\n%s", log)
	}
	feat, err := os.ReadFile(filepath.Join(repo, "feature.txt"))
	if err != nil || string(feat) != "from worktree\n" {
		t.Fatalf("worktree content missing after merge: %q %v", feat, err)
	}
	if st := mwGit(t, repo, "status", "--porcelain"); st != "" {
		t.Fatalf("parent tree dirty after clean merge: %s", st)
	}
}

func TestMergeWorktreeConflictReportAndAbort(t *testing.T) {
	repo, wt := newMergeWorktreeFixture(t)

	os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("line1\nline2-worktree\nline3\n"), 0644)
	mwCommit(t, wt, "task: reword line2 for the feature")
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2-parent\nline3\n"), 0644)
	mwCommit(t, repo, "parent: reword line2 for hotfix")

	res := mwCall(t, repo, wt, "merge", "")
	if res.IsError {
		t.Fatalf("conflict merge should return a report, not an error: %s", res.Content)
	}
	for _, want := range []string{"hello.txt", "conflict region", "wt-task", "reword line2 for the feature", "action=finalize"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("arbitration report missing %q:\n%s", want, res.Content)
		}
	}

	if ok, _ := mwGitOK(repo, "rev-parse", "-q", "--verify", "MERGE_HEAD"); !ok {
		t.Fatal("expected in-progress merge state (MERGE_HEAD) to be kept")
	}

	ab := mwCall(t, repo, wt, "abort", "")
	if ab.IsError {
		t.Fatalf("abort failed: %s", ab.Content)
	}
	if st := mwGit(t, repo, "status", "--porcelain"); st != "" {
		t.Fatalf("parent tree dirty after abort: %s", st)
	}
	got, _ := os.ReadFile(filepath.Join(repo, "hello.txt"))
	if string(got) != "line1\nline2-parent\nline3\n" {
		t.Fatalf("abort did not restore parent content: %q", got)
	}
	if ok, _ := mwGitOK(repo, "rev-parse", "--verify", "wt-task"); !ok {
		t.Fatal("abort must never delete the worktree branch")
	}
}

func TestMergeWorktreeFinalizeAfterManualResolve(t *testing.T) {
	repo, wt := newMergeWorktreeFixture(t)
	os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("line1\nline2-worktree\nline3\n"), 0644)
	mwCommit(t, wt, "task: reword line2")
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2-parent\nline3\n"), 0644)
	mwCommit(t, repo, "parent: reword line2 differently")

	if res := mwCall(t, repo, wt, "merge", ""); res.IsError {
		t.Fatalf("merge start failed: %s", res.Content)
	}

	if res := mwCall(t, repo, wt, "finalize", ""); !res.IsError {
		t.Fatalf("finalize should refuse unresolved markers, got: %s", res.Content)
	}

	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2-synthesis\nline3\n"), 0644)
	mwGit(t, repo, "add", "hello.txt")

	bad := mwCall(t, repo, wt, "finalize", "false")
	if !bad.IsError || !strings.Contains(bad.Content, "verify_command failed") {
		t.Fatalf("verify failure should abort fail-closed, got: %+v", bad)
	}
	if st := mwGit(t, repo, "status", "--porcelain"); st != "" {
		t.Fatalf("parent tree dirty after verify-gated abort: %s", st)
	}
	if ok, _ := mwGitOK(repo, "rev-parse", "-q", "--verify", "MERGE_HEAD"); ok {
		t.Fatal("verify-gated abort should end the merge state")
	}

	if res := mwCall(t, repo, wt, "merge", ""); res.IsError {
		t.Fatalf("second merge failed: %s", res.Content)
	}
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2-synthesis\nline3\n"), 0644)
	mwGit(t, repo, "add", "hello.txt")
	ok := mwCall(t, repo, wt, "finalize", "true")
	if ok.IsError {
		t.Fatalf("finalize with passing gate failed: %s", ok.Content)
	}
	got, _ := os.ReadFile(filepath.Join(repo, "hello.txt"))
	if string(got) != "line1\nline2-synthesis\nline3\n" {
		t.Fatalf("resolved content not committed: %q", got)
	}
}

func TestMergeWorktreeSafetyGates(t *testing.T) {
	repo, wt := newMergeWorktreeFixture(t)

	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("line1\nline2-dirty\nline3\n"), 0644)
	res := mwCall(t, repo, wt, "merge", "")
	if !res.IsError || !strings.Contains(res.Content, "dirty") {
		t.Fatalf("dirty parent tree must be refused, got: %+v", res)
	}
	mwGit(t, repo, "checkout", "--", "hello.txt")

	outside := t.TempDir()
	mwGit(t, outside, "init", "-b", "main")
	res = mwCall(t, repo, outside, "merge", "")
	if !res.IsError || !strings.Contains(res.Content, "not a ggcode-managed worktree") {
		t.Fatalf("foreign worktree must be refused, got: %+v", res)
	}

	res = mwCall(t, repo, wt, "finalize", "")
	if !res.IsError || !strings.Contains(res.Content, "no merge in progress") {
		t.Fatalf("finalize without merge state must be refused, got: %+v", res)
	}
}
