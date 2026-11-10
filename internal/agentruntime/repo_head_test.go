package agentruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue5370 pins the single-line repo HEAD marker contract (#5370):
// the volatile "Recent commits" message list was removed from the startup
// snapshot; one stable-format line keeps the change-detection signal
// (branch@sha + behind-count) and commit context moves to git_log.
func TestRepoHeadSection_RealGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitInit(t, dir)
	gitCommit(t, dir, "initial commit")
	gitCommit(t, dir, "add feature X")
	gitCommit(t, dir, "fix bug Y")

	section := repoHeadSection(dir)

	if section == "" {
		t.Fatal("expected non-empty section for repo with commits")
	}
	if !strings.Contains(section, "## Repo HEAD") {
		t.Errorf("expected section header, got:\n%s", section)
	}
	if !strings.Contains(section, "repo HEAD at session start: ") {
		t.Errorf("expected marker prefix, got:\n%s", section)
	}
	if !strings.Contains(section, "commit context: use git_log") {
		t.Errorf("expected git_log guidance, got:\n%s", section)
	}
}

// Branch must be named explicitly so a parked feature branch cannot be
// misread as main state (harm 1, shared-checkout-context-snapshot-trap).
func TestRepoHeadSection_NamesBranchExplicitly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitInit(t, dir)
	gitCommit(t, dir, "initial commit")

	if err := runGit(dir, "checkout", "-b", "feature/parked-branch"); err != nil {
		t.Fatalf("checkout branch: %v", err)
	}
	gitCommit(t, dir, "work on feature")

	section := repoHeadSection(dir)
	if !strings.Contains(section, "feature/parked-branch@") {
		t.Errorf("expected explicit branch@sha marker, got:\n%s", section)
	}
}

// Commit messages must NOT be injected: the whole point of #5370 is that
// time-varying message text belongs behind git_log, not in the prompt.
func TestRepoHeadSection_NoCommitMessages(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitInit(t, dir)
	gitCommit(t, dir, "initial commit")
	gitCommit(t, dir, "very distinctive secret message body")

	section := repoHeadSection(dir)
	if strings.Contains(section, "very distinctive secret message body") {
		t.Errorf("commit message leaked into prompt section:\n%s", section)
	}
	// Single payload line under the header (header block + one line).
	body := strings.TrimPrefix(section, "\n\n## Repo HEAD\n")
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 1 {
		t.Errorf("expected single-line marker, got %d lines:\n%s", len(lines), section)
	}
}

// Behind-count vs upstream preserves the "did main move while I was
// parked" change-detection bit without any per-commit lines.
func TestRepoHeadSection_BehindCountVsUpstream(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitInit(t, dir)
	gitCommit(t, dir, "initial commit")

	if err := runGit(dir, "branch", "-M", "main"); err != nil {
		t.Fatalf("rename to main: %v", err)
	}
	if err := runGit(dir, "checkout", "-b", "topic"); err != nil {
		t.Fatalf("checkout topic: %v", err)
	}

	// Simulate upstream divergence: main advances while we stay on topic.
	gitCommit(t, dir, "work on topic")
	if err := runGit(dir, "checkout", "main"); err != nil {
		t.Fatalf("checkout main: %v", err)
	}
	gitCommit(t, dir, "merge landed on main 1")
	gitCommit(t, dir, "merge landed on main 2")
	if err := runGit(dir, "checkout", "topic"); err != nil {
		t.Fatalf("checkout topic: %v", err)
	}
	if err := runGit(dir, "branch", "--set-upstream-to=main"); err != nil {
		t.Fatalf("set upstream: %v", err)
	}

	section := repoHeadSection(dir)
	if !strings.Contains(section, "topic@") {
		t.Errorf("expected topic branch marker, got:\n%s", section)
	}
	if !strings.Contains(section, "2 behind upstream") {
		t.Errorf("expected behind-count vs upstream, got:\n%s", section)
	}
}

func TestRepoHeadSection_EmptyRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	gitInit(t, dir)

	section := repoHeadSection(dir)
	if section != "" {
		t.Errorf("expected empty section for repo with no commits, got:\n%s", section)
	}
}

func TestRepoHeadSection_NonGitDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	section := repoHeadSection(dir)
	if section != "" {
		t.Errorf("expected empty section for non-VCS dir, got:\n%s", section)
	}
}

func TestRepoHeadSection_EmptyDir(t *testing.T) {
	t.Parallel()

	section := repoHeadSection("")
	if section != "" {
		t.Errorf("expected empty section for empty dir")
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := runGit(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	for _, kv := range [][2]string{{"user.name", "test"}, {"user.email", "test@test.com"}} {
		if err := runGit(dir, "config", kv[0], kv[1]); err != nil {
			t.Fatalf("git config %s: %v", kv[0], err)
		}
	}
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()

	f, err := os.Create(filepath.Join(dir, "file_"+strings.ReplaceAll(msg, " ", "_")+".txt"))
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	f.Close()
	if err := runGit(dir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(dir, "commit", "-m", msg, "--allow-empty"); err != nil {
		t.Fatalf("git commit %q: %v", msg, err)
	}
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}
