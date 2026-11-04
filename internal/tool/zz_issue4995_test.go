package tool

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #4995: `git worktree add` without -b leaves the new worktree on a
// detached HEAD when the target is a commit-ish (SHA, remote-tracking ref,
// tag, refs/heads/<branch>); the failure only surfaces at push time
// ("destination ... not a full refname"), costing a recovery round-trip.
// Guards (tool layer, probe-grounded):
//   - creation-time advisory: after a raw `git worktree add` succeeds
//     (run_command sync path AND background job completion), probe
//     two-positional forms and name the detached worktrees in the result
//   - push-time hint: a failed `git push` from a detached HEAD (sync
//     failure message OR background job failure) gets the verified
//     recovery recipe (switch -c + push, or HEAD:refs/heads/<b>)
//
// Empirical ground truth recorded 2026-11-04 on git 2.56 (see
// TestIssue4995_WorktreeAddAdvisoryOnFullyQualifiedLocalRef):
//   detaches: SHA, origin/x, refs/remotes/*, refs/tags/*, refs/heads/*, tags,
//             rev expressions (HEAD~1, x^0)
//   attaches: short local branch name, path-only form, -b/-B forms

// The classifier feeds a PROBE, not a verdict: every two-positional form
// without -b/-B/--detach is probe-worthy (the probe decides attached vs
// detached on the real checkout).
func TestIssue4995_EveryExplicitTargetIsProbeWorthy(t *testing.T) {
	for _, cmd := range []string{
		"git worktree add /tmp/x main",            // short local branch
		"git worktree add /tmp/x refs/heads/main", // fully-qualified local ref
		"git worktree add /tmp/x v1.0",            // tag name
		"git worktree add /tmp/x origin/main",     // remote-tracking
		"git worktree add /tmp/x 82f35bd",         // SHA
	} {
		if got := detectWorktreeAddSegments(cmd); len(got) == 0 {
			t.Errorf("detectWorktreeAddSegments(%q) = none, want >=1 (probe-worthy)", cmd)
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

// git 2.56 empirical correction: `git worktree add <path> refs/heads/<b>`
// checks out DETACHED (only the short-name DWIM form attaches). The
// classifier must probe this shape and the advisory must fire.
func TestIssue4995_WorktreeAddAdvisoryOnFullyQualifiedLocalRef(t *testing.T) {
	dir := initTestGitRepo(t)
	branch := gitCurrentBranch(t, dir)

	rc := RunCommand{WorkingDir: dir}
	res, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "git worktree add " + filepath.Join(dir, "wt-fq") + " refs/heads/" + branch,
		"description": "create worktree from fully-qualified local ref",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("worktree add failed (branch %q may be checked out elsewhere; fixture bug): %s", branch, res.Content)
	}
	if !strings.Contains(res.Content, worktreeSafetyMarker) {
		t.Errorf("expected detached advisory for refs/heads/%s target, got:\n%s", branch, res.Content)
	}
}

// A bare tag name (no refs/tags/ prefix) also detaches; classifier marks it
// ambiguous (could be a branch), the probe must still catch it.
func TestIssue4995_WorktreeAddAdvisoryOnTagName(t *testing.T) {
	dir := initTestGitRepo(t)
	gitIn(t, dir, "tag", "v1.0")

	rc := RunCommand{WorkingDir: dir}
	res, err := rc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command":     "git worktree add " + filepath.Join(dir, "wt-tag") + " v1.0",
		"description": "create worktree from tag",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("worktree add failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, worktreeSafetyMarker) {
		t.Errorf("expected detached advisory for tag target, got:\n%s", res.Content)
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
		"git worktree add /tmp/x",               // path only
		"git worktree add -b wt /tmp/x HEAD",    // -b
		"git worktree add -B wt /tmp/x HEAD",    // -B
		"git worktree add --detach /tmp/x HEAD", // explicit detach
		"git status",
		"go test ./...",
	}
	// These forms ATTACH but are still probe-worthy (probe, not verdict):
	for _, cmd := range []string{
		"git worktree add /tmp/x topic",           // short local branch - probe, stays silent when attached
		"git worktree add /tmp/x refs/heads/main", // fully-qualified - DETACHES on git 2.56, must probe
	} {
		if got := detectWorktreeAddSegments(cmd); len(got) != 1 {
			t.Errorf("detectWorktreeAddSegments(%q) = %v, want 1 (probe-worthy)", cmd, got)
		}
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

func gitCurrentBranch(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("branch --show-current: %v", err)
	}
	b := strings.TrimSpace(string(out))
	if b == "" {
		t.Fatal("no current branch (detached fixture repo?)")
	}
	return b
}

func firstCommandJobID(mgr *CommandJobManager) string {
	for _, s := range mgr.List() {
		return s.ID
	}
	return ""
}

// --- background job coverage (start_command/wait_command path; replaces
// the #4996 agent-layer hook for start_command) ---

func TestIssue4995_BackgroundJobWorktreeAddAdvisory(t *testing.T) {
	dir := initTestGitRepo(t)
	sha := gitRevParseHead(t, dir)

	mgr := NewCommandJobManager(dir)
	defer mgr.ShutdownAll(2 * time.Second)
	sc := StartCommandTool{Manager: mgr}
	res, err := sc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command": "git worktree add " + filepath.Join(dir, "wt-bg") + " " + sha,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("start_command failed: %s", res.Content)
	}
	// Wait for job completion, then read the snapshot back: the advisory
	// must surface via the finished-job snapshot (wait_command/read path).
	jobID := firstCommandJobID(mgr)
	if jobID == "" {
		t.Fatal("no job id found after start_command")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		snap, rerr := mgr.Read(jobID, 20, 0)
		if rerr != nil {
			t.Fatalf("read job: %v", rerr)
		}
		if snap.Status == CommandJobCompleted || snap.Status == CommandJobFailed {
			if !strings.Contains(snap.SafetyHint, worktreeSafetyMarker) || !strings.Contains(snap.SafetyHint, "wt-bg") {
				t.Fatalf("expected detached advisory in finished snapshot SafetyHint, got: %q (status %s)", snap.SafetyHint, snap.Status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish in 15s (status %s)", snap.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestIssue4995_BackgroundJobPushFailureHint(t *testing.T) {
	dir := initTestGitRepo(t)
	bare := filepath.Join(dir, "origin.git")
	gitIn(t, dir, "init", "--bare", bare)
	gitIn(t, dir, "remote", "add", "origin", bare)
	gitIn(t, dir, "worktree", "add", "--detach", filepath.Join(dir, "wt-bgpush"), "HEAD")
	wt := filepath.Join(dir, "wt-bgpush")
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

	mgr := NewCommandJobManager(wt)
	defer mgr.ShutdownAll(2 * time.Second)
	sc := StartCommandTool{Manager: mgr}
	res, err := sc.Execute(context.Background(), mustJSON(t, map[string]any{
		"command": "git push origin HEAD",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("start_command failed: %s", res.Content)
	}
	jobID := firstCommandJobID(mgr)
	if jobID == "" {
		t.Fatal("no job id found after start_command")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		snap, rerr := mgr.Read(jobID, 20, 0)
		if rerr != nil {
			t.Fatalf("read job: %v", rerr)
		}
		if snap.Status == CommandJobFailed {
			if !strings.Contains(snap.SafetyHint, worktreeSafetyMarker) || !strings.Contains(snap.SafetyHint, "switch -c") {
				t.Fatalf("expected push-recovery hint in finished snapshot SafetyHint, got: %q", snap.SafetyHint)
			}
			// The formatted output must surface it too.
			if out := formatCommandJobSnapshot(snap, true); !strings.Contains(out, worktreeSafetyMarker) {
				t.Fatalf("formatCommandJobSnapshot must surface SafetyHint, got:\n%s", out)
			}
			break
		}
		if snap.Status == CommandJobCompleted {
			t.Fatal("push unexpectedly succeeded (fixture broken: bare origin accepted HEAD?)")
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish in 15s (status %s)", snap.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
