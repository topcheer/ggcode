package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func zz4803Agent(dir string) *Agent {
	a := &Agent{workingDir: dir}
	return a
}

func zz4803Run(t *testing.T, a *Agent, cmd string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"command": cmd})
	return a.checkSharedTreeBranchGuard("run_command", args, "done")
}

func TestIssue4803_WarnsOnFixBranchInSharedTree(t *testing.T) {

	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	msg := zz4803Run(t, zz4803Agent(dir), "echo seeded")
	_ = msg
	resetSharedTreeBranchGuard()
	shared := t.TempDir()
	zz4803Git(t, shared, "init", "-b", "fix-branch-x")

	a := zz4803Agent(shared)
	msg = zz4803Run(t, a, "go test ./...")
	if msg == "" {
		t.Fatal("validation on non-main shared-tree branch must warn")
	}
	if !strings.Contains(msg, "fix-branch-x") || !strings.Contains(msg, "SHARED") {
		t.Fatalf("warning must name branch and shared-tree risk, got: %s", msg)
	}

	if again := zz4803Run(t, a, "make test"); again != "" {
		t.Fatalf("warn-once violated: %s", again)
	}
}

func TestIssue4803_SilentOnMainAndWorktreeAndNonValidation(t *testing.T) {
	resetSharedTreeBranchGuard()
	onMain := t.TempDir()
	zz4803Git(t, onMain, "init", "-b", "main")
	if msg := zz4803Run(t, zz4803Agent(onMain), "go test ./..."); msg != "" {
		t.Fatalf("main branch must stay silent, got: %s", msg)
	}
	resetSharedTreeBranchGuard()
	wt := filepath.Join(t.TempDir(), "proj", ".ggcode", "worktrees", "fix-x")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	zz4803Git(t, wt, "init", "-b", "fix-anything")
	if msg := zz4803Run(t, zz4803Agent(wt), "go test ./..."); msg != "" {
		t.Fatalf("worktree trees are SUPPOSED to be on fix branches - must stay silent, got: %s", msg)
	}
	resetSharedTreeBranchGuard()
	if msg := zz4803Run(t, zz4803Agent(onMain), "ls -la"); msg != "" {
		t.Fatalf("non-validation command must stay silent, got: %s", msg)
	}
}

func zz4803Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}
