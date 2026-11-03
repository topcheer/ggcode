package agent

import (
	"strings"
	"testing"
)

func TestIssue705_RelativeEditedPathIntersectsDirtyTree(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	writeFileT(t, dir+"/src/foo.go", "base")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "base")
	writeFileT(t, dir+"/src/foo.go", "agent edit")

	stats := &RunStats{
		FilesEdited: []string{"src/foo.go"},
		ToolCalls:   map[string]int{"edit_file": 1},
	}
	msg := a.checkCommitHintGate(stats)
	if msg == "" {
		t.Fatal("relative-path edit must still intersect the dirty tree (#705: hint was inert for the most common LLM input form)")
	}
	if !strings.Contains(msg, "1 file") {
		t.Fatalf("hint must count the 1 relative-path edited file: %q", msg)
	}
	if !strings.Contains(msg, "src/foo.go") {
		t.Fatalf("hint must name the edited file: %q", msg)
	}
}

func TestIssue705_MixedAbsoluteAndRelativeEdits(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	writeFileT(t, dir+"/rel.go", "x")
	writeFileT(t, dir+"/abs.go", "y")

	abs := dir + "/abs.go"
	stats := &RunStats{
		FilesEdited: []string{"rel.go", abs},
		ToolCalls:   map[string]int{"edit_file": 2},
	}
	msg := a.checkCommitHintGate(stats)
	if msg == "" {
		t.Fatal("hint must fire for a mixed absolute/relative edit list")
	}
	if !strings.Contains(msg, "2 files") {
		t.Fatalf("hint must count both files regardless of path form: %q", msg)
	}
}

func TestIssue705_RelativePathNotDirtyNoHint(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	writeFileT(t, dir+"/user_untracked.go", "user")
	writeFileT(t, dir+"/agent.go", "committed")
	runGit(t, dir, "add", "agent.go")
	runGit(t, dir, "commit", "-qm", "base")

	stats := &RunStats{
		FilesEdited: []string{"agent.go"},
		ToolCalls:   map[string]int{"edit_file": 1},
	}
	if msg := a.checkCommitHintGate(stats); msg != "" {
		t.Fatalf("no hint when the relative-path edit is committed: %q", msg)
	}
}

func TestIssue705_NestedRelativePath(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	writeFileT(t, dir+"/internal/agent/nested.go", "base")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-qm", "base")
	writeFileT(t, dir+"/internal/agent/nested.go", "agent edit")

	stats := &RunStats{
		FilesEdited: []string{"internal/agent/nested.go"},
		ToolCalls:   map[string]int{"edit_file": 1},
	}
	msg := a.checkCommitHintGate(stats)
	if msg == "" {
		t.Fatal("nested relative-path edit must intersect the dirty tree")
	}
}
