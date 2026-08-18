package agent

// Regression tests for issue #698: the commit hint gate attributed the whole
// working tree's dirty-file count to the agent ("You made changes to N files")
// instead of RunStats.FilesEdited — pre-existing user changes and other
// agents' untracked files were counted, sending the agent hunting for edits
// it never made. The gate must scope the hint to the agent's own edit list
// intersected with the dirty tree, and disclose unrelated dirt separately.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newCommitHintAgent builds a minimal Agent whose commitHint state is armed
// and whose working dir is a fresh git repo (real porcelain, no mocking of
// git itself — same approach as commit_hint_gate_test.go).
func newCommitHintAgent(t *testing.T) *Agent {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")

	a := &Agent{workingDir: dir}
	a.commitHint = &commitHintState{}
	return a
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIssue698_HintScopesToAgentEditsOnly(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	// Pre-existing USER change (tracked file modified before the agent ran)
	// plus a pre-existing UNTRACKED user file.
	writeFileT(t, filepath.Join(dir, "user_tracked.go"), "user change")
	writeFileT(t, filepath.Join(dir, "user_untracked.go"), "user file")
	runGit(t, dir, "add", "user_tracked.go")

	// The agent edited exactly one file.
	agentFile := filepath.Join(dir, "agent_edit.go")
	writeFileT(t, agentFile, "agent edit")

	stats := &RunStats{
		FilesEdited: []string{agentFile},
		ToolCalls:   map[string]int{"edit_file": 1},
	}

	msg := a.checkCommitHintGate(stats)
	if msg == "" {
		t.Fatal("hint must fire when the agent's own edit is uncommitted")
	}
	// The count must be the agent's dirty edits (1), never the tree's total (3).
	if !strings.Contains(msg, "1 file") {
		t.Fatalf("hint must scope to the agent's 1 edited file, got: %q", msg)
	}
	if strings.Contains(msg, "3 files") || strings.Contains(msg, "2 files") {
		t.Fatalf("hint must not count user's pre-existing changes: %q", msg)
	}
	// Unrelated dirt is disclosed, not attributed.
	if !strings.Contains(msg, "unrelated") {
		t.Fatalf("hint must disclose unrelated pre-existing changes separately: %q", msg)
	}
	if !strings.Contains(msg, agentFile) {
		t.Fatalf("hint must name the agent's file: %q", msg)
	}
}

func TestIssue698_NoHintWhenAgentEditsAllCommitted(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	// Agent edited and committed its file via shell git (no git_commit TOOL,
	// so the tool-exemption path must not kick in).
	agentFile := filepath.Join(dir, "agent_edit.go")
	writeFileT(t, agentFile, "agent edit")
	runGit(t, dir, "add", "agent_edit.go")
	runGit(t, dir, "commit", "-qm", "agent work")

	// Tree is still dirty from the user's unrelated change.
	writeFileT(t, filepath.Join(dir, "user_tracked_base.go"), "base")
	runGit(t, dir, "add", "user_tracked_base.go")
	runGit(t, dir, "commit", "-qm", "base")
	writeFileT(t, filepath.Join(dir, "user_tracked_base.go"), "user change")

	stats := &RunStats{
		FilesEdited: []string{agentFile},
		ToolCalls:   map[string]int{"edit_file": 1},
	}
	if msg := a.checkCommitHintGate(stats); msg != "" {
		t.Fatalf("no hint when the agent's edits are committed, even if the tree is dirty: %q", msg)
	}
}

func TestIssue698_NoHintWithoutFilesEdited(t *testing.T) {
	a := newCommitHintAgent(t)
	// Dirty tree exists (nothing committed), but the agent edited nothing —
	// porcelain non-empty must NOT trigger attribution.
	writeFileT(t, filepath.Join(a.workingDir, "user_untracked.go"), "x")

	stats := &RunStats{
		FilesEdited: nil,
		ToolCalls:   map[string]int{"read_file": 5},
	}
	if msg := a.checkCommitHintGate(stats); msg != "" {
		t.Fatalf("no hint when FilesEdited is empty: %q", msg)
	}
}

func TestIssue698_GitStashDoesNotSuppressHint(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	agentFile := filepath.Join(dir, "agent_edit.go")
	writeFileT(t, agentFile, "agent edit")

	// The agent stashed (and popped) — the edits are back in the tree,
	// uncommitted. A stash round trip is NOT "handled version control".
	stats := &RunStats{
		FilesEdited: []string{agentFile},
		ToolCalls: map[string]int{
			"edit_file": 1,
			"git_stash": 2, // push + pop
		},
	}
	msg := a.checkCommitHintGate(stats)
	if msg == "" {
		t.Fatal("git_stash round trip must NOT suppress the commit hint — the changes are still uncommitted")
	}
}

func TestIssue698_GitAddStillSuppressesHint(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir
	agentFile := filepath.Join(dir, "agent_edit.go")
	writeFileT(t, agentFile, "agent edit")

	stats := &RunStats{
		FilesEdited: []string{agentFile},
		ToolCalls: map[string]int{
			"edit_file": 1,
			"git_add":   1,
		},
	}
	if msg := a.checkCommitHintGate(stats); msg != "" {
		t.Fatalf("git_add must still suppress the hint: %q", msg)
	}
}

func TestIssue698_OnlyAgentFilesNamedInList(t *testing.T) {
	a := newCommitHintAgent(t)
	dir := a.workingDir

	// Two agent files, one user file.
	f1 := filepath.Join(dir, "a1.go")
	f2 := filepath.Join(dir, "a2.go")
	writeFileT(t, f1, "x")
	writeFileT(t, f2, "y")
	writeFileT(t, filepath.Join(dir, "zzz_user_untracked.go"), "user")

	stats := &RunStats{
		FilesEdited: []string{f1, f2},
		ToolCalls:   map[string]int{"edit_file": 2},
	}
	msg := a.checkCommitHintGate(stats)
	if msg == "" {
		t.Fatal("hint must fire")
	}
	if !strings.Contains(msg, "2 files") {
		t.Fatalf("hint must count exactly the agent's 2 dirty files: %q", msg)
	}
	if strings.Contains(msg, "zzz_user_untracked.go") {
		t.Fatalf("hint's file list must not include the user's untracked file: %q", msg)
	}
}

// --- pure helpers (no git needed) -------------------------------------------

func TestIssue698_PorcelainFilePathsRenames(t *testing.T) {
	porcelain := " M modified.go\n" +
		"?? untracked.go\n" +
		"R  old_name.go -> new_name.go\n" +
		"A  added.go\n"
	got := porcelainFilePaths(porcelain)
	want := []string{"modified.go", "untracked.go", "new_name.go", "added.go"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("porcelainFilePaths = %v, want %v", got, want)
	}
}

func TestIssue698_IntersectFileSets(t *testing.T) {
	edited := []string{"a.go", "b.go", "c.go"}
	dirty := []string{"b.go", "c.go", "d.go"}
	got := intersectFileSets(edited, dirty)
	if len(got) != 2 || got[0] != "b.go" || got[1] != "c.go" {
		t.Fatalf("intersectFileSets = %v, want [b.go c.go]", got)
	}
}

func TestIssue698_ShortenFileListCaps(t *testing.T) {
	files := make([]string, 15)
	for i := range files {
		files[i] = string(rune('a'+i)) + ".go"
	}
	got := shortenFileList(files)
	if len(got) != 11 { // 10 listed + "... and 5 more"
		t.Fatalf("shortenFileList len = %d, want 11", len(got))
	}
	if !strings.Contains(got[len(got)-1], "5 more") {
		t.Fatalf("cap message missing: %v", got)
	}
}
