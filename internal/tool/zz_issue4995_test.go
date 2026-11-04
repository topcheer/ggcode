package tool

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #4995: `git worktree add` without -b leaves the new worktree on a
// detached HEAD when the target is a commit-ish (SHA, remote-tracking ref);
// the failure only surfaces at push time ("destination ... not a full
// refname"), costing a recovery round-trip. Two guards:
//   - creation-time advisory: after a raw `git worktree add` succeeds, probe
//     detach-prone targets and name the detached worktrees in the result
//   - push-time hint: a failed `git push` from a detached HEAD gets the
//     verified recovery recipe (switch -c + push, or HEAD:refs/heads/<b>)

// --- target-shape classification ---

func TestIssue4995_DetachingTargetShapes(t *testing.T) {
	cases := []struct {
		target string
		want   bool
	}{
		{"refs/heads/main", false}, // fully-qualified local branch attaches
		{"main", false},            // bare local branch name attaches
		{"origin/main", true},      // remote-tracking ref detaches
		{"refs/remotes/origin/main", true},
		{"refs/tags/v1.0", true},
		{"82f35bd", true},                                  // abbreviated SHA
		{"82f35bdabc123def456abc123def456abc123def", true}, // full SHA
		{"HEAD~1", true},                                   // rev expression
		{"main^0", true},
	}
	for _, c := range cases {
		if got := isDetachingWorktreeTarget(c.target); got != c.want {
			t.Errorf("isDetachingWorktreeTarget(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}

// --- creation-time advisory wiring ---

func TestIssue4995_WorktreeAddAdvisoryOnDetachedSHA(t *testing.T) {
	dir := initTestGitRepo(t)
	sha := gitRevParseHead(t, dir)

	rc := RunCommand{WorkingDir: dir}
	res, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "# probe\n git worktree add " + filepath.Join(dir, "wt-sha") + " " + sha,
		"description": "create worktree from SHA",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("worktree add failed: %s", res.Content)
	}
	for _, want := range []string{worktreeSafetyMarker, "detached HEAD", "wt-sha", "switch -c", "HEAD:refs/heads"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("advisory missing %q in result:\n%s", want, res.Content)
		}
	}
}

func TestIssue4995_WorktreeAddAdvisoryOnRemoteRef(t *testing.T) {
	dir := initTestGitRepo(t)
	gitIn(t, dir, "update-ref", "refs/remotes/origin/main", gitRevParseHead(t, dir))

	rc := RunCommand{WorkingDir: dir}
	res, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "git worktree add " + filepath.Join(dir, "wt-origin") + " origin/main",
		"description": "create worktree from remote ref",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("worktree add failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, worktreeSafetyMarker) || !strings.Contains(res.Content, "wt-origin") {
		t.Errorf("expected detached advisory for origin/main target, got:\n%s", res.Content)
	}
}

func TestIssue4995_NoAdvisoryForSafeForms(t *testing.T) {
	dir := initTestGitRepo(t)
	head := gitRevParseHead(t, dir)

	// git worktree add <path> <local-branch> attaches; no advisory.
	rc := RunCommand{WorkingDir: dir}
	res, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "git branch topic && git worktree add " + filepath.Join(dir, "wt-branch") + " topic",
		"description": "create worktree on local branch",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("worktree add failed: %s", res.Content)
	}
	if strings.Contains(res.Content, worktreeSafetyMarker) {
		t.Errorf("attached-branch worktree must not trigger advisory:\n%s", res.Content)
	}

	// git worktree add <path> (no target) checks out current branch: safe.
	res2, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "git worktree add " + filepath.Join(dir, "wt-plain"),
		"description": "create plain worktree",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res2.IsError {
		t.Fatalf("worktree add failed: %s", res2.Content)
	}
	if strings.Contains(res2.Content, worktreeSafetyMarker) {
		t.Errorf("default worktree must not trigger advisory:\n%s", res2.Content)
	}

	// -b form (what enter_worktree already does) is safe by construction.
	res3, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "git worktree add -b wt-b " + filepath.Join(dir, "wt-b-dir") + " " + head,
		"description": "create worktree with -b",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res3.IsError {
		t.Fatalf("worktree add -b failed: %s", res3.Content)
	}
	if strings.Contains(res3.Content, worktreeSafetyMarker) {
		t.Errorf("-b worktree must not trigger advisory:\n%s", res3.Content)
	}
}

// Explicit --detach is a deliberate read-only review pattern (watchdog
// guidance): never flagged.
func TestIssue4995_NoAdvisoryForExplicitDetach(t *testing.T) {
	dir := initTestGitRepo(t)
	rc := RunCommand{WorkingDir: dir}
	res, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "git worktree add --detach " + filepath.Join(dir, "wt-detach") + " HEAD",
		"description": "create detached review worktree",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("worktree add --detach failed: %s", res.Content)
	}
	if strings.Contains(res.Content, worktreeSafetyMarker) {
		t.Errorf("explicit --detach must not trigger advisory:\n%s", res.Content)
	}
}

