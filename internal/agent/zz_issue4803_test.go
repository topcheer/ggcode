package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mkGitRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", "."},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-q", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if branch != "main" {
		if out, err := exec.Command("git", "-C", dir, "checkout", "-q", "-b", branch).CombinedOutput(); err != nil {
			t.Fatalf("git checkout: %v\n%s", err, out)
		}
	}
	return dir
}

func runArgs(t *testing.T, cmd string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// #4803 (S-35): build/test commands executed on a non-trunk branch must
// carry the branch-baseline advisory; on the trunk they stay silent.
func TestIssue4803_BaselineAdvisoryOnBranch(t *testing.T) {
	a := &Agent{baselineGuard: &mainTreeBaselineGuard{}}

	fix := mkGitRepo(t, "fix-1234")
	if adv := a.advisoryFor("run_command", runArgs(t, "go build ./..."), fix); adv == "" {
		t.Fatal("build on a fix branch must produce the [baseline] advisory")
	} else if !strings.Contains(adv, "fix-1234") {
		t.Fatalf("advisory must name the branch, got %q", adv)
	}

	mainDir := mkGitRepo(t, "main")
	if adv := a.advisoryFor("run_command", runArgs(t, "go test ./..."), mainDir); adv != "" {
		t.Fatalf("trunk run must stay silent, got %q", adv)
	}
}

// Non-build commands and non-repo dirs never fire; the advisory is scoped
// to build/test-shaped run_command invocations.
func TestIssue4803_AdvisoryScope(t *testing.T) {
	a := &Agent{baselineGuard: &mainTreeBaselineGuard{}}
	fix := mkGitRepo(t, "fix-2")

	if adv := a.advisoryFor("run_command", runArgs(t, "gh pr list"), fix); adv != "" {
		t.Fatalf("non-build command must not fire, got %q", adv)
	}
	if adv := a.advisoryFor("read_file", []byte(`{"path":"x"}`), fix); adv != "" {
		t.Fatal("non-run_command tool must not fire")
	}
	if adv := a.advisoryFor("run_command", runArgs(t, "go build ./..."), t.TempDir()); adv != "" {
		t.Fatal("non-git dir must stay silent")
	}
}
