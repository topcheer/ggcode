package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Helper ---

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		// Skip hooks to avoid triggering external tools in tests
		cmd.Env = append(cmd.Environ(), "GIT_TEMPLATE_DIR=", "GIT_HOOK_DISABLED=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %s: %v", name, args, string(out), err)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	// Need at least one commit for worktree add to work
	cmd := exec.Command("sh", "-c", "echo hello > README.md")
	cmd.Dir = dir
	cmd.Run()
	run("git", "add", "README.md")
	run("git", "commit", "--no-verify", "-m", "initial")

	// Remove hooks to prevent interference
	exec.Command("rm", "-rf", dir+"/.git/hooks").Run()
	return dir
}

// --- isWorktreeNameChar ---

func TestIsWorktreeNameChar(t *testing.T) {
	valid := "abcXYZ012_-test.name"
	for _, c := range valid {
		if !isWorktreeNameChar(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}

	invalid := " /\\!@#$%^&*(){}[]|;:'\",<>?\n\t"
	for _, c := range invalid {
		if isWorktreeNameChar(c) {
			t.Errorf("expected %q to be invalid", c)
		}
	}
}

// --- EnterWorktree ---

func TestEnterWorktree_Execute(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"test-branch"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "test-branch") {
		t.Errorf("expected branch name in result: %s", result.Content)
	}
	if !strings.Contains(result.Content, ".ggcode/worktrees") {
		t.Errorf("expected .ggcode/worktrees path: %s", result.Content)
	}
	if !strings.Contains(result.Content, "SuggestedWorkingDir is set to this path") {
		t.Errorf("expected explicit SuggestedWorkingDir guidance: %s", result.Content)
	}
	if strings.Contains(result.Content, "All subsequent tool calls") {
		t.Errorf("result should not imply all subsequent tool calls automatically switch directories: %s", result.Content)
	}

	// Verify SuggestedWorkingDir is set to the worktree path
	if result.SuggestedWorkingDir == "" {
		t.Error("SuggestedWorkingDir should be set")
	}
	if !strings.Contains(result.SuggestedWorkingDir, "test-branch") {
		t.Errorf("SuggestedWorkingDir = %q, should contain test-branch", result.SuggestedWorkingDir)
	}

	// Verify the worktree directory actually exists
	gitRoot := dir
	worktreePath := gitRoot + "/.ggcode/worktrees/test-branch"
	cmd := exec.Command("git", "worktree", "list")
	cmd.Dir = gitRoot
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), worktreePath) {
		t.Errorf("worktree not found in git worktree list: %s", string(out))
	}

	// Clean up
	cmd = exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = gitRoot
	cmd.Run()
}

func TestEnterWorktree_InvalidName(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	tests := []string{
		`{"name":"has spaces"}`,
		`{"name":"path/traversal"}`,
		`{"name":"../../../etc"}`,
		`{"name":"semi;colon"}`,
	}
	for _, input := range tests {
		result, err := tool.Execute(context.Background(), json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Errorf("expected error for input %s", input)
		}
	}
}

func TestEnterWorktree_InvalidJSON(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid JSON")
	}
}

func TestEnterWorktree_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	tool := EnterWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for non-git directory")
	}
}

func TestEnterWorktree_DefaultName(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "wt-") {
		t.Errorf("expected auto-generated name with wt- prefix: %s", result.Content)
	}

	// Clean up — extract path from result
	pathStart := strings.Index(result.Content, dir)
	if pathStart >= 0 {
		wtPath := result.Content[pathStart:]
		wtPath = strings.TrimSuffix(wtPath, "'")
		wtPath = strings.TrimSuffix(wtPath, ")")
		wtPath = strings.Fields(wtPath)[0]
		cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
		cmd.Dir = dir
		cmd.Run()
	}
}

func TestEnterWorktree_DuplicateName(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := EnterWorktree{WorkingDir: dir}

	// First creation should succeed
	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"name":"dup"}`))
	if result.IsError {
		t.Fatalf("first creation failed: %s", result.Content)
	}

	// Second creation with same name should fail
	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"name":"dup"}`))
	if !result.IsError {
		t.Error("expected error for duplicate worktree name")
	}

	// Clean up
	cmd := exec.Command("git", "worktree", "remove", "--force", dir+"/.ggcode/worktrees/dup")
	cmd.Dir = dir
	cmd.Run()
}

// --- ExitWorktree ---

func TestExitWorktree_InvalidJSON(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := ExitWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`bad json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid JSON")
	}
}

func TestExitWorktree_NotInWorktree(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := ExitWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"remove"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when not in a worktree")
	}
}

func TestExitWorktree_InvalidAction(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := ExitWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"explode"}`))
	if err != nil {
		t.Fatal(err)
	}
	// Should either error (not in worktree) or error (invalid action)
	if !result.IsError {
		t.Error("expected error for invalid action")
	}
}

func TestExitWorktree_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	tool := ExitWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"remove"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for non-git directory")
	}
}

// --- findGitRoot ---

