package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangeReconcile_NoGitRepo(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{filepath.Join(dir, "main.go")},
	}
	// Not a git repo — should return empty.
	msg := a.checkChangeReconcile(stats)
	if msg != "" {
		t.Fatalf("expected empty message in non-git dir, got: %s", msg)
	}
}

func TestChangeReconcile_NoChanges(t *testing.T) {
	dir := initGitRepo(t)
	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{filepath.Join(dir, "main.go")},
	}
	msg := a.checkChangeReconcile(stats)
	if msg != "" {
		t.Fatalf("expected empty when no changes, got: %s", msg)
	}
}

func TestChangeReconcile_AllExpected(t *testing.T) {
	dir := initGitRepo(t)

	// Create and commit initial file.
	mainGo := filepath.Join(dir, "main.go")
	mustWriteCR(t, mainGo, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// Agent edits main.go (expected).
	mustWriteCR(t, mainGo, "package main\n\nfunc main() {}\n")

	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkChangeReconcile(stats)
	if msg != "" {
		t.Fatalf("expected empty when only expected files changed, got: %s", msg)
	}
}

func TestChangeReconcile_UnexpectedSourceFile(t *testing.T) {
	dir := initGitRepo(t)

	// Create and commit initial files.
	mainGo := filepath.Join(dir, "main.go")
	helperGo := filepath.Join(dir, "helper.go")
	mustWriteCR(t, mainGo, "package main\n")
	mustWriteCR(t, helperGo, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// Agent edits main.go but a command also modifies helper.go.
	mustWriteCR(t, mainGo, "package main\n\nfunc main() {}\n")
	mustWriteCR(t, helperGo, "package main\n\n// unexpected change\n")

	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkChangeReconcile(stats)
	if msg == "" {
		t.Fatal("expected warning for unexpected source file change")
	}
	if !strings.Contains(msg, "helper.go") {
		t.Fatalf("expected 'helper.go' in message, got: %s", msg)
	}
}

func TestChangeReconcile_SideEffectFilesIgnored(t *testing.T) {
	dir := initGitRepo(t)

	// Create and commit initial files.
	mainGo := filepath.Join(dir, "main.go")
	goSum := filepath.Join(dir, "go.sum")
	mustWriteCR(t, mainGo, "package main\n")
	mustWriteCR(t, goSum, "initial content\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// Agent edits main.go; go mod tidy changes go.sum (expected side effect).
	mustWriteCR(t, mainGo, "package main\n\nfunc main() {}\n")
	mustWriteCR(t, goSum, "modified by go mod tidy\n")

	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkChangeReconcile(stats)
	if msg != "" {
		t.Fatalf("expected no warning for lock file side effect, got: %s", msg)
	}
}

func TestChangeReconcile_AlreadyFired(t *testing.T) {
	dir := initGitRepo(t)

	mainGo := filepath.Join(dir, "main.go")
	helperGo := filepath.Join(dir, "helper.go")
	mustWriteCR(t, mainGo, "package main\n")
	mustWriteCR(t, helperGo, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	mustWriteCR(t, mainGo, "package main\n\nfunc main() {}\n")
	mustWriteCR(t, helperGo, "package main\n\n// changed\n")

	a := &Agent{
		changeReconcile: &changeReconcileState{fired: true},
		workingDir:      dir,
	}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkChangeReconcile(stats)
	if msg != "" {
		t.Fatal("expected empty when gate already fired")
	}
}

func TestChangeReconcile_NoCodeChanged(t *testing.T) {
	dir := initGitRepo(t)

	mainGo := filepath.Join(dir, "main.go")
	mustWriteCR(t, mainGo, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// Agent didn't edit any files.
	mustWriteCR(t, mainGo, "package main\n\n// changed externally\n")

	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}
	stats := &RunStats{
		ToolCalls: map[string]int{"run_command": 1},
	}
	msg := a.checkChangeReconcile(stats)
	// Should still detect unexpected changes even if agent used no edit tools.
	if msg == "" {
		t.Fatal("expected warning when source file changed but agent didn't edit anything")
	}
}

// --- helpers ---

func TestChangeReconcile_PreRunDirtyExcluded(t *testing.T) {
	dir := initGitRepo(t)

	// Create and commit initial files.
	mainGo := filepath.Join(dir, "main.go")
	helperGo := filepath.Join(dir, "helper.go")
	mustWriteCR(t, mainGo, "package main\n")
	mustWriteCR(t, helperGo, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// User has pre-existing uncommitted changes to helper.go BEFORE the run.
	mustWriteCR(t, helperGo, "package main\n\n// user's own change\n")

	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}

	// Capture pre-run state — should record helper.go as dirty.
	a.changeReconcile.capturePreRunState(dir)
	if a.changeReconcile.dirtyFileCount() != 1 {
		t.Fatalf("expected 1 pre-run dirty file, got %d", a.changeReconcile.dirtyFileCount())
	}

	// Agent edits main.go during the run.
	mustWriteCR(t, mainGo, "package main\n\nfunc main() {}\n")

	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	// helper.go was dirty BEFORE the run, so it should NOT be flagged.
	msg := a.checkChangeReconcile(stats)
	if msg != "" {
		t.Fatalf("expected no warning for pre-existing dirty file, got: %s", msg)
	}
}

func TestChangeReconcile_PreRunDirtyButAgentAlsoEdited(t *testing.T) {
	dir := initGitRepo(t)

	mainGo := filepath.Join(dir, "main.go")
	helperGo := filepath.Join(dir, "helper.go")
	mustWriteCR(t, mainGo, "package main\n")
	mustWriteCR(t, helperGo, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// User has pre-existing changes to helper.go.
	mustWriteCR(t, helperGo, "package main\n\n// user change\n")

	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}
	a.changeReconcile.capturePreRunState(dir)

	// Agent also edits helper.go and main.go.
	mustWriteCR(t, mainGo, "package main\n\nfunc main() {}\n")
	mustWriteCR(t, helperGo, "package main\n\n// user change + agent change\n")

	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 2},
		FilesEdited: []string{"main.go", "helper.go"},
	}
	// Both files were edited by agent — no unexpected changes.
	msg := a.checkChangeReconcile(stats)
	if msg != "" {
		t.Fatalf("expected no warning when agent edited all changed files, got: %s", msg)
	}
}

func TestChangeReconcile_ResetClearsPreRunState(t *testing.T) {
	dir := initGitRepo(t)

	mainGo := filepath.Join(dir, "main.go")
	mustWriteCR(t, mainGo, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	mustWriteCR(t, mainGo, "package main\n\n// dirty\n")

	c := newChangeReconcileState()
	c.capturePreRunState(dir)
	if c.dirtyFileCount() != 1 {
		t.Fatalf("expected 1 dirty file before reset, got %d", c.dirtyFileCount())
	}

	c.reset()
	if c.dirtyFileCount() != 0 {
		t.Fatalf("expected 0 dirty files after reset, got %d", c.dirtyFileCount())
	}
	if c.fired || c.preRunCaptured {
		t.Fatal("expected fired and preRunCaptured to be false after reset")
	}
}

func TestChangeReconcile_PreRunDirtyNewUnexpectedStillDetected(t *testing.T) {
	dir := initGitRepo(t)

	mainGo := filepath.Join(dir, "main.go")
	helperGo := filepath.Join(dir, "helper.go")
	thirdGo := filepath.Join(dir, "third.go")
	mustWriteCR(t, mainGo, "package main\n")
	mustWriteCR(t, helperGo, "package main\n")
	mustWriteCR(t, thirdGo, "package main\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// User has pre-existing changes to helper.go.
	mustWriteCR(t, helperGo, "package main\n\n// user change\n")

	a := &Agent{
		changeReconcile: newChangeReconcileState(),
		workingDir:      dir,
	}
	a.changeReconcile.capturePreRunState(dir)

	// Agent edits main.go; a side-effect command also modifies third.go.
	mustWriteCR(t, mainGo, "package main\n\nfunc main() {}\n")
	mustWriteCR(t, thirdGo, "package main\n\n// side effect\n")

	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"main.go"},
	}
	msg := a.checkChangeReconcile(stats)
	// helper.go is excluded (pre-existing), but third.go should still be flagged.
	if msg == "" {
		t.Fatal("expected warning for unexpected third.go change")
	}
	if !strings.Contains(msg, "third.go") {
		t.Fatalf("expected 'third.go' in message, got: %s", msg)
	}
	if strings.Contains(msg, "helper.go") {
		t.Fatalf("helper.go should be excluded as pre-existing, but found in: %s", msg)
	}
}

// initGitRepo is defined in test_impact_test.go.

func runGitCR(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
}

func mustWriteCR(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
