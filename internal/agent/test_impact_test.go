package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitRepo creates a temporary git repository for testing.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v failed: %v", args, err)
		}
	}
	return dir
}

// writeGoFile writes a Go source file and creates parent directories.
func writeGoFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	abs := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestChangedGoFilesFromGit_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	// Not a git repo — should return nil.
	if files := changedGoFilesFromGit(tmp); files != nil {
		t.Errorf("expected nil in non-git dir, got %v", files)
	}
}

func TestChangedGoFilesFromGit_NoChanges(t *testing.T) {
	dir := initGitRepo(t)
	writeGoFile(t, dir, "main.go", "package main\n")
	if err := exec.Command("git", "-C", dir, "add", ".").Run(); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	files := changedGoFilesFromGit(dir)
	if len(files) != 0 {
		t.Errorf("expected no changed files, got %v", files)
	}
}

func TestChangedGoFilesFromGit_NewFile(t *testing.T) {
	dir := initGitRepo(t)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Create a new untracked Go file.
	writeGoFile(t, dir, "internal/foo/bar.go", "package foo\n")

	files := changedGoFilesFromGit(dir)
	if len(files) != 1 {
		t.Fatalf("expected 1 changed file, got %d: %v", len(files), files)
	}
	if files[0] != "internal/foo/bar.go" {
		t.Errorf("expected internal/foo/bar.go, got %s", files[0])
	}
}

func TestChangedGoFilesFromGit_ExcludesTestFiles(t *testing.T) {
	dir := initGitRepo(t)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Create both a source file and a test file.
	writeGoFile(t, dir, "foo.go", "package main\n")
	writeGoFile(t, dir, "foo_test.go", "package main\n")

	files := changedGoFilesFromGit(dir)
	for _, f := range files {
		if f == "foo_test.go" {
			t.Errorf("test files should be excluded, but found %s", f)
		}
	}
}

func TestChangedGoFilesFromGit_EmptyWorkingDir(t *testing.T) {
	if files := changedGoFilesFromGit(""); files != nil {
		t.Error("expected nil for empty workingDir")
	}
}

func TestChangedGoPackageDirs(t *testing.T) {
	dir := initGitRepo(t)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Create files in multiple packages.
	writeGoFile(t, dir, "internal/agent/foo.go", "package agent\n")
	writeGoFile(t, dir, "internal/util/bar.go", "package util\n")
	writeGoFile(t, dir, "internal/agent/baz.go", "package agent\n")

	dirs := changedGoPackageDirs(dir)
	if len(dirs) != 2 {
		t.Fatalf("expected 2 package dirs, got %d: %v", len(dirs), dirs)
	}
	// Should be sorted.
	if dirs[0] != "internal/agent" || dirs[1] != "internal/util" {
		t.Errorf("unexpected dirs: %v", dirs)
	}
}

func TestChangedGoPackageDirs_NoChanges(t *testing.T) {
	dir := initGitRepo(t)
	if dirs := changedGoPackageDirs(dir); dirs != nil {
		t.Errorf("expected nil for clean repo, got %v", dirs)
	}
}

func TestHasGoTestFile_Exists(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "foo.go", "package main\n")
	writeGoFile(t, dir, "foo_test.go", "package main\n")

	if !hasGoTestFile(dir, "foo.go") {
		t.Error("expected hasGoTestFile=true when foo_test.go exists")
	}
}

func TestHasGoTestFile_NotExists(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "bar.go", "package main\n")

	if hasGoTestFile(dir, "bar.go") {
		t.Error("expected hasGoTestFile=false when bar_test.go does not exist")
	}
}

func TestHasGoTestFile_Subdir(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "internal/foo/handler.go", "package foo\n")
	writeGoFile(t, dir, "internal/foo/handler_test.go", "package foo\n")

	if !hasGoTestFile(dir, "internal/foo/handler.go") {
		t.Error("expected hasGoTestFile=true for subdir file")
	}
}

func TestUntestedChangedFiles(t *testing.T) {
	dir := initGitRepo(t)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// File with test.
	writeGoFile(t, dir, "tested.go", "package main\n")
	writeGoFile(t, dir, "tested_test.go", "package main\n")
	// File without test.
	writeGoFile(t, dir, "untested.go", "package main\n")

	untested := untestedChangedFiles(dir)
	if len(untested) != 1 {
		t.Fatalf("expected 1 untested file, got %d: %v", len(untested), untested)
	}
	if untested[0] != "untested.go" {
		t.Errorf("expected untested.go, got %s", untested[0])
	}
}

