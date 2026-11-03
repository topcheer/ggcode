package a2a

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIssue4562_WorktreeDotGitFileIsAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = base
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "main")
	if err := os.WriteFile(filepath.Join(base, "main", "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	run("-C", "main", "add", ".")
	run("-C", "main", "commit", "-qm", "init")
	run("-C", "main", "worktree", "add", "-q", "../wt")

	info, err := os.Stat(filepath.Join(base, "wt", ".git"))
	if err != nil || info.IsDir() {
		t.Fatalf("test premise: worktree .git should be a file, got dir=%v err=%v", info != nil && info.IsDir(), err)
	}

	if meta := detectWorkspaceMeta(filepath.Join(base, "wt")); !meta.HasGit {
		t.Fatal("#4562: a git worktree (.git as file) must report HasGit=true - git-ops was rejected and the agent card exposed has_git=false")
	}
	if meta := detectWorkspaceMeta(filepath.Join(base, "main")); !meta.HasGit {
		t.Fatal("plain repo (.git dir) must keep HasGit=true")
	}
	if meta := detectWorkspaceMeta(t.TempDir()); meta.HasGit {
		t.Fatal("non-repo dir must stay HasGit=false")
	}
}

func TestIssue4562_DotGitPointerFileDetected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere/repo.git\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if meta := detectWorkspaceMeta(dir); !meta.HasGit {
		t.Fatal("#4562: a .git pointer file (submodule/worktree layout) must count as a git repository")
	}
}
