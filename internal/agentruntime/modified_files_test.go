package agentruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"}, {"config", "commit.gpgsign", "false"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Initial commit
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "add", "README.md")
	c.Dir = dir
	c.Run()
	c = exec.Command("git", "commit", "-m", "init")
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	c.Run()
	return dir
}

func TestModifiedFilesSection_NotARepo(t *testing.T) {
	dir := t.TempDir()
	got := modifiedFilesSection(dir)
	if got != "" {
		t.Errorf("expected empty string for non-repo, got %q", got)
	}
}

func TestModifiedFilesSection_CleanRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := setupTestGitRepo(t)
	got := modifiedFilesSection(dir)
	if got != "" {
		t.Errorf("expected empty string for clean repo, got %q", got)
	}
}

func TestModifiedFilesSection_DirtyRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := setupTestGitRepo(t)

	// Create modified, staged, and untracked files
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "staged.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "add", "staged.go")
	c.Dir = dir
	c.Run()

	got := modifiedFilesSection(dir)
	if got == "" {
		t.Fatal("expected non-empty modified files section")
	}
	if !strings.Contains(got, "## Modified files") {
		t.Errorf("expected header in %q", got)
	}
	if !strings.Contains(got, "README.md") {
		t.Errorf("expected README.md in %q", got)
	}
	if !strings.Contains(got, "new.go") {
		t.Errorf("expected new.go in %q", got)
	}
	if !strings.Contains(got, "staged.go") {
		t.Errorf("expected staged.go in %q", got)
	}
}

func TestModifiedFilesSection_Truncation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := setupTestGitRepo(t)

	// Create and commit tracked files first, then modify them to exceed the cap.
	// Using tracked files is much faster than untracked ones for git status.
	fileCount := modifiedFilesMax + 3
	for i := 0; i < fileCount; i++ {
		name := filepath.Join(dir, "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go")
		if err := os.WriteFile(name, []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	c := exec.Command("git", "add", ".")
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	c.Run()
	c = exec.Command("git", "commit", "-m", "add files")
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	c.Run()

	// Now modify all of them
	for i := 0; i < fileCount; i++ {
		name := filepath.Join(dir, "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go")
		if err := os.WriteFile(name, []byte("package main // changed\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got := modifiedFilesSection(dir)
	if got == "" {
		t.Fatal("expected non-empty modified files section")
	}
	if !strings.Contains(got, "more)") {
		t.Errorf("expected truncation indicator in %q", got)
	}
}

func TestModifiedFilesSection_EmptyDir(t *testing.T) {
	got := modifiedFilesSection("")
	if got != "" {
		t.Errorf("expected empty string for empty dir, got %q", got)
	}
}

func TestModifiedFilesSection_NoHang(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := setupTestGitRepo(t)
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0644)

	done := make(chan string, 1)
	go func() {
		done <- modifiedFilesSection(dir)
	}()
	select {
	case <-time.After(10 * time.Second):
		t.Fatal("modifiedFilesSection hung")
	case <-done:
		// OK
	}

	// Use ctx to avoid unused import
	_ = context.Background()
}