func TestFindGitRoot(t *testing.T) {
	dir := initTestGitRepo(t)
	realDir, _ := filepath.EvalSymlinks(dir)

	root, err := findGitRoot(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if root != realDir {
		t.Errorf("findGitRoot = %q, want %q", root, realDir)
	}
}

func TestFindGitRoot_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := findGitRoot(context.Background(), dir)
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

// --- findGitRootFromWorktree ---

func TestFindGitRootFromWorktree(t *testing.T) {
	dir := initTestGitRepo(t)
	realDir, _ := filepath.EvalSymlinks(dir)

	// Create a worktree
	enterTool := EnterWorktree{WorkingDir: dir}
	result, _ := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"test-wt"}`))
	if result.IsError {
		t.Fatalf("create worktree failed: %s", result.Content)
	}

	wtPath := dir + "/.ggcode/worktrees/test-wt"
	gitRoot, err := findGitRootFromWorktree(wtPath)
	if err != nil {
		t.Fatalf("findGitRootFromWorktree error: %v", err)
	}
	// findGitRootFromWorktree returns the git dir (could be .git or the repo root)
	// Just verify it contains the main repo path
	if !strings.Contains(gitRoot, "TestFindGitRootFromWorktree") {
		t.Errorf("gitRoot = %q, expected to contain test dir", gitRoot)
	}
	_ = realDir

	// Clean up
	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

// --- isInsideWorktree ---

func TestIsInsideWorktree_RegularDir(t *testing.T) {
	dir := initTestGitRepo(t)
	isWT, path, err := isInsideWorktree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if isWT {
		t.Error("regular git repo should not be detected as worktree")
	}
	_ = path
}

func TestIsInsideWorktree_InsideWorktree(t *testing.T) {
	dir := initTestGitRepo(t)

	enterTool := EnterWorktree{WorkingDir: dir}
	result, _ := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"wt-inside"}`))
	if result.IsError {
		t.Fatalf("create worktree failed: %s", result.Content)
	}

	wtPath := dir + "/.ggcode/worktrees/wt-inside"
	isWT, detectedPath, err := isInsideWorktree(wtPath)
	if err != nil {
		t.Fatalf("isInsideWorktree error: %v", err)
	}
	if !isWT {
		t.Error("should detect worktree directory")
	}
	if detectedPath != wtPath {
		t.Errorf("path = %q, want %q", detectedPath, wtPath)
	}

	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

// --- Round-trip: create then remove ---

