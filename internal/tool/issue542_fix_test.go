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

// runGit runs a git command in dir, failing the test on error.
func runGit542(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo542 creates a temporary git repo with an initial commit.
func initRepo542(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit542(t, dir, "init", "-b", "main")
	runGit542(t, dir, "config", "user.email", "t@example.com")
	runGit542(t, dir, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit542(t, dir, "add", ".")
	runGit542(t, dir, "commit", "-m", "init")
	return dir
}

// TestExitWorktreeRemove_RejectsUserCreatedWorktree verifies Bug A: a manually
// created worktree (git worktree add outside .ggcode/worktrees) must NOT be
// recognized as a ggcode-managed worktree; exit_worktree remove must refuse,
// leaving the directory and the unpushed branch intact.
func TestExitWorktreeRemove_RejectsUserCreatedWorktree(t *testing.T) {
	mainRepo := initRepo542(t)

	userWT := filepath.Join(t.TempDir(), "user-worktree")
	runGit542(t, mainRepo, "worktree", "add", "-b", "userbranch", userWT)

	// Unpushed commit on the user branch — losing it is the destructive bug.
	if err := os.WriteFile(filepath.Join(userWT, "work.txt"), []byte("unpushed work"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit542(t, userWT, "add", ".")
	runGit542(t, userWT, "commit", "-m", "unpushed work")

	tool := ExitWorktree{WorkingDir: userWT}
	input, _ := json.Marshal(map[string]any{
		"action":          "remove",
		"discard_changes": true,
		"description":     "probe",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("exit_worktree remove must reject a user-created worktree, got success: %s", res.Content)
	}
	if !strings.Contains(res.Content, "not currently inside a worktree created by enter_worktree") {
		t.Fatalf("unexpected rejection message: %s", res.Content)
	}

	// The worktree directory and its unpushed branch must survive.
	if info, err := os.Stat(filepath.Join(userWT, "work.txt")); err != nil || info.IsDir() {
		t.Fatalf("user worktree file was destroyed: %v", err)
	}
	branches := runGit542(t, mainRepo, "branch", "--list", "userbranch")
	if branches == "" {
		t.Fatal("unpushed branch userbranch was deleted by exit_worktree")
	}

	// Cleanup worktree registration without touching branches.
	runGit542(t, mainRepo, "worktree", "remove", "--force", userWT)
}

// TestExitWorktreeRemove_AllowsGgcodeManagedWorktree verifies the ownership
// fix does not break the legitimate path: enter_worktree-style worktrees
// under <main>/.ggcode/worktrees/ are still recognized and removable.
func TestExitWorktreeRemove_AllowsGgcodeManagedWorktree(t *testing.T) {
	mainRepo := initRepo542(t)

	et := EnterWorktree{WorkingDir: mainRepo}
	res, err := et.Execute(context.Background(), json.RawMessage(`{"name":"wt542","description":"probe"}`))
	if err != nil || res.IsError {
		t.Fatalf("enter_worktree failed: %v %s", err, res.Content)
	}
	wtPath := res.SuggestedWorkingDir
	if wtPath == "" || !strings.Contains(filepath.ToSlash(wtPath), ".ggcode/worktrees/") {
		t.Fatalf("unexpected worktree path: %q", wtPath)
	}

	xt := ExitWorktree{WorkingDir: wtPath}
	xres, err := xt.Execute(context.Background(), json.RawMessage(`{"action":"remove","description":"probe"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if xres.IsError {
		t.Fatalf("exit_worktree remove rejected a ggcode-managed worktree: %s", xres.Content)
	}
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); !os.IsNotExist(err) {
		t.Fatalf("ggcode worktree was not removed: %v", err)
	}
}

// TestIsInsideWorktree_SubmoduleRejected: a submodule checkout has a .git file
// too, and must never be treated as a ggcode worktree.
func TestIsInsideWorktree_SubmoduleRejected(t *testing.T) {
	parent := initRepo542(t)
	child := initRepo542(t)
	// Git >= 2.38.1 denies the local file transport by default
	// (protocol.file.allow); this temp-dir submodule fixture legitimately
	// needs it, so enable it for this one command only.
	runGit542(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", child, "sub")
	runGit542(t, parent, "commit", "-m", "add submodule")

	subDir := filepath.Join(parent, "sub")
	ok, _, err := isInsideWorktree(subDir)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("submodule checkout must not be recognized as a ggcode worktree")
	}
}

// TestWriteFile_RelativePathResolvedAgainstWorkingDir verifies Bug B: a
// relative path with WorkingDir set lands in WorkingDir, not the process CWD.
func TestWriteFile_RelativePathResolvedAgainstWorkingDir(t *testing.T) {
	dirA := t.TempDir() // WorkingDir
	dirB := t.TempDir() // process CWD stand-in

	tf := WriteFile{WorkingDir: dirA}
	input, _ := json.Marshal(map[string]any{
		"path":        "rel.txt",
		"content":     "from worktree",
		"description": "probe",
	})
	// chdir to dirB to prove the file does NOT land in the process CWD.
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dirB); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	res, err := tf.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("write_file failed: %v %s", err, res.Content)
	}
	if _, err := os.Stat(filepath.Join(dirA, "rel.txt")); err != nil {
		t.Fatalf("file did not land in WorkingDir (%s): %v", dirA, err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "rel.txt")); !os.IsNotExist(err) {
		t.Fatalf("file unexpectedly landed in process CWD (%s)", dirB)
	}
}

// TestReadFile_RelativePathResolvedAgainstWorkingDir: read_file resolves
// relative paths against WorkingDir.
func TestReadFile_RelativePathResolvedAgainstWorkingDir(t *testing.T) {
	dirA := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "rel.txt"), []byte("content-A"), 0644); err != nil {
		t.Fatal(err)
	}

	tf := ReadFile{WorkingDir: dirA}
	res, err := tf.Execute(context.Background(), json.RawMessage(`{"path":"rel.txt"}`))
	if err != nil || res.IsError {
		t.Fatalf("read_file failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "content-A") {
		t.Fatalf("read_file did not resolve path against WorkingDir: %s", res.Content)
	}
}

// TestEditFile_RelativePathResolvedAgainstWorkingDir: edit_file resolves
// relative paths against WorkingDir.
func TestEditFile_RelativePathResolvedAgainstWorkingDir(t *testing.T) {
	dirA := t.TempDir()
	target := filepath.Join(dirA, "rel.txt")
	if err := os.WriteFile(target, []byte("alpha\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tf := EditFile{WorkingDir: dirA}
	input, _ := json.Marshal(map[string]any{
		"file_path":   "rel.txt",
		"old_text":    "alpha",
		"new_text":    "beta",
		"description": "probe",
	})
	res, err := tf.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("edit_file failed: %v %s", err, res.Content)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "beta\n" {
		t.Fatalf("edit_file did not edit the file under WorkingDir; content=%q", data)
	}
}

// TestResolveToolPath covers the shared resolver: absolute paths untouched,
// relative joined to workingDir, empty rejected.
func TestResolveToolPath(t *testing.T) {
	base := string(filepath.Separator) + "abs" + string(filepath.Separator) + "base"
	got, err := resolveToolPath(base, "/other")
	if err != nil || got != base {
		t.Fatalf("absolute path must be untouched: %q %v", got, err)
	}
	got, err = resolveToolPath("sub/f.txt", base)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "sub", "f.txt"); got != want {
		t.Fatalf("relative path: got %q want %q", got, want)
	}
	if _, err := resolveToolPath("   ", base); err == nil {
		t.Fatal("whitespace-only path must be rejected")
	}
}

// TestIsEmptyValue_WhitespaceStringIsEmpty verifies Bug C: a whitespace-only
// string value (" ") is treated as empty by the agent-level required-param
// gateway, matching CheckRequired's Trim semantics.
func TestIsEmptyValue_WhitespaceStringIsEmpty(t *testing.T) {
	cases := map[string]bool{
		`""`:    true,
		`" "`:   true,
		`"\t"`:  true,
		`"  "`:  true,
		`"a"`:   false,
		`" a "`: false,
		`null`:  true,
		// #568: explicit empty containers are provided values.
		`[]`:    false,
		`{}`:    false,
		`0`:     false,
		`false`: false,
	}
	for val, wantEmpty := range cases {
		if got := isEmptyValue(json.RawMessage(val)); got != wantEmpty {
			t.Errorf("isEmptyValue(%s) = %v, want %v", val, got, wantEmpty)
		}
	}
}

// TestValidateRequiredParams_WhitespaceBypass: end-to-end gateway check —
// a required field set to " " must be reported as missing.
func TestValidateRequiredParams_WhitespaceBypass(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"pattern": {"type": "string"}},
		"required": ["pattern"]
	}`)
	args := json.RawMessage(`{"pattern": " "}`)
	msg := ValidateRequiredParams(schema, args)
	if msg == "" {
		t.Fatal("whitespace-only required param must be flagged as missing")
	}
	if !strings.Contains(msg, "pattern") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

// TestValidateRequiredParams_ExplicitEmptyIsProvided: regression guard for
// #568 — an explicit [] or {} counts as provided; absent and null stay missing.
func TestValidateRequiredParams_ExplicitEmptyIsProvided(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"files":{"type":"array"}},"required":["files"]}`)
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{"files":[]}`)); msg != "" {
		t.Fatalf("explicit empty array must be provided, got: %s", msg)
	}
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{"files":["a"]}`)); msg != "" {
		t.Fatalf("non-empty array flagged: %s", msg)
	}
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{"files":0}`)); msg != "" {
		t.Fatalf("numeric 0 must be considered present: %s", msg)
	}
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{}`)); msg == "" {
		t.Fatal("absent required field must still be flagged missing")
	}
	if msg := ValidateRequiredParams(schema, json.RawMessage(`{"files":null}`)); msg == "" {
		t.Fatal("null required field must still be flagged missing")
	}
	objSchema := json.RawMessage(`{"type":"object","properties":{"opts":{"type":"object"}},"required":["opts"]}`)
	if msg := ValidateRequiredParams(objSchema, json.RawMessage(`{"opts":{}}`)); msg != "" {
		t.Fatalf("explicit empty object must be provided, got: %s", msg)
	}
}
