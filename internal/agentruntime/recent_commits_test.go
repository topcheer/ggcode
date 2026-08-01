package agentruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecentCommitsSection_RealGitRepo creates a temp git repo with commits
// and verifies the section is generated correctly.
func TestRecentCommitsSection_RealGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitInit(t, dir)
	gitCommit(t, dir, "initial commit")
	gitCommit(t, dir, "add feature X")
	gitCommit(t, dir, "fix bug Y")

	section := recentCommitsSection(dir)

	if section == "" {
		t.Fatal("expected non-empty section for repo with commits")
	}
	if !strings.Contains(section, "## Recent commits") {
		t.Errorf("expected section header, got:\n%s", section)
	}
	if !strings.Contains(section, "fix bug Y") {
		t.Errorf("expected most recent commit in section, got:\n%s", section)
	}
	if !strings.Contains(section, "initial commit") {
		t.Errorf("expected oldest commit in section, got:\n%s", section)
	}
}

// TestRecentCommitsSection_EmptyRepo verifies that a repo with no commits
// returns an empty string.
func TestRecentCommitsSection_EmptyRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitInit(t, dir)

	section := recentCommitsSection(dir)
	if section != "" {
		t.Errorf("expected empty section for repo with no commits, got:\n%s", section)
	}
}

// TestRecentCommitsSection_NonGitDir returns empty for a non-VCS directory.
func TestRecentCommitsSection_NonGitDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	section := recentCommitsSection(dir)
	if section != "" {
		t.Errorf("expected empty section for non-VCS dir, got:\n%s", section)
	}
}

// TestRecentCommitsSection_EmptyDir returns empty for empty workingDir.
func TestRecentCommitsSection_EmptyDir(t *testing.T) {
	t.Parallel()

	section := recentCommitsSection("")
	if section != "" {
		t.Errorf("expected empty section for empty dir")
	}
}

// TestRecentCommitsSection_MaxEntries verifies that the section caps at
// recentCommitsMax entries even when the repo has many commits.
func TestRecentCommitsSection_MaxEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitInit(t, dir)
	// Create more commits than recentCommitsMax.
	for i := 0; i < recentCommitsMax+5; i++ {
		gitCommit(t, dir, "commit "+string(rune('A'+i)))
	}

	section := recentCommitsSection(dir)
	if section == "" {
		t.Fatal("expected non-empty section")
	}

	// Count lines that look like commit entries (oneline hash + message).
	// Skip header lines.
	lines := 0
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "Latest commits") {
			continue
		}
		lines++
	}
	if lines > recentCommitsMax {
		t.Errorf("expected at most %d commit lines, got %d", recentCommitsMax, lines)
	}
}

// --- helpers ---

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", dir)
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Set required git config for commits in CI/test environments.
	for _, kv := range [][2]string{{"user.name", "test"}, {"user.email", "test@test.com"}} {
		c := exec.Command("git", "config", kv[0], kv[1])
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git config %s: %v", kv[0], err)
		}
	}
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	// Write a unique file so each commit has content.
	f, err := os.Create(filepath.Join(dir, "file_"+msg+".txt"))
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	f.Close()
	add := exec.Command("git", "add", "-A")
	add.Dir = dir
	if err := add.Run(); err != nil {
		t.Fatalf("git add: %v", err)
	}
	commit := exec.Command("git", "commit", "-m", msg, "--allow-empty")
	commit.Dir = dir
	if err := commit.Run(); err != nil {
		t.Fatalf("git commit %q: %v", msg, err)
	}
}