func TestWorktreeRoundTrip(t *testing.T) {
	dir := initTestGitRepo(t)

	enterTool := EnterWorktree{WorkingDir: dir}

	// Create
	result, err := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"roundtrip-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("create failed: %s", result.Content)
	}

	wtPath := dir + "/.ggcode/worktrees/roundtrip-test"

	// Verify worktree exists
	if _, err := exec.Command("git", "worktree", "list").CombinedOutput(); err != nil {
		t.Fatal(err)
	}

	// Exit from inside the worktree
	exitTool := ExitWorktree{WorkingDir: wtPath}
	result, err = exitTool.Execute(context.Background(), json.RawMessage(`{"action":"remove","discard_changes":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exit failed: %s", result.Content)
	}
	// Verify SuggestedWorkingDir points back to main repo
	if result.SuggestedWorkingDir == "" {
		t.Error("SuggestedWorkingDir should be set after remove")
	}

	// Verify worktree is gone
	cmd := exec.Command("git", "worktree", "list")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "roundtrip-test") {
		t.Errorf("worktree should be removed: %s", string(out))
	}
}

func TestWorktreeRoundTrip_KeepAction(t *testing.T) {
	dir := initTestGitRepo(t)

	enterTool := EnterWorktree{WorkingDir: dir}

	// Create
	result, _ := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"keep-test"}`))
	if result.IsError {
		t.Fatalf("create failed: %s", result.Content)
	}

	wtPath := dir + "/.ggcode/worktrees/keep-test"

	// Exit with keep — should NOT remove
	exitTool := ExitWorktree{WorkingDir: wtPath}
	result, err := exitTool.Execute(context.Background(), json.RawMessage(`{"action":"keep"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exit keep failed: %s", result.Content)
	}
	// Verify SuggestedWorkingDir points back to main repo
	if result.SuggestedWorkingDir == "" {
		t.Error("SuggestedWorkingDir should be set to main repo root")
	}

	// Verify worktree still exists
	cmd := exec.Command("git", "worktree", "list")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "keep-test") {
		t.Errorf("worktree should still exist after keep: %s", string(out))
	}

	// Clean up
	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

func TestFindGitRootFromWorktree_NotWorktree(t *testing.T) {
	dir := t.TempDir()
	_, err := findGitRootFromWorktree(dir)
	if err == nil {
		t.Error("expected error for non-worktree directory")
	}
}

// --- ListWorktree ---

func TestListWorktree_SingleRepo(t *testing.T) {
	dir := initTestGitRepo(t)
	tool := ListWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Main repo should appear as a worktree
	if !strings.Contains(result.Content, "worktree") {
		t.Errorf("expected worktree listing, got: %s", result.Content)
	}
}

func TestListWorktree_MultipleWorktrees(t *testing.T) {
	dir := initTestGitRepo(t)

	// Create two worktrees
	enterTool := EnterWorktree{WorkingDir: dir}
	r1, err := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"wt-alpha"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r1.IsError {
		t.Fatalf("create wt-alpha failed: %s", r1.Content)
	}

	r2, err := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"wt-beta"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r2.IsError {
		t.Fatalf("create wt-beta failed: %s", r2.Content)
	}

	// List should show all three (main + 2 worktrees)
	listTool := ListWorktree{WorkingDir: dir}
	result, err := listTool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "wt-alpha") {
		t.Errorf("expected wt-alpha in listing: %s", result.Content)
	}
	if !strings.Contains(result.Content, "wt-beta") {
		t.Errorf("expected wt-beta in listing: %s", result.Content)
	}
	if !strings.Contains(result.Content, "3 worktree") {
		t.Errorf("expected 3 worktrees, got: %s", result.Content)
	}

	// Clean up
	exec.Command("git", "worktree", "remove", "--force", r1.SuggestedWorkingDir).Run()
	exec.Command("git", "worktree", "remove", "--force", r2.SuggestedWorkingDir).Run()
}

func TestListWorktree_DirtyDetection(t *testing.T) {
	dir := initTestGitRepo(t)

	// Create a worktree
	enterTool := EnterWorktree{WorkingDir: dir}
	r1, err := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"wt-dirty"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r1.IsError {
		t.Fatalf("create wt-dirty failed: %s", r1.Content)
	}
	wtPath := r1.SuggestedWorkingDir

	// Make it dirty
	cmd := exec.Command("sh", "-c", "echo 'uncommitted' > newfile.txt")
	cmd.Dir = wtPath
	cmd.Run()

	// List should show dirty marker
	listTool := ListWorktree{WorkingDir: dir}
	result, err := listTool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "uncommitted") {
		t.Errorf("expected dirty marker in listing: %s", result.Content)
	}

	// Clean up
	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

func TestListWorktree_NonGitRepo(t *testing.T) {
	dir := t.TempDir()
	tool := ListWorktree{WorkingDir: dir}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for non-git directory")
	}
}

// TestExitWorktree_DirtyWithoutForceRefused pins #1356: removing a dirty
// worktree WITHOUT discard_changes must be refused by git itself (no
// unconditional --force), while discard_changes=true still removes it.
// Uses a real git repo so the force-gating actually round-trips to git.
func TestExitWorktree_DirtyWithoutForceRefused(t *testing.T) {
	dir := initTestGitRepo(t)
	enter := EnterWorktree{WorkingDir: dir}
	res, err := enter.Execute(context.Background(), json.RawMessage(`{"name":"dirty-wt"}`))
	if err != nil || res.IsError {
		t.Fatalf("enter failed: %v %s", err, res.Content)
	}
	wtPath := dir + "/.ggcode/worktrees/dirty-wt"
	defer func() {
		cleanup := exec.Command("git", "worktree", "remove", "--force", wtPath)
		cleanup.Dir = dir
		cleanup.Run()
	}()

	// Dirty the worktree AFTER entering, bypassing the tool's own porcelain
	// pre-check as closely as a parallel writer would.
	if err := os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("concurrent write"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The porcelain check should refuse first - same observable contract.
	exit := ExitWorktree{WorkingDir: wtPath}
	res, err = exit.Execute(context.Background(), json.RawMessage(`{"action":"remove"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected refusal for dirty worktree without discard_changes, got: %s", res.Content)
	}

	// discard_changes=true must still remove it (--force restored).
	res, err = exit.Execute(context.Background(), json.RawMessage(`{"action":"remove","discard_changes":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected removal with discard_changes=true, got: %s", res.Content)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree directory still exists after forced removal")
	}
}

// TestExitWorktreeRejectsInvalidAction pins #1406-A: every non-"keep"
// value - typos like "exit"/"close" (beyond distance-2 enum correction)
// AND a missing action (json.Unmarshal does not enforce required) - used
// to fall through to the destructive remove branch (worktree deleted +
// git branch -D). Fail closed now.
func TestExitWorktreeRejectsInvalidAction(t *testing.T) {
	tw := ExitWorktree{WorkingDir: t.TempDir()}
	for _, action := range []string{"", "exit", "close", "delete", "KEEP"} {
		res, err := tw.Execute(context.Background(), json.RawMessage(`{"action":"`+action+`"}`))
		if err != nil {
			t.Fatalf("action %q: unexpected err: %v", action, err)
		}
		if !res.IsError {
			t.Errorf("action %q must fail closed, got success: %s", action, res.Content)
		}
		if !strings.Contains(res.Content, "keep") || !strings.Contains(res.Content, "remove") {
			t.Errorf("action %q: error should name the valid values: %s", action, res.Content)
		}
	}
}
