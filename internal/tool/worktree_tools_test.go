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

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir

		cmd.Env = append(cmd.Environ(), "GIT_TEMPLATE_DIR=", "GIT_HOOK_DISABLED=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %s: %v", name, args, string(out), err)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")

	cmd := exec.Command("sh", "-c", "echo hello > README.md")
	cmd.Dir = dir
	cmd.Run()
	run("git", "add", "README.md")
	run("git", "commit", "--no-verify", "-m", "initial")

	exec.Command("rm", "-rf", dir+"/.git/hooks").Run()
	return dir
}

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

	if result.SuggestedWorkingDir == "" {
		t.Error("SuggestedWorkingDir should be set")
	}
	if !strings.Contains(result.SuggestedWorkingDir, "test-branch") {
		t.Errorf("SuggestedWorkingDir = %q, should contain test-branch", result.SuggestedWorkingDir)
	}

	gitRoot := dir
	// git canonicalizes symlinked paths: `git worktree list` reports the
	// on-disk resolved form, while t.TempDir() under a symlinked TMPDIR
	// (stock darwin /var/folders) returns the raw symlink form - a
	// deterministic red on darwin, invisible on linux where /tmp is real
	// (#5616). Compare against the resolved root.
	rootResolved, rerr := filepath.EvalSymlinks(gitRoot)
	if rerr != nil {
		t.Fatal(rerr)
	}
	worktreePath := rootResolved + "/.ggcode/worktrees/test-branch"
	cmd := exec.Command("git", "worktree", "list")
	cmd.Dir = gitRoot
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), worktreePath) {
		t.Errorf("worktree not found in git worktree list: %s", string(out))
	}

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

	result, _ := tool.Execute(context.Background(), json.RawMessage(`{"name":"dup"}`))
	if result.IsError {
		t.Fatalf("first creation failed: %s", result.Content)
	}

	result, _ = tool.Execute(context.Background(), json.RawMessage(`{"name":"dup"}`))
	if !result.IsError {
		t.Error("expected error for duplicate worktree name")
	}

	cmd := exec.Command("git", "worktree", "remove", "--force", dir+"/.ggcode/worktrees/dup")
	cmd.Dir = dir
	cmd.Run()
}

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

