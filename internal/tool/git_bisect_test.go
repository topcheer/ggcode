package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// initBisectRepo creates a linear 5-commit history (c1..c5) in a temp dir
// where commit c3 introduces a "broken.txt" marker file. Returns the dir and
// the short hash of c3.
func initBisectRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	commit := func(name string, broken bool) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if broken {
			if err := os.WriteFile(filepath.Join(dir, "broken.txt"), []byte("bug\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", "-A")
		run("commit", "-q", "-m", name)
	}
	commit("c1", false)
	commit("c2", false)
	commit("c3", true)
	commit("c4", false)
	commit("c5", false)

	cmd := exec.Command("git", "log", "--format=%h", "--grep=c3", "-1")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("resolve c3 hash: %v", err)
	}
	return dir, strings.TrimSpace(string(out))
}

func callBisect(t *testing.T, dir string, args map[string]any) Result {
	t.Helper()
	args["description"] = "bisect test"
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := GitBisect{WorkingDir: dir}.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	return res
}

func TestGitBisectStepwiseFindsFirstBadCommit(t *testing.T) {
	dir, c3 := initBisectRepo(t)

	res := callBisect(t, dir, map[string]any{"action": "start", "bad": "HEAD", "good": "HEAD~4"})
	if res.IsError {
		t.Fatalf("start failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Current checkout:") {
		t.Fatalf("start result missing checkout line: %s", res.Content)
	}

	found := false
	for i := 0; i < 10; i++ {
		action := "good"
		if _, err := os.Stat(filepath.Join(dir, "broken.txt")); err == nil {
			action = "bad"
		}
		res = callBisect(t, dir, map[string]any{"action": action})
		if strings.Contains(res.Content, "is the first") && strings.Contains(res.Content, "bad") {
			found = true
			break
		}
		if strings.Contains(res.Content, "is the first") && strings.Contains(res.Content, "good") {
			t.Fatalf("unexpected first-good verdict: %s", res.Content)
		}
		if !strings.Contains(res.Content, "Current checkout:") {
			t.Fatalf("step result missing checkout line: %s", res.Content)
		}
	}
	if !found {
		t.Fatalf("bisection did not converge: %s", res.Content)
	}
	if !strings.Contains(res.Content, c3) {
		t.Fatalf("verdict does not name c3 (%s): %s", c3, res.Content)
	}
	if !strings.Contains(res.Content, "action=reset") {
		t.Fatalf("verdict missing reset hint: %s", res.Content)
	}

	// Reset must restore the original HEAD (c5).
	res = callBisect(t, dir, map[string]any{"action": "reset"})
	if res.IsError {
		t.Fatalf("reset failed: %s", res.Content)
	}
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("log after reset: %v", err)
	}
	if strings.TrimSpace(string(out)) != "c5" {
		t.Fatalf("after reset HEAD = %q, want c5", strings.TrimSpace(string(out)))
	}
}

func TestGitBisectRunModeScripted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c scripted run not available on windows")
	}
	dir, c3 := initBisectRepo(t)

	if res := callBisect(t, dir, map[string]any{"action": "start", "bad": "HEAD", "good": "HEAD~4"}); res.IsError {
		t.Fatalf("start failed: %s", res.Content)
	}
	// Exit 0 (good) exactly when broken.txt is absent.
	res := callBisect(t, dir, map[string]any{"action": "run", "command": "test ! -f broken.txt", "timeout_seconds": 60})
	if res.IsError {
		t.Fatalf("run failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "is the first") {
		t.Fatalf("run did not converge: %s", res.Content)
	}
	if !strings.Contains(res.Content, c3) {
		t.Fatalf("run verdict does not name c3 (%s): %s", c3, res.Content)
	}
	if res := callBisect(t, dir, map[string]any{"action": "reset"}); res.IsError {
		t.Fatalf("reset failed: %s", res.Content)
	}
}

func TestGitBisectStartRequiresGood(t *testing.T) {
	dir, _ := initBisectRepo(t)
	res := callBisect(t, dir, map[string]any{"action": "start"})
	if !res.IsError {
		t.Fatalf("start without good should error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "good is required") {
		t.Fatalf("unexpected error text: %s", res.Content)
	}
}

func TestGitBisectRejectsLeadingDashRefs(t *testing.T) {
	dir, _ := initBisectRepo(t)
	res := callBisect(t, dir, map[string]any{"action": "start", "bad": "HEAD", "good": "-exec"})
	if !res.IsError {
		t.Fatalf("dash-prefixed good ref should be rejected: %s", res.Content)
	}
	if !strings.Contains(res.Content, "option injection") {
		t.Fatalf("unexpected error text: %s", res.Content)
	}
	res = callBisect(t, dir, map[string]any{"action": "bad", "commit": "--hard"})
	if !res.IsError || !strings.Contains(res.Content, "option injection") {
		t.Fatalf("dash-prefixed commit should be rejected: %s", res.Content)
	}
}

func TestGitBisectStatusWhenNotBisecting(t *testing.T) {
	dir, _ := initBisectRepo(t)
	res := callBisect(t, dir, map[string]any{"action": "status"})
	if res.IsError {
		t.Fatalf("status outside bisection should be friendly, got error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No bisection in progress") {
		t.Fatalf("unexpected status text: %s", res.Content)
	}
}

func TestGitBisectStepOutsideSession(t *testing.T) {
	dir, _ := initBisectRepo(t)
	res := callBisect(t, dir, map[string]any{"action": "good"})
	if !res.IsError {
		t.Fatalf("good outside bisection should error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "No bisection in progress") {
		t.Fatalf("unexpected error text: %s", res.Content)
	}
}

func TestGitBisectRunRequiresCommand(t *testing.T) {
	dir, _ := initBisectRepo(t)
	res := callBisect(t, dir, map[string]any{"action": "run"})
	if !res.IsError {
		t.Fatalf("run without command should error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "command is required") {
		t.Fatalf("unexpected error text: %s", res.Content)
	}
}

func TestGitBisectUnsupportedAction(t *testing.T) {
	dir, _ := initBisectRepo(t)
	res := callBisect(t, dir, map[string]any{"action": "rebase"})
	if !res.IsError {
		t.Fatalf("unsupported action should error: %s", res.Content)
	}
}
