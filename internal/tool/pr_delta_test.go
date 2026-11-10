package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// prDeltaGit runs git in dir and fails the test on error.
func prDeltaGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return prDeltaGitEnv(t, dir, nil, args...)
}

// prDeltaCherryPick cherry-picks with a pinned committer date so the picked
// commit deterministically gets a different sha (but the same patch) as the
// original. Without this, a cherry-pick within the same second with the same
// committer, parent, tree and message reproduces the *identical* commit sha,
// which would make the commit a true ancestor of base instead of a
// patch-equivalent duplicate.
func prDeltaCherryPick(t *testing.T, dir, sha string) {
	t.Helper()
	prDeltaGitEnv(t, dir, []string{"GIT_COMMITTER_DATE=2020-01-01T00:00:00Z"}, "cherry-pick", sha)
}

func prDeltaGitEnv(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// prDeltaInitRepo creates a temp git repo with a root commit on main.
func prDeltaInitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prDeltaGit(t, dir, "init", "-b", "main")
	prDeltaGit(t, dir, "config", "user.email", "test@example.com")
	prDeltaGit(t, dir, "config", "user.name", "Test")
	prDeltaWriteFile(t, dir, "root.txt", "root\n")
	prDeltaGit(t, dir, "add", ".")
	prDeltaGit(t, dir, "commit", "-m", "root commit")
	return dir
}

func prDeltaWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	writeTestFile(t, dir+"/"+name, content)
}

func prDeltaCommit(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	prDeltaWriteFile(t, dir, name, content)
	prDeltaGit(t, dir, "add", ".")
	prDeltaGit(t, dir, "commit", "-m", msg)
	return strings.TrimSpace(prDeltaGit(t, dir, "rev-parse", "HEAD"))
}

func prDeltaRun(t *testing.T, tool PRDelta, args map[string]any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("pr_delta execute: %v", err)
	}
	return res
}

func TestPRDelta_MixedEquivalentAndDelta(t *testing.T) {
	dir := prDeltaInitRepo(t)

	prDeltaGit(t, dir, "checkout", "-b", "feature")
	prDeltaCommit(t, dir, "a.txt", "alpha\n", "add alpha")     // true delta
	f2 := prDeltaCommit(t, dir, "b.txt", "beta\n", "add beta") // will be absorbed

	// Base advances independently: cherry-picks F2 (absorbed) plus its own commit.
	prDeltaGit(t, dir, "checkout", "main")
	prDeltaCherryPick(t, dir, f2)
	prDeltaCommit(t, dir, "c.txt", "gamma\n", "add gamma")

	res := prDeltaRun(t, PRDelta{}, map[string]any{
		"pr_branch":   "feature",
		"base":        "main",
		"repo_path":   dir,
		"description": "test",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := res.Content
	if !strings.Contains(out, "[delta]") || !strings.Contains(out, "add alpha") {
		t.Errorf("expected [delta] add alpha in output:\n%s", out)
	}
	if !strings.Contains(out, "[equivalent] ") || !strings.Contains(out, "add beta") {
		t.Errorf("expected [equivalent] add beta in output:\n%s", out)
	}
	// The [equivalent] line must be the beta commit, not alpha.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "[delta]") && strings.Contains(line, "add beta") {
			t.Errorf("beta commit should be equivalent, got delta:\n%s", out)
		}
		if strings.Contains(line, "[equivalent]") && strings.Contains(line, "add alpha") {
			t.Errorf("alpha commit should be delta, got equivalent:\n%s", out)
		}
	}
	want := "1/2 commits already absorbed by main; true delta = 1 commits, files affected by delta only: 1"
	if !strings.Contains(out, want) {
		t.Errorf("summary mismatch:\nwant: %s\ngot:\n%s", want, out)
	}
	if strings.Contains(out, "fully patch-equivalent") {
		t.Errorf("should not be fully equivalent:\n%s", out)
	}
}

