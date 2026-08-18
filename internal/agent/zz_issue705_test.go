package agent

// Regression tests for issue #705: the #698 commit-hint fix was inert when the
// LLM passed RELATIVE tool paths — FilesEdited records the literal
// pre-execution argument (extractPathsFromToolCall runs before the tool layer
// resolves it against WorkingDir), while the porcelain side is absolutized, so
// intersectFileSets compared "src/foo.go" against "<abs>/src/foo.go" and the
// intersection was always empty. Both sides must be absolutized against
// workingDir before intersecting.

import (
	"strings"
	"testing"
)

// #705 core scenario: relative-path edit_file argument, file actually dirty.
func TestIssue705_RelativeEditedPathIntersectsDirtyTree(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	// NOTE: files must be TRACKED — plain `git status --porcelain` collapses
	// wholly-untracked directories to "?? src/" (directory, not file), which
	// is orthogonal to #705. The real-world scenario is an edit to an existing
	// tracked file, so commit a base first, then the agent's edit dirties it.
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

// Mixed absolute/relative edit list — both forms must match.
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

// Negative stays negative: a relative path that is NOT dirty must still not
// fire (absolutizing must not create false positives from user dirt).
func TestIssue705_RelativePathNotDirtyNoHint(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	// User dirt only; the agent's relative-path edit was committed via shell.
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

// Sub-directory relative paths ("pkg/internal/x.go") resolve correctly too.
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