func TestFindGitRootFromWorktree(t *testing.T) {
	dir := initTestGitRepo(t)
	realDir, _ := filepath.EvalSymlinks(dir)

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

	if !strings.Contains(gitRoot, "TestFindGitRootFromWorktree") {
		t.Errorf("gitRoot = %q, expected to contain test dir", gitRoot)
	}
	_ = realDir

	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

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

func TestWorktreeRoundTrip(t *testing.T) {
	dir := initTestGitRepo(t)

	enterTool := EnterWorktree{WorkingDir: dir}

	result, err := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"roundtrip-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("create failed: %s", result.Content)
	}

	wtPath := dir + "/.ggcode/worktrees/roundtrip-test"

	if _, err := exec.Command("git", "worktree", "list").CombinedOutput(); err != nil {
		t.Fatal(err)
	}

	exitTool := ExitWorktree{WorkingDir: wtPath}
	result, err = exitTool.Execute(context.Background(), json.RawMessage(`{"action":"remove","discard_changes":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exit failed: %s", result.Content)
	}

	if result.SuggestedWorkingDir == "" {
		t.Error("SuggestedWorkingDir should be set after remove")
	}

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

	result, _ := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"keep-test"}`))
	if result.IsError {
		t.Fatalf("create failed: %s", result.Content)
	}

	wtPath := dir + "/.ggcode/worktrees/keep-test"

	exitTool := ExitWorktree{WorkingDir: wtPath}
	result, err := exitTool.Execute(context.Background(), json.RawMessage(`{"action":"keep"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("exit keep failed: %s", result.Content)
	}

	if result.SuggestedWorkingDir == "" {
		t.Error("SuggestedWorkingDir should be set to main repo root")
	}

	cmd := exec.Command("git", "worktree", "list")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "keep-test") {
		t.Errorf("worktree should still exist after keep: %s", string(out))
	}

	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

func TestFindGitRootFromWorktree_NotWorktree(t *testing.T) {
	dir := t.TempDir()
	_, err := findGitRootFromWorktree(dir)
	if err == nil {
		t.Error("expected error for non-worktree directory")
	}
}

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

	if !strings.Contains(result.Content, "worktree") {
		t.Errorf("expected worktree listing, got: %s", result.Content)
	}
}

func TestListWorktree_MultipleWorktrees(t *testing.T) {
	dir := initTestGitRepo(t)

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

	exec.Command("git", "worktree", "remove", "--force", r1.SuggestedWorkingDir).Run()
	exec.Command("git", "worktree", "remove", "--force", r2.SuggestedWorkingDir).Run()
}

func TestListWorktree_DirtyDetection(t *testing.T) {
	dir := initTestGitRepo(t)

	enterTool := EnterWorktree{WorkingDir: dir}
	r1, err := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"wt-dirty"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r1.IsError {
		t.Fatalf("create wt-dirty failed: %s", r1.Content)
	}
	wtPath := r1.SuggestedWorkingDir

	cmd := exec.Command("sh", "-c", "echo 'uncommitted' > newfile.txt")
	cmd.Dir = wtPath
	cmd.Run()

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

	if err := os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("concurrent write"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit := ExitWorktree{WorkingDir: wtPath}
	res, err = exit.Execute(context.Background(), json.RawMessage(`{"action":"remove"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected refusal for dirty worktree without discard_changes, got: %s", res.Content)
	}

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

func Test1710WorktreeNameRejectsPathComponents(t *testing.T) {
	for _, bad := range []string{".", ".."} {
		if isSafeWorktreeName(bad) {
			t.Errorf("name %q must be rejected as a path component", bad)
		}
	}
	for _, ok := range []string{"fix-1710", "wt_dotted.name", "a"} {
		if !isSafeWorktreeName(ok) {
			t.Errorf("name %q should be accepted", ok)
		}
	}
}

// Pin for issue #5413: enter_worktree called from INSIDE a worktree must
// create the new worktree under the MAIN tree's .ggcode/worktrees/, not
// nested inside the current worktree (6-deep chains observed in real
// sessions, with project-memory index pollution).
func TestEnterWorktree_FromInsideWorktreeLandsInMainTree(t *testing.T) {
	mainDir := initTestGitRepo(t)
	first := EnterWorktree{WorkingDir: mainDir}
	res1, err := first.Execute(context.Background(), json.RawMessage(`{"name":"wt-a"}`))
	if err != nil || res1.IsError {
		t.Fatalf("first enter_worktree failed: %v %s", err, res1.Content)
	}
	wtA := res1.SuggestedWorkingDir
	if wtA == "" {
		t.Fatal("first enter_worktree returned empty SuggestedWorkingDir")
	}

	// Second call with WorkingDir INSIDE the first worktree: the new
	// worktree must land in <main>/.ggcode/worktrees/, not <wtA>/.ggcode/...
	second := EnterWorktree{WorkingDir: wtA}
	res2, err := second.Execute(context.Background(), json.RawMessage(`{"name":"wt-b"}`))
	if err != nil || res2.IsError {
		t.Fatalf("nested enter_worktree failed: %v %s", err, res2.Content)
	}
	wtB := res2.SuggestedWorkingDir
	// macOS: t.TempDir() yields /var/... while git rev-parse resolves
	// symlinks to /private/var/... - compare on eval'd paths (#5413 test).
	wantBase := filepath.Join(mainDir, ".ggcode", "worktrees")
	realWant, werr := filepath.EvalSymlinks(wantBase)
	if werr != nil {
		t.Fatalf("eval wantBase: %v", werr)
	}
	realGot, gerr := filepath.EvalSymlinks(wtB)
	if gerr != nil {
		t.Fatalf("eval wtB: %v", gerr)
	}
	if filepath.Dir(realGot) != realWant {
		t.Fatalf("wt-b must live under main-tree worktrees dir %s, got %s (issue #5413 nesting)", realWant, realGot)
	}
	if strings.Contains(wtB, "wt-a") {
		t.Fatalf("wt-b must not be nested inside wt-a (issue #5413), got %s", wtB)
	}
}

// TestCreateAgentWorktreeFromInsideWorktree pins #5420: spawn_agent
// isolation worktrees created from INSIDE an existing worktree must land
// as siblings in <main>/.ggcode/worktrees/, not nested inside the current
// worktree. Before the fix createAgentWorktree used bare findGitRoot,
// which stops at the worktree itself (.git is a gitdir FILE) and nests.
func TestCreateAgentWorktreeFromInsideWorktree(t *testing.T) {
	dir := initTestGitRepo(t)
	// macOS: git resolves /var/folders to /private/var/folders; normalize
	// both sides before comparing prefixes.
	if resolved, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		dir = resolved
	}
	tool := EnterWorktree{WorkingDir: dir}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"outer-wt"}`))
	if err != nil || result.IsError {
		t.Fatalf("enter outer worktree: err=%v result=%s", err, result.Content)
	}
	wtPath := filepath.Join(dir, ".ggcode", "worktrees", "outer-wt")
	if _, statErr := os.Stat(wtPath); statErr != nil {
		t.Fatalf("outer worktree not created at %s: %v", wtPath, statErr)
	}

	// spawn an agent isolation worktree from INSIDE the outer worktree
	agentPath, branch, err := createAgentWorktree(context.Background(), wtPath, "testagent")
	if err != nil {
		t.Fatalf("createAgentWorktree from inside worktree: %v", err)
	}

	// the agent worktree must be a SIBLING under the MAIN tree's
	// .ggcode/worktrees, not nested under the outer worktree
	if !strings.HasPrefix(agentPath, filepath.Join(dir, ".ggcode", "worktrees")) {
		t.Errorf("agent worktree must land in <main>/.ggcode/worktrees, got %s", agentPath)
	}
	if strings.Contains(agentPath, filepath.Join("outer-wt", ".ggcode")) {
		t.Errorf("agent worktree must not nest inside the outer worktree, got %s", agentPath)
	}
	if branch == "" || !strings.HasPrefix(branch, "agent-testagent") {
		t.Errorf("expected agent-testagent* branch, got %q", branch)
	}
	if _, statErr := os.Stat(agentPath); statErr != nil {
		t.Errorf("agent worktree not created: %v", statErr)
	}
}
