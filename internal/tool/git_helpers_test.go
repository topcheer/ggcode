package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitCommandEnv(t *testing.T) {
	cmd := gitCommand(t.Context(), "status")
	found := false
	for _, env := range cmd.Env {
		if env == "GIT_PAGER=cat" {
			found = true
			break
		}
	}
	if !found {
		t.Error("gitCommand should set GIT_PAGER=cat")
	}
}

func TestGitTrackedFiles(t *testing.T) {
	// Skip if git not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir, _ := os.MkdirTemp("", "ggcode_git_test_*")
	defer os.RemoveAll(dir)

	// Not a git repo yet
	if tracked := gitTrackedFiles(t.Context(), dir); tracked != nil {
		t.Fatal("expected nil outside git repo")
	}

	// Init git repo
	cmd := exec.Command("git", "init", dir)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Create files
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log output"), 0644)

	// Create .gitignore
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0644)

	// Add and commit
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	cmd.Run()

	tracked := gitTrackedFiles(t.Context(), dir)
	if tracked == nil {
		t.Fatal("expected non-nil tracked set")
	}
	if _, ok := tracked["hello.go"]; !ok {
		t.Error("hello.go should be tracked")
	}
	if _, ok := tracked["debug.log"]; ok {
		t.Error("debug.log should be ignored by .gitignore")
	}
}

func TestIsGitCommitCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{`git commit -m "hello"`, true},
		{`git status`, false},
		{`GIT_PAGER=cat git commit -m "hello"`, true},
		{`git -c user.name=x commit -m "hello"`, true},
		{`git log --oneline`, false},
		{`echo "not git"`, false},
		{`git add -A`, false},
	}

	for _, tt := range tests {
		if got := isGitCommitCommand(tt.cmd); got != tt.want {
			t.Errorf("isGitCommitCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestInjectCoAuthorTrailer(t *testing.T) {
	// Inject into -m message
	result := injectCoAuthorTrailer(`git commit -m "feat: add feature"`)
	if !containsCoAuthor(result) {
		t.Errorf("expected Co-Authored-By in result: %s", result)
	}

	// Don't duplicate
	result2 := injectCoAuthorTrailer(result)
	if count := countCoAuthor(result2); count != 1 {
		t.Errorf("expected exactly 1 Co-Authored-By, got %d: %s", count, result2)
	}

	// No -m: use --trailer
	result3 := injectCoAuthorTrailer(`git commit`)
	if !containsCoAuthor(result3) {
		t.Errorf("expected Co-Authored-By via --trailer: %s", result3)
	}
	if !containsString(result3, "--trailer") {
		t.Errorf("expected --trailer in result: %s", result3)
	}
}

func TestIsBinaryFile(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_binary_test_*")
	defer os.RemoveAll(dir)

	// Text file
	textFile := filepath.Join(dir, "test.txt")
	os.WriteFile(textFile, []byte("hello world\nthis is text"), 0644)
	if isBinaryFile(textFile) {
		t.Error("text file should not be binary")
	}

	// Binary file (contains null bytes)
	binFile := filepath.Join(dir, "test.bin")
	os.WriteFile(binFile, []byte("hello\x00world"), 0644)
	if !isBinaryFile(binFile) {
		t.Error("file with null bytes should be binary")
	}
}

func TestIsGitCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"git status", true},
		{"git log --oneline", true},
		{"GIT_PAGER=cat git status", true},
		{"echo hello", false},
		{"gitcommit", false},
	}

	for _, tt := range tests {
		if got := isGitCommand(tt.cmd); got != tt.want {
			t.Errorf("isGitCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func containsCoAuthor(s string) bool {
	return containsString(s, "Co-Authored-By: ggcode")
}

func countCoAuthor(s string) int {
	count := 0
	search := "Co-Authored-By: ggcode"
	idx := 0
	for {
		pos := containsStringAt(s[idx:], search)
		if pos < 0 {
			break
		}
		count++
		idx += pos + len(search)
	}
	return count
}

func containsString(s, substr string) bool {
	return containsStringAt(s, substr) >= 0
}

func containsStringAt(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