func TestUntestedChangedFiles_AllTested(t *testing.T) {
	dir := initGitRepo(t)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	writeGoFile(t, dir, "foo.go", "package main\n")
	writeGoFile(t, dir, "foo_test.go", "package main\n")

	if untested := untestedChangedFiles(dir); len(untested) != 0 {
		t.Errorf("expected 0 untested files, got %v", untested)
	}
}

func TestImpactScopedTestCommand(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Change files in two packages.
	writeGoFile(t, dir, "internal/agent/foo.go", "package agent\n")
	writeGoFile(t, dir, "internal/util/bar.go", "package util\n")

	cmd := impactScopedTestCommand(dir)
	if cmd == "" {
		t.Fatal("expected non-empty command")
	}
	if cmd != "go test ./internal/agent/ ./internal/util/" {
		t.Errorf("unexpected command: %s", cmd)
	}
}

func TestImpactScopedTestCommand_NoGoMod(t *testing.T) {
	dir := initGitRepo(t)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	writeGoFile(t, dir, "foo.go", "package main\n")

	if cmd := impactScopedTestCommand(dir); cmd != "" {
		t.Errorf("expected empty for non-Go-module dir, got %s", cmd)
	}
}

func TestImpactScopedTestCommand_EmptyWorkingDir(t *testing.T) {
	if cmd := impactScopedTestCommand(""); cmd != "" {
		t.Errorf("expected empty for empty dir, got %s", cmd)
	}
}

func TestImpactScopedTestCommand_NoChanges(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	if cmd := impactScopedTestCommand(dir); cmd != "" {
		t.Errorf("expected empty for clean repo, got %s", cmd)
	}
}

func TestImpactScopedTestCommand_SinglePackage(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	writeGoFile(t, dir, "pkg/foo.go", "package pkg\n")

	cmd := impactScopedTestCommand(dir)
	if cmd != "go test ./pkg/" {
		t.Errorf("expected 'go test ./pkg/', got %s", cmd)
	}
}

func TestTestCoverageNudge_NoChanges(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	if nudge := testCoverageNudge(dir); nudge != "" {
		t.Errorf("expected empty nudge for clean repo, got %s", nudge)
	}
}

func TestTestCoverageNudge_WithUntested(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	writeGoFile(t, dir, "foo.go", "package main\n")
	writeGoFile(t, dir, "bar.go", "package main\n")

	nudge := testCoverageNudge(dir)
	if nudge == "" {
		t.Fatal("expected non-empty nudge")
	}
	if !strings.Contains(nudge, "2 changed files have no tests") {
		t.Errorf("unexpected nudge: %s", nudge)
	}
}

func TestTestCoverageNudge_WithTests(t *testing.T) {
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	writeGoFile(t, dir, "foo.go", "package main\n")
	writeGoFile(t, dir, "foo_test.go", "package main\n")

	if nudge := testCoverageNudge(dir); nudge != "" {
		t.Errorf("expected empty nudge when all tested, got %s", nudge)
	}
}

func TestPostEditVerifyHintWithImpactAndCoverage(t *testing.T) {
	// This test verifies that postEditVerifyHint integrates the impact-scoped
	// command and coverage nudge when in a git repo. We create a real git repo
	// with changed Go files and verify the hint includes both pieces.
	dir := initGitRepo(t)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	writeGoFile(t, dir, "main.go", "package main\n")
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Create an untested Go file in a subpackage.
	sub := filepath.Join(dir, "internal", "agent")
	os.MkdirAll(sub, 0755)
	os.WriteFile(filepath.Join(sub, "foo.go"), []byte("package agent\n"), 0644)

	a := &Agent{workingDir: dir}

	args, _ := json.Marshal(map[string]string{"file_path": filepath.Join(sub, "foo.go")})
	var hint string
	for i := 0; i < 3; i++ {
		hint = a.postEditVerifyHint("edit_file", args)
	}
	if hint == "" {
		t.Fatal("expected hint after 3 edits")
	}
	// Should contain the full-suite fallback.
	if !strings.Contains(hint, "go build ./...") {
		t.Errorf("hint should contain full-suite command, got: %s", hint)
	}
	// Should contain the coverage nudge since foo.go has no test.
	if !strings.Contains(hint, "Test coverage gap") {
		t.Errorf("hint should contain coverage nudge, got: %s", hint)
	}
}