// --- push-time hint wiring ---

func TestIssue4995_PushFailureHintFromDetachedHead(t *testing.T) {
	dir := initTestGitRepo(t)
	// Bare origin so `git push origin HEAD` reaches the refspec-resolution
	// failure (the detached-HEAD fingerprint) instead of dying on a missing
	// remote.
	bare := filepath.Join(dir, "origin.git")
	gitIn(t, dir, "init", "--bare", bare)
	gitIn(t, dir, "remote", "add", "origin", bare)
	// Create a detached worktree, commit on it, then push HEAD from inside
	// it via WorkingDir - the exact session A/B failure chain.
	gitIn(t, dir, "worktree", "add", "--detach", filepath.Join(dir, "wt-push"), "HEAD")
	wt := filepath.Join(dir, "wt-push")
	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = wt
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %s: %v", name, args, string(out), err)
		}
	}
	run("sh", "-c", "echo work > f.txt")
	run("git", "add", "f.txt")
	run("git", "commit", "-m", "work")

	rc := RunCommand{WorkingDir: wt}
	res, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "git push origin HEAD",
		"description": "push from detached worktree",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected push failure, got success:\n%s", res.Content)
	}
	for _, want := range []string{worktreeSafetyMarker, "detached HEAD", "switch -c", "HEAD:refs/heads"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("push hint missing %q:\n%s", want, res.Content)
		}
	}
}

// The hint must NOT fire when the push failure has the marker phrase but
// HEAD is attached (e.g. an abbreviated-ref push from a branch checkout
// with an odd remote): the verified probe keeps the hint truthful.
func TestIssue4995_NoPushHintWhenAttached(t *testing.T) {
	dir := initTestGitRepo(t)
	// The helper must stay silent on an attached HEAD even when handed the
	// marker text directly: the filesystem probe keeps the hint truthful.
	if hint := detectDetachedWorktreePush(context.Background(),
		"git push origin HEAD",
		"",
		"error: The destination you provided is not a full refname (i.e., starting with \"refs/\"). We tried to guess what you meant by:",
		dir); hint != "" {
		t.Errorf("expected no hint on attached HEAD, got:\n%s", hint)
	}
}

// --- unit-level parser coverage ---

func TestIssue4995_ParserShapes(t *testing.T) {
	prone := []string{
		"git worktree add /tmp/x 82f35bd",
		"git worktree add /tmp/x origin/main",
		"git -c advice.detachedHead=false worktree add /tmp/x 82f35bd",
		"GIT_PAGER=cat git worktree add /tmp/x refs/remotes/origin/main",
		"git worktree add --checkout /tmp/x HEAD~1",
		"git worktree add /tmp/x 82f35bd && echo done",
		"cd repo; git worktree add /tmp/x origin/main",
	}
	for _, cmd := range prone {
		if got := detectWorktreeAddSegments(cmd); len(got) == 0 {
			t.Errorf("detectWorktreeAddSegments(%q) = none, want >=1", cmd)
		}
	}
	safe := []string{
		"git worktree add /tmp/x",                 // path only
		"git worktree add /tmp/x topic",           // local branch
		"git worktree add -b wt /tmp/x HEAD",      // -b
		"git worktree add -B wt /tmp/x HEAD",      // -B
		"git worktree add --detach /tmp/x HEAD",   // explicit detach
		"git worktree add /tmp/x refs/heads/main", // qualified local branch
		"git status",
		"go test ./...",
	}
	for _, cmd := range safe {
		if got := detectWorktreeAddSegments(cmd); len(got) != 0 {
			t.Errorf("detectWorktreeAddSegments(%q) = %v, want none", cmd, got)
		}
	}
	if !isGitPushSegment("git push origin HEAD") || !isGitPushSegment("cd x && git push") {
		t.Error("isGitPushSegment missed push segments")
	}
	if isGitPushSegment("git status") {
		t.Error("isGitPushSegment false positive")
	}
}

// --- helpers ---

func gitRevParseHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}
