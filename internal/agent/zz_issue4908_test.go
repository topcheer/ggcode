package agent

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// #4908: the warn-once latch must be consumed only when a warning is
// actually emitted. A first validation while the tree is on main must NOT
// silence the guard for a later mid-run switch to a fix branch; and a
// mid-run switch to a NEW branch must warn again (per-branch latch).
func zz4908Repo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	steps := [][]string{
		{"init", "-q"},
		{"checkout", "-q", "-b", "main"},
		{"commit", "-q", "--allow-empty", "-m", "x"},
	}
	if branch != "main" {
		steps = append(steps, []string{"checkout", "-q", "-b", branch})
	}
	for _, args := range steps {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	return dir
}

func zz4908Run(t *testing.T, dir, cmd string) string {
	t.Helper()
	a := &Agent{workingDir: dir}
	args, _ := json.Marshal(map[string]string{"command": cmd})
	return a.checkSharedTreeBranchGuard("run_command", args, "done")
}

func TestIssue4908_MainFirstDoesNotConsumeLatch(t *testing.T) {
	resetSharedTreeBranchGuard()
	t.Cleanup(resetSharedTreeBranchGuard)

	// First validation on main: silent, latch untouched.
	dirMain := zz4908Repo(t, "main")
	if w := zz4908Run(t, dirMain, "go build ./..."); w != "" {
		t.Fatalf("on main the guard must stay silent, got %q", w)
	}
	sharedTreeBranchMu.Lock()
	latch := sharedTreeWarnedBranch
	sharedTreeBranchMu.Unlock()
	if latch != "" {
		t.Fatalf("silent main validation must not consume the latch, got %q", latch)
	}

	// Later validation on a fix branch (mid-run switch scenario): warns.
	dirFix := zz4908Repo(t, "fix/issue-4908")
	w := zz4908Run(t, dirFix, "go build ./...")
	if w == "" {
		t.Fatal("after a main-tree validation, a fix-branch validation must still warn (#4908)")
	}
	if !strings.Contains(w, "fix/issue-4908") {
		t.Fatalf("warning must name the branch: %q", w)
	}
}

func TestIssue4908_PerBranchLatchMidRunSwitch(t *testing.T) {
	resetSharedTreeBranchGuard()
	t.Cleanup(resetSharedTreeBranchGuard)

	dir := zz4908Repo(t, "fix/first")
	if w1 := zz4908Run(t, dir, "go build ./..."); w1 == "" {
		t.Fatal("first validation on fix branch must warn")
	}
	if w2 := zz4908Run(t, dir, "go build ./..."); w2 != "" {
		t.Fatalf("same-branch re-validation must stay quiet, got %q", w2)
	}
	// Mid-run switch to a NEW branch: warns again.
	if out, err := exec.Command("git", "-C", dir, "checkout", "-q", "-b", "fix/second").CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v %s", err, out)
	}
	if w3 := zz4908Run(t, dir, "go test ./..."); w3 == "" || !strings.Contains(w3, "fix/second") {
		t.Fatalf("mid-run switch to a NEW branch must warn naming the branch, got %q", w3)
	}
}
