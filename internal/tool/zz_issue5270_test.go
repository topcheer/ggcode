package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// issue5270RunGuard executes scripts/ci/stale-base-guard.sh in detection
// mode (no GITHUB_TOKEN) inside dir and returns its combined output.
func issue5270RunGuard(t *testing.T, dir, base, head string, extraEnv []string) string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci", "stale-base-guard.sh"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("stale-base-guard.sh missing: %v", err)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Env = append(cmd.Env, "BASE_REF="+base, "HEAD_REF="+head, "GITHUB_TOKEN=", "PR_NUMBER=")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("stale-base-guard.sh: %v\n%s", err, out.String())
	}
	return out.String()
}

// TestIssue5270GuardScriptBehavior pins the dual-signal sequencing of the
// create-time guard (#5270) against real git fixtures:
//   - escalate: replayed commits detected via patch-id equivalence;
//   - warn: behind-count alone above the threshold;
//   - clean: fresh branch, or behind-count under an overridden threshold.
func TestIssue5270GuardScriptBehavior(t *testing.T) {
	dir := prDeltaInitRepo(t)
	b1 := prDeltaCommit(t, dir, "b1.txt", "b1\n", "base one")
	b2 := prDeltaCommit(t, dir, "b2.txt", "b2\n", "base two")
	b3 := prDeltaCommit(t, dir, "b3.txt", "b3\n", "base three")
	prDeltaCommit(t, dir, "b4.txt", "b4\n", "base four")
	prDeltaCommit(t, dir, "b5.txt", "b5\n", "base five")

	// Stale branch: forks after b1 (4 behind), cherry-picks b2/b3
	// (replayed work under different shas) plus one real commit.
	prDeltaGit(t, dir, "checkout", "-b", "pr-stale", b1)
	prDeltaCherryPick(t, dir, b2)
	prDeltaCherryPick(t, dir, b3)
	prDeltaCommit(t, dir, "real.txt", "real work\n", "real change")

	got := issue5270RunGuard(t, dir, "main", "pr-stale", nil)
	if !strings.Contains(got, "verdict=escalate") {
		t.Fatalf("replayed-work case: want verdict=escalate, got:\n%s", got)
	}
	if !strings.Contains(got, "behind=4") || !strings.Contains(got, "absorbed=2") || !strings.Contains(got, "pr_commits=3") {
		t.Fatalf("replayed-work case: want behind=4 absorbed=2 pr_commits=3, got:\n%s", got)
	}
	if !strings.Contains(got, "2 of 3 commits appear already merged upstream; rebase recommended") {
		t.Fatalf("replayed-work case: issue wording missing, got:\n%s", got)
	}
	if !strings.Contains(got, "base two") || !strings.Contains(got, "base three") {
		t.Fatalf("replayed-work case: replayed commit list missing, got:\n%s", got)
	}
	if !strings.Contains(got, "<!-- stale-base-guard -->") {
		t.Fatalf("replayed-work case: comment marker missing, got:\n%s", got)
	}

	// Behind-only branch: 4 behind, no replayed commits -> warn, no
	// escalated wording (patch-ids must not match).
	prDeltaGit(t, dir, "checkout", "-b", "pr-behind", b1)
	prDeltaCommit(t, dir, "n1.txt", "n1\n", "new one")
	got = issue5270RunGuard(t, dir, "main", "pr-behind", nil)
	if !strings.Contains(got, "verdict=warn") {
		t.Fatalf("behind-only case: want verdict=warn, got:\n%s", got)
	}
	if !strings.Contains(got, "4 commits behind") {
		t.Fatalf("behind-only case: want behind-count wording, got:\n%s", got)
	}
	if strings.Contains(got, "already merged upstream") {
		t.Fatalf("behind-only case: escalated wording must not fire without patch-id matches, got:\n%s", got)
	}

	// Fresh branch off the base tip -> clean (and stays silent in
	// comment mode; the script prints the resolved body in detect mode).
	prDeltaGit(t, dir, "checkout", "-b", "pr-fresh", "main")
	prDeltaCommit(t, dir, "f1.txt", "f1\n", "fresh one")
	got = issue5270RunGuard(t, dir, "main", "pr-fresh", nil)
	if !strings.Contains(got, "verdict=clean") {
		t.Fatalf("fresh-branch case: want verdict=clean, got:\n%s", got)
	}

	// Threshold is configurable: the same 4-behind branch is clean when
	// the threshold is raised to 10.
	got = issue5270RunGuard(t, dir, "main", "pr-behind", []string{"BEHIND_THRESHOLD=10"})
	if !strings.Contains(got, "verdict=clean") {
		t.Fatalf("threshold-override case: want verdict=clean with threshold 10, got:\n%s", got)
	}
}

// TestIssue5270GuardSurfaces pins the workflow trigger surface, the
// non-blocking posture, and the detection method anchors.
func TestIssue5270GuardSurfaces(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "stale-base-guard.yml"))
	if err != nil {
		t.Fatalf("stale-base-guard.yml missing: %v", err)
	}
	workflow := string(wf)
	for _, want := range []string{
		"pull_request:",
		"opened",
		"synchronize",
		"continue-on-error: true",
		"pull-requests: write",
		"stale-base-guard.sh",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("workflow missing anchor %q", want)
		}
	}

	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", "stale-base-guard.sh"))
	if err != nil {
		t.Fatalf("stale-base-guard.sh missing: %v", err)
	}
	guard := string(script)
	for _, want := range []string{
		"rev-list --count",
		"patch-id --stable",
		"--no-merges",
		"merge-base",
		"already merged upstream; rebase recommended",
		"<!-- stale-base-guard -->",
	} {
		if !strings.Contains(guard, want) {
			t.Fatalf("guard script missing anchor %q", want)
		}
	}
}