func TestPRDelta_FullyEquivalent(t *testing.T) {
	dir := prDeltaInitRepo(t)

	prDeltaGit(t, dir, "checkout", "-b", "feature")
	f1 := prDeltaCommit(t, dir, "a.txt", "alpha\n", "add alpha")

	prDeltaGit(t, dir, "checkout", "main")
	prDeltaCherryPick(t, dir, f1)

	res := prDeltaRun(t, PRDelta{}, map[string]any{
		"pr_branch":   "feature",
		"base":        "main",
		"repo_path":   dir,
		"description": "test",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := res.Content
	if !strings.Contains(out, "1/1 commits already absorbed by main; true delta = 0 commits") {
		t.Errorf("summary mismatch:\n%s", out)
	}
	if !strings.Contains(out, "PR is fully patch-equivalent to base - nothing to review/merge") {
		t.Errorf("expected fully patch-equivalent verdict:\n%s", out)
	}
}

func TestPRDelta_MergeCommitsSkipped(t *testing.T) {
	dir := prDeltaInitRepo(t)

	prDeltaGit(t, dir, "checkout", "-b", "feature")
	f1 := prDeltaCommit(t, dir, "a.txt", "alpha\n", "add alpha")

	// Side branch merged into feature: introduces a merge commit.
	prDeltaGit(t, dir, "checkout", "-b", "side")
	prDeltaCommit(t, dir, "s.txt", "side\n", "side work")
	prDeltaGit(t, dir, "checkout", "feature")
	prDeltaGit(t, dir, "merge", "--no-ff", "-m", "Merge branch 'side' into feature", "side")

	prDeltaGit(t, dir, "checkout", "main")
	prDeltaCherryPick(t, dir, f1)

	res := prDeltaRun(t, PRDelta{}, map[string]any{
		"pr_branch":   "feature",
		"base":        "main",
		"repo_path":   dir,
		"description": "test",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := res.Content
	if strings.Contains(out, "Merge branch") {
		t.Errorf("merge commit should be skipped:\n%s", out)
	}
	// Only the non-merge feature commits are counted: add alpha (absorbed) + side work (delta).
	if !strings.Contains(out, "1/2 commits already absorbed by main; true delta = 1 commits") {
		t.Errorf("summary mismatch:\n%s", out)
	}
	if !strings.Contains(out, "side work") || !strings.Contains(out, "[delta]") {
		t.Errorf("side work should appear as delta:\n%s", out)
	}
}

func TestPRDelta_DefaultBaseFromOriginHead(t *testing.T) {
	// Build a local bare "origin" plus a clone so origin/HEAD resolves to main.
	root := t.TempDir()
	origin := root + "/origin.git"
	work := root + "/work"

	prDeltaGit(t, root, "init", "--bare", "-b", "main", origin)

	cmd := exec.Command("git", "clone", origin, work)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	prDeltaGit(t, work, "config", "user.email", "test@example.com")
	prDeltaGit(t, work, "config", "user.name", "Test")
	prDeltaWriteFile(t, work, "root.txt", "root\n")
	prDeltaGit(t, work, "add", ".")
	prDeltaGit(t, work, "commit", "-m", "root commit")
	prDeltaGit(t, work, "push", "-u", "origin", "main")
	// The clone happened while origin was empty, so set origin/HEAD explicitly
	// (same as what `git remote set-head origin --auto` does after a fetch).
	prDeltaGit(t, work, "remote", "set-head", "origin", "main")

	prDeltaGit(t, work, "checkout", "-b", "feature")
	f1 := prDeltaCommit(t, work, "a.txt", "alpha\n", "add alpha")
	prDeltaGit(t, work, "push", "-u", "origin", "feature")

	prDeltaGit(t, work, "checkout", "main")
	prDeltaCherryPick(t, work, f1)
	prDeltaGit(t, work, "push")

	// No explicit base: must resolve origin/HEAD -> origin/main.
	res := prDeltaRun(t, PRDelta{WorkingDir: work}, map[string]any{
		"pr_branch":   "feature",
		"description": "test",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := res.Content
	if !strings.Contains(out, "origin/main") {
		t.Errorf("expected resolved base origin/main in output:\n%s", out)
	}
	if !strings.Contains(out, "PR is fully patch-equivalent to base - nothing to review/merge") {
		t.Errorf("expected fully patch-equivalent verdict:\n%s", out)
	}
}

func TestPRDelta_DefaultBaseUnresolvable(t *testing.T) {
	dir := prDeltaInitRepo(t) // no origin remote at all
	res := prDeltaRun(t, PRDelta{}, map[string]any{
		"pr_branch":   "main",
		"repo_path":   dir,
		"description": "test",
	})
	if !res.IsError {
		t.Fatalf("expected error when origin/HEAD is unresolvable, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "origin/HEAD") {
		t.Errorf("error should mention origin/HEAD resolution:\n%s", res.Content)
	}
}

func TestPRDelta_SameContentDifferentContextNotEquivalent(t *testing.T) {
	dir := prDeltaInitRepo(t)
	base := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	prDeltaWriteFile(t, dir, "f.txt", base)
	prDeltaGit(t, dir, "add", ".")
	prDeltaGit(t, dir, "commit", "-m", "add f")

	// feature: insert NEW after line 2 (context: lines 1-2 / 3-5 unchanged).
	prDeltaGit(t, dir, "checkout", "-b", "feature")
	feature := strings.Replace(base, "2\n", "2\nNEW\n", 1)
	prDeltaWriteFile(t, dir, "f.txt", feature)
	prDeltaGit(t, dir, "add", ".")
	prDeltaGit(t, dir, "commit", "-m", "insert NEW after 2")

	// main: first mutate line 3 (changes the hunk context), then apply the same
	// logical insertion. Same content, different context -> different patch-id.
	prDeltaGit(t, dir, "checkout", "main")
	mainP1 := strings.Replace(base, "3\n", "3-changed\n", 1)
	prDeltaWriteFile(t, dir, "f.txt", mainP1)
	prDeltaGit(t, dir, "add", ".")
	prDeltaGit(t, dir, "commit", "-m", "mutate line 3")
	mainP2 := strings.Replace(mainP1, "2\n", "2\nNEW\n", 1)
	prDeltaWriteFile(t, dir, "f.txt", mainP2)
	prDeltaGit(t, dir, "add", ".")
	prDeltaGit(t, dir, "commit", "-m", "insert NEW after 2 (different context)")

	res := prDeltaRun(t, PRDelta{}, map[string]any{
		"pr_branch":   "feature",
		"base":        "main",
		"repo_path":   dir,
		"description": "test",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	out := res.Content
	if !strings.Contains(out, "0/1 commits already absorbed by main; true delta = 1 commits") {
		t.Errorf("same content with different context must NOT be equivalent:\n%s", out)
	}
	if !strings.Contains(out, "[delta] ") {
		t.Errorf("expected delta line:\n%s", out)
	}
}

func TestPRDelta_BehindBaseOnly(t *testing.T) {
	dir := prDeltaInitRepo(t)

	// feature stays at root; main advances. base..feature is empty.
	prDeltaGit(t, dir, "checkout", "-b", "feature")
	prDeltaGit(t, dir, "checkout", "main")
	prDeltaCommit(t, dir, "c.txt", "gamma\n", "advance main")

	res := prDeltaRun(t, PRDelta{}, map[string]any{
		"pr_branch":   "feature",
		"base":        "main",
		"repo_path":   dir,
		"description": "test",
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No commits on feature are outside main") {
		t.Errorf("expected empty-delta message:\n%s", res.Content)
	}
}

func TestPRDelta_MissingBranchErrors(t *testing.T) {
	dir := prDeltaInitRepo(t)

	res := prDeltaRun(t, PRDelta{}, map[string]any{
		"pr_branch":   "",
		"repo_path":   dir,
		"description": "test",
	})
	if !res.IsError || !strings.Contains(res.Content, "pr_branch is required") {
		t.Errorf("expected pr_branch required error, got: %+v", res)
	}

	res = prDeltaRun(t, PRDelta{}, map[string]any{
		"pr_branch":   "no-such-branch",
		"base":        "main",
		"repo_path":   dir,
		"description": "test",
	})
	if !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Errorf("expected not-found error, got: %+v", res)
	}
}
