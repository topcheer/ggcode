package tool

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func zz4778Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestIssue4778_WorktreeBasesOnOriginMain(t *testing.T) {

	origin := t.TempDir()
	zz4778Git(t, origin, "init", "-q", "--bare", "-b", "main", origin)
	seed := t.TempDir()
	zz4778Git(t, seed, "init", "-q", "-b", "main")
	zz4778Git(t, seed, "config", "user.email", "t@t")
	zz4778Git(t, seed, "config", "user.name", "t")
	zz4778Git(t, seed, "commit", "--allow-empty", "-m", "A")
	zz4778Git(t, seed, "remote", "add", "origin", origin)
	zz4778Git(t, seed, "push", "-q", "origin", "main")
	local := t.TempDir()
	zz4778Git(t, seed, "clone", "-q", origin, local)

	zz4778Git(t, seed, "commit", "--allow-empty", "-m", "B")
	zz4778Git(t, seed, "push", "-q", "origin", "main")

	localB := zz4778Git(t, local, "ls-remote", origin, "refs/heads/main")

	tool := EnterWorktree{WorkingDir: local}
	res, err := tool.Execute(context.Background(), []byte(`{"name":"wt4778","description":"d"}`))
	if err != nil || res.IsError {
		t.Fatalf("enter_worktree failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "Base: origin/main") {
		t.Fatalf("result must name the origin/main base:\n%s", res.Content)
	}

	wt := t.TempDir()
	_ = wt
	wtPath := zz4778Git(t, local, "worktree", "list", "--porcelain")
	_ = wtPath
	branchHead := zz4778Git(t, local, "rev-parse", "wt4778")
	remoteHash := strings.Fields(localB)
	if len(remoteHash) == 0 || !strings.HasPrefix(branchHead, remoteHash[0]) {
		t.Fatalf("worktree branch %s must equal remote main tip %v", branchHead, remoteHash)
	}
}

func TestIssue4778_FallbackToLocalMainWarns(t *testing.T) {

	dir := t.TempDir()
	zz4778Git(t, dir, "init", "-q", "-b", "main")
	zz4778Git(t, dir, "config", "user.email", "t@t")
	zz4778Git(t, dir, "config", "user.name", "t")

	zz4778Git(t, dir, "commit", "--allow-empty", "-m", "m1")
	mainTip := zz4778Git(t, dir, "rev-parse", "main")
	zz4778Git(t, dir, "checkout", "-q", "-b", "feat")
	zz4778Git(t, dir, "commit", "--allow-empty", "-m", "f1")

	tool := EnterWorktree{WorkingDir: dir}
	res, err := tool.Execute(context.Background(), []byte(`{"name":"wt4778b","description":"d"}`))
	if err != nil || res.IsError {
		t.Fatalf("enter_worktree failed: %v %s", err, res.Content)
	}
	for _, want := range []string{"Warning:", "NOT origin/main", "#4778"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("fallback note missing %q:\n%s", want, res.Content)
		}
	}
	branchHead := zz4778Git(t, dir, "rev-parse", "wt4778b")
	if branchHead != mainTip {
		t.Fatalf("worktree must base on local main %s, got %s", mainTip, branchHead)
	}
}
