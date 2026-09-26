package agent

import (
	"os/exec"
	"strings"
	"testing"
)

// newAutoCommitAgent arms both the commit gate and auto-commit on a fresh
// git repo (reuses newCommitHintAgent's helpers from zz_issue698_test.go).
func newAutoCommitAgent(t *testing.T) *Agent {
	t.Helper()
	a := newCommitHintAgent(t)
	a.autoCommit = true
	return a
}

// gitHeadSummary returns "hash subject" of HEAD plus the files it touched.
func gitHeadSummary(t *testing.T, dir string) (string, string) {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--name-only", "--pretty=%h %s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("empty git log: %q", string(out))
	}
	files := ""
	if len(lines) > 1 {
		files = strings.TrimSpace(lines[1])
	}
	return lines[0], files
}

func TestAutoCommit_CommitsAttributableEditsOnly(t *testing.T) {
	a := newAutoCommitAgent(t)
	dir := a.workingDir

	// Pre-existing user dirt that must NOT be swept into the auto-commit.
	writeFileT(t, dir+"/user_tracked.go", "user change")
	runGit(t, dir, "add", "user_tracked.go")
	writeFileT(t, dir+"/user_untracked.txt", "user file")

	// The agent's edit.
	agentFile := dir + "/agent_edit.go"
	writeFileT(t, agentFile, "package main\n")

	stats := &RunStats{
		FilesEdited: []string{agentFile},
		ToolCalls:   map[string]int{"edit_file": 1},
		UserPrompt:  "fix the parser bug",
	}

	gate := a.runCommitGate(stats)
	if gate.hint == "" || len(gate.files) != 1 {
		t.Fatalf("gate must attribute exactly the agent edit, got hint=%q files=%v", gate.hint, gate.files)
	}

	note := a.tryAutoCommit(stats, gate.files)
	if note == "" {
		t.Fatal("auto-commit must succeed in a clean repo")
	}
	if !strings.Contains(note, "agent_edit.go") {
		t.Fatalf("note should list the committed file, got %q", note)
	}

	head, files := gitHeadSummary(t, dir)
	if !strings.Contains(head, "fix the parser bug") {
		t.Fatalf("commit subject should derive from the user prompt, got %q", head)
	}
	if files != "agent_edit.go" {
		t.Fatalf("commit must contain exactly the attributable file, got %q", files)
	}

	body, err := exec.Command("git", "-C", dir, "log", "-1", "--pretty=%b").CombinedOutput()
	if err != nil || !strings.Contains(string(body), "Co-Authored-By: ggcode") {
		t.Fatalf("commit body must carry ggcode attribution, got %q err=%v", string(body), err)
	}
}

func TestAutoCommit_DisabledNoOps(t *testing.T) {
	a := newCommitHintAgent(t) // autoCommit stays false
	dir := a.workingDir
	agentFile := dir + "/agent_edit.go"
	writeFileT(t, agentFile, "x")
	stats := &RunStats{FilesEdited: []string{agentFile}, ToolCalls: map[string]int{"edit_file": 1}}
	if note := a.tryAutoCommit(stats, []string{agentFile}); note != "" {
		t.Fatalf("disabled auto-commit must no-op, got %q", note)
	}
	status, err := gitStatusPorcelain(dir)
	if err != nil || status == "" {
		t.Fatalf("tree must stay dirty when disabled, status=%q err=%v", status, err)
	}
}

func TestAutoCommit_FailsGracefullyOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{workingDir: dir, autoCommit: true}
	if note := a.tryAutoCommit(&RunStats{}, []string{dir + "/f.txt"}); note != "" {
		t.Fatalf("expected empty note outside a repo, got %q", note)
	}
}

func TestAutoCommit_HookFailureFallsBack(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	// A failing pre-commit hook makes `git commit` fail deterministically
	// while `git add` still succeeds — the exact fallback path.
	writeFileT(t, dir+"/.git/hooks/pre-commit", "#!/bin/sh\nexit 1\n")
	if out, err := exec.Command("chmod", "+x", dir+"/.git/hooks/pre-commit").CombinedOutput(); err != nil {
		t.Fatalf("chmod: %v\n%s", err, out)
	}
	a := &Agent{workingDir: dir, autoCommit: true}
	f := dir + "/f.go"
	writeFileT(t, f, "x")
	if note := a.tryAutoCommit(&RunStats{}, []string{f}); note != "" {
		t.Fatalf("commit failure must fall back to empty note, got %q", note)
	}
}

func TestAutoCommitSubject(t *testing.T) {
	cases := []struct {
		prompt, want string
	}{
		{"", "ggcode: apply requested changes"},
		{"fix the bug", "fix the bug"},
		{"first line\nsecond line", "first line"},
		{"use `goolm` tags here", "use 'goolm' tags here"},
	}
	for _, c := range cases {
		if got := autoCommitSubject(&RunStats{UserPrompt: c.prompt}); got != c.want {
			t.Errorf("prompt %q: got %q want %q", c.prompt, got, c.want)
		}
	}
	long := strings.Repeat("a", 100)
	got := autoCommitSubject(&RunStats{UserPrompt: long})
	if len([]rune(got)) != autoCommitSubjectMax+3 || !strings.HasSuffix(got, "...") {
		t.Errorf("long subject not truncated: len=%d got=%q", len([]rune(got)), got)
	}
}
