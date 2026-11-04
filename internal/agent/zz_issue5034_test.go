package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #5034: the adversarial-review untracked filter compared path forms that
// never match - RunStats.FilesEdited scopes are verbatim tool-arg paths
// (typically absolute), while `git status --porcelain` emits repo-relative
// paths. Agent-created files silently escaped the review gate. Both sides
// are now normalized to repo-root-relative form, and directory entries
// ("?? dir/") match file-level scopes inside them (reverse containment).

func TestIssue5034AbsoluteScopeMatchesRelativePorcelain(t *testing.T) {
	wd := t.TempDir()
	absFile := filepath.Join(wd, "brandnew.go")
	if got := untrackedLineInPaths(wd, "?? brandnew.go", []string{absFile}); !got {
		t.Errorf("absolute scope %q must match repo-relative untracked line \"?? brandnew.go\" (#5034)", absFile)
	}
	// Nested: absolute file scope under an untracked directory entry.
	if got := untrackedLineInPaths(wd, "?? pkg/", []string{filepath.Join(wd, "pkg", "new.go")}); !got {
		t.Errorf("directory entry \"?? pkg/\" must match file scope inside it (reverse containment, #5034)")
	}
	// Absolute scope outside the repo must not match unrelated relative paths.
	if got := untrackedLineInPaths(wd, "?? other.go", []string{filepath.Join(wd, "brandnew.go")}); got {
		t.Errorf("unrelated untracked path must still be filtered out")
	}
}

func TestIssue5034CollectDiffSeesAgentCreatedFileWithAbsoluteScope(t *testing.T) {
	dir := t.TempDir()
	gitCmd5018(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644)
	gitCmd5018(t, dir, "add", ".")
	gitCmd5018(t, dir, "commit", "-q", "-m", "base")

	// Agent creates a brand-new file; write_file records the ABSOLUTE path.
	newFile := filepath.Join(dir, "agentnew.go")
	if err := os.WriteFile(newFile, []byte("package p\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	a := &Agent{}
	a.SetWorkingDir(dir)
	diff := a.collectAdversarialDiff([]string{newFile})
	if !strings.Contains(diff, "agentnew.go") {
		t.Fatalf("absolute-scope diff must list this run's agent-created agentnew.go (#5034):\n%s", diff)
	}
}
