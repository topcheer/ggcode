package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #5034 (#5018 follow-up): RunStats.FilesEdited records tool-arg paths
// verbatim (typically absolute), while `git status --porcelain` emits
// repo-relative paths. The old form-exact comparison never matched, so
// agent-created (untracked) files silently escaped adversarial review.
// Directory entries ("?? dir/") also need reverse containment to match file
// scopes inside that directory.

func gitCmd5034(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestIssue5034AbsolutePathScopeMatchesRelativePorcelain(t *testing.T) {
	dir := t.TempDir()
	gitCmd5034(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "base.go"), []byte("package p\n"), 0o644)
	gitCmd5034(t, dir, "add", ".")
	gitCmd5034(t, dir, "commit", "-q", "-m", "base")

	// Agent creates a brand-new file; FilesEdited records the ABSOLUTE path
	// (tool-arg verbatim, as recordFileEdit does).
	created := filepath.Join(dir, "brand_new.go")
	os.WriteFile(created, []byte("package p\n\nconst New = 1\n"), 0o644)
	// Another agent's untracked leftover stays out of scope.
	os.WriteFile(filepath.Join(dir, "theirleftover.txt"), []byte("junk\n"), 0o644)

	a := &Agent{}
	a.SetWorkingDir(dir)

	diff := a.collectAdversarialDiff([]string{created})
	if !strings.Contains(diff, "brand_new.go") {
		t.Fatalf("absolute scope must match the relative porcelain entry for the agent-created file (#5034):\n%s", diff)
	}
	if strings.Contains(diff, "theirleftover.txt") {
		t.Fatalf("out-of-scope untracked leftovers must stay filtered:\n%s", diff)
	}
}

func TestIssue5034DirectoryEntryReverseContainment(t *testing.T) {
	// Porcelain collapses a fully-untracked directory to "?? pkg/"; a file
	// scope inside it must match via reverse containment.
	dir := t.TempDir()
	gitCmd5034(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "base.go"), []byte("package p\n"), 0o644)
	gitCmd5034(t, dir, "add", ".")
	gitCmd5034(t, dir, "commit", "-q", "-m", "base")
	os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "pkg", "new.go"), []byte("package pkg\n"), 0o644)

	a := &Agent{}
	a.SetWorkingDir(dir)

	diff := a.collectAdversarialDiff([]string{filepath.Join(dir, "pkg", "new.go")})
	if !strings.Contains(diff, "pkg/") {
		t.Fatalf("file scope inside an untracked dir must match the '?? pkg/' entry (#5034):\n%s", diff)
	}
}

func TestIssue5034PureFunctionForms(t *testing.T) {
	wd := "/repo"
	abs := []string{"/repo/a.go"}
	if !untrackedLineInPaths("?? a.go", abs, wd) {
		t.Fatal("absolute scope /repo/a.go must match relative porcelain a.go")
	}
	if untrackedLineInPaths("?? a.go", []string{"/other/a.go"}, wd) {
		t.Fatal("scope outside workingDir must not match")
	}
	// Directory entry reverse containment.
	if !untrackedLineInPaths("?? pkg/", []string{"/repo/pkg/new.go"}, wd) {
		t.Fatal("'?? pkg/' must match file scope inside the directory")
	}
	if !untrackedLineInPaths("?? pkg", []string{"/repo/pkg/new.go"}, wd) {
		t.Fatal("trailing-slash-less directory form must also match")
	}
	if untrackedLineInPaths("?? other/x.go", []string{"/repo/pkg/new.go"}, wd) {
		t.Fatal("unrelated directory must not match")
	}
	// Scope escaping the workingDir (.. after Rel) keeps its absolute form
	// and cannot accidentally match a relative entry.
	if untrackedLineInPaths("?? secrets.go", []string{"/etc/secrets.go"}, wd) {
		t.Fatal("out-of-tree scope must not match in-tree porcelain entries")
	}
}
