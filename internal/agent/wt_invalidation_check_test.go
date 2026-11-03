package agent

import (
	"strings"
	"testing"
)

func TestWTInvalidation_BasicDetection(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("internal/agent/agent.go")
	w.recordRead("internal/agent/expired_read_check.go")
	w.recordRead("internal/agent/tool_target_mismatch.go")

	msg := w.checkMutation("git_checkout", `{"branch":"feature"}`)
	if msg == "" {
		t.Fatal("expected warning message for git_checkout after 3 reads")
	}
	if !strings.Contains(msg, "Working-Tree Invalidation") {
		t.Errorf("expected message to contain warning header, got: %s", msg)
	}
	if !strings.Contains(msg, "git_checkout") {
		t.Errorf("expected message to mention tool name, got: %s", msg)
	}
	if !strings.Contains(msg, "3 file(s)") {
		t.Errorf("expected message to mention 3 files, got: %s", msg)
	}
}

func TestWTInvalidation_TooFewReads(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file.go")

	msg := w.checkMutation("git_checkout", `{}`)
	if msg != "" {
		t.Errorf("expected no warning with only 1 read, got: %s", msg)
	}
}

func TestWTInvalidation_NoReads(t *testing.T) {
	w := newWTInvalidationState()

	msg := w.checkMutation("git_checkout", `{}`)
	if msg != "" {
		t.Errorf("expected no warning with 0 reads, got: %s", msg)
	}
}

func TestWTInvalidation_NonMutatingTool(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")
	w.recordRead("file3.go")

	if isWTMutatingTool("read_file") {
		t.Error("read_file should not be a WT-mutating tool")
	}
	if isWTMutatingTool("edit_file") {
		t.Error("edit_file should not be a WT-mutating tool")
	}
	if isWTMutatingTool("run_command") {
		t.Error("run_command should not be classified as WT-mutating (not a git op)")
	}
}

func TestWTInvalidation_MutatingTools(t *testing.T) {
	tools := []string{
		"git_checkout", "git_reset", "git_stash",
		"git_pull", "git_revert", "git_merge",
		"git_rebase", "git_cherry_pick",
	}
	for _, tool := range tools {
		if !isWTMutatingTool(tool) {
			t.Errorf("%s should be classified as WT-mutating", tool)
		}
	}
}

func TestWTInvalidation_MaxWarnings(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")
	msg1 := w.checkMutation("git_checkout", `{}`)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	w.recordRead("file3.go")
	w.recordRead("file4.go")
	msg2 := w.checkMutation("git_reset", `{}`)
	if msg2 == "" {
		t.Fatal("expected second warning")
	}

	w.recordRead("file5.go")
	msg3 := w.checkMutation("git_stash", `{}`)
	if msg3 != "" {
		t.Errorf("expected no third warning (max reached), got: %s", msg3)
	}
}

func TestWTInvalidation_NoRepeatWithoutNewReads(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")

	msg1 := w.checkMutation("git_checkout", `{}`)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	msg2 := w.checkMutation("git_reset", `{}`)
	if msg2 != "" {
		t.Errorf("expected no repeat warning without new reads, got: %s", msg2)
	}
}

func TestWTInvalidation_Reset(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")
	_ = w.checkMutation("git_checkout", `{}`)

	w.reset()

	if len(w.readFiles) != 0 {
		t.Errorf("expected readFiles cleared after reset, got %d", len(w.readFiles))
	}
	if w.warnedCount != 0 {
		t.Errorf("expected warnedCount 0 after reset, got %d", w.warnedCount)
	}
}

func TestWTInvalidation_NormalizePath(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"./internal/agent/foo.go", "internal/agent/foo.go"},
		{"internal/agent/foo.go", "internal/agent/foo.go"},
		{"", ""},
		{"  file.go  ", "file.go"},
	}
	for _, tc := range tests {
		got := normalizeWTPath(tc.input)
		if got != tc.output {
			t.Errorf("normalizeWTPath(%q) = %q, want %q", tc.input, got, tc.output)
		}
	}
}

