package agentruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Field report 2026-11-26 (GGAiAce): 450 of 473 porcelain lines were tracked
// .ggcode/memory churn (auto memory writes + the curator's prune), so the
// 20-line "Modified files" window showed ONLY harness state and the real
// changes were hidden behind "... (453 more)". Harness state is not the
// user's working-tree change and must not take the window.
func TestModifiedFilesSection_ExcludesHarnessState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := setupTestGitRepo(t)
	// Tracked harness state, now modified / deleted / joined by untracked files.
	write(t, filepath.Join(dir, ".ggcode", "memory", "keep.md"), "v1\n")
	write(t, filepath.Join(dir, ".ggcode", "memory", "gone.md"), "v1\n")
	write(t, filepath.Join(dir, ".ggcode", "agent-rules.json"), "{}\n")
	gitIn(t, dir, "add", "-f", ".")
	gitIn(t, dir, "commit", "-q", "-m", "harness state tracked")
	write(t, filepath.Join(dir, ".ggcode", "memory", "keep.md"), "v2\n")
	if err := os.Remove(filepath.Join(dir, ".ggcode", "memory", "gone.md")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".ggcode", "memory", "experience", "new.md"), "x\n")
	write(t, filepath.Join(dir, ".ggcode", "state.json"), "{}\n")
	// The user's actual change.
	write(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")

	got := modifiedFilesSection(dir)
	if got == "" {
		t.Fatal("real change must still be listed")
	}
	if strings.Contains(got, ".ggcode") {
		t.Fatalf("harness state leaked into the Modified files section:\n%s", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Fatalf("real change missing from the Modified files section:\n%s", got)
	}
	if strings.Contains(got, "more)") {
		t.Fatalf("no truncation marker expected once harness state is dropped:\n%s", got)
	}
}

func TestModifiedFilesSection_SilentWhenOnlyHarnessStateChanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := setupTestGitRepo(t)
	write(t, filepath.Join(dir, ".ggcode", "memory", "a.md"), "v1\n")
	gitIn(t, dir, "add", "-f", ".")
	gitIn(t, dir, "commit", "-q", "-m", "harness state tracked")
	write(t, filepath.Join(dir, ".ggcode", "memory", "a.md"), "v2\n")
	write(t, filepath.Join(dir, ".ggcode", "memory", "b.md"), "new\n")
	if got := modifiedFilesSection(dir); got != "" {
		t.Fatalf("harness-only churn must produce no section, got:\n%s", got)
	}
}

// A subdirectory working dir still drops the ROOT .ggcode/ (git prints it
// as "../.ggcode/..." from the subdir; the segment check catches it).
func TestModifiedFilesSection_SubdirDropRootHarnessState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := setupTestGitRepo(t)
	write(t, filepath.Join(root, ".ggcode", "memory", "a.md"), "v1\n")
	write(t, filepath.Join(root, "app", "main.go"), "package main\n")
	gitIn(t, root, "add", "-f", ".")
	gitIn(t, root, "commit", "-q", "-m", "base")
	write(t, filepath.Join(root, ".ggcode", "memory", "a.md"), "v2\n")
	write(t, filepath.Join(root, "app", "main.go"), "package main\n\nfunc main() {}\n")
	got := modifiedFilesSection(filepath.Join(root, "app"))
	if strings.Contains(got, ".ggcode") || !strings.Contains(got, "main.go") {
		t.Fatalf("subdir section must list only the real change:\n%s", got)
	}
}