func TestWTInvalidation_DeduplicateReads(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file.go")
	w.recordRead("file.go")
	w.recordRead("file.go")

	if len(w.readFiles) != 1 {
		t.Errorf("expected 1 unique read, got %d", len(w.readFiles))
	}
}

func TestWTInvalidation_PathListTruncation(t *testing.T) {
	w := newWTInvalidationState()

	for i := 0; i < 5; i++ {
		w.recordRead("file" + string(rune('0'+i)) + ".go")
	}

	msg := w.checkMutation("git_checkout", `{}`)
	if msg == "" {
		t.Fatal("expected warning message")
	}
	if !strings.Contains(msg, "and 2 more") {
		t.Errorf("expected truncation suffix 'and 2 more', got: %s", msg)
	}
}

func TestWTInvalidation_ErrorResult(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("file1.go")
	w.recordRead("file2.go")

	msg := w.checkMutation("git_checkout", `{}`)
	if msg == "" {
		t.Fatal("expected warning even for error result (filtering is in agent.go)")
	}
}

func TestWTInvalidation_RereadCountsAsNewRead(t *testing.T) {
	w := newWTInvalidationState()

	w.recordRead("a.go")
	w.recordRead("b.go")
	if msg := w.checkMutation("git_reset", `{}`); msg == "" {
		t.Fatal("expected first warning after initial reads")
	}

	w.recordRead("a.go")
	w.recordRead("b.go")
	if msg := w.checkMutation("git_stash", `{"message":"x"}`); msg == "" {
		t.Fatal("expected second warning after compliant re-reads (re-reads are read events)")
	}

	w.recordRead("c.go")
	if msg := w.checkMutation("git_checkout", `{}`); msg != "" {
		t.Errorf("expected suppression after max warnings, got: %s", msg)
	}
}

func TestWTInvalidation_TreePreservingCommandsAreExempt(t *testing.T) {
	readOnly := []string{
		"git fetch origin main",
		"git push origin HEAD",
		"git remote -v",
		"git tag v1.3.199",
		"git rev-parse HEAD",
		"git blame internal/agent/agent.go",
		"git config user.name",
		"git ls-files | head",
		"git worktree list",
		"git reflog -5",
		"git stash list",
	}
	for _, cmd := range readOnly {
		if runCommandMutatesTree(`{"command":"` + cmd + `"}`) {
			t.Errorf("%q must not be classified as tree-mutating", cmd)
		}
	}
}

func TestWTInvalidation_CompoundCommandsScanEveryGit(t *testing.T) {
	mutating := []string{
		"git fetch && git reset --hard",
		"git status && git checkout main",
		"git log -1 && git stash pop",
		"git rev-parse HEAD; git clean -fd",
	}
	for _, cmd := range mutating {
		if !runCommandMutatesTree(`{"command":"` + cmd + `"}`) {
			t.Errorf("%q contains a tree-mutating git command and must be classified as mutating", cmd)
		}
	}

	readOnlyLines := []string{
		"git status && git fetch origin main",
		"git rev-parse HEAD; git diff --stat",
	}
	for _, cmd := range readOnlyLines {
		if runCommandMutatesTree(`{"command":"` + cmd + `"}`) {
			t.Errorf("%q is fully tree-preserving and must not be classified as mutating", cmd)
		}
	}
}

func TestWTInvalidation_ReadOnlyDoesNotConsumeWarningSlot(t *testing.T) {
	w := newWTInvalidationState()
	w.recordRead("a.go")
	w.recordRead("b.go")

	if msg := w.checkMutation("run_command", `{"command":"git fetch origin main"}`); msg != "" {
		t.Fatalf("git fetch must not fire the invalidation warning, got: %s", msg)
	}

	if msg := w.checkMutation("run_command", `{"command":"git checkout main"}`); msg == "" {
		t.Fatal("real mutation after read-only fetch must still warn")
	}
}
