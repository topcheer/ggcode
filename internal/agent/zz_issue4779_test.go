package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4779 (S-07): after a push to a review-gated branch, the toolchain must
// warn inline when the earlier APPROVED review went stale, instead of the
// merge refusal being the first signal. gh is faked via PATH injection so
// the full gate path (push receipt -> PR lookup -> decision query) runs.
func zz4779FakeGh(t *testing.T, prListOut, viewOut string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"\"pr list\") printf '%s\\n' '" + prListOut + "' ;;\n" +
		"\"pr view\") printf '%s\\n' '" + viewOut + "' ;;\n" +
		"*) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func zz4779Agent(t *testing.T, wd string) *Agent {
	t.Helper()
	a := &Agent{}
	a.staleReview = newStaleReviewState()
	// WorkingDir without a full ctor: reuse the exported field path used by
	// tests in this package; fall back to a temp dir when unset.
	a.workingDir = wd
	return a
}

const zz4779PushReceipt = "Enumerating objects: 5, done.\n" +
	"   1a2b3c4..5d6e7f8  fix/issue-4779 -> origin/fix/issue-4779\n" +
	"To github.com:topcheer/ggcode.git\n"

func TestIssue4779_StaleApprovalWarns(t *testing.T) {
	wd := zz4779FakeGh(t, "4899", "REVIEW_REQUIRED|APPROVED")
	a := zz4779Agent(t, wd)
	args := []byte(`{"command":"git push -u origin fix/issue-4779"}`)
	got := a.checkPushReviewStaleness("run_command", args, zz4779PushReceipt)
	if got == "" {
		t.Fatal("stale approval must warn inline")
	}
	for _, want := range []string{"#4779", "PR #4899", "stale/dismissed", "re-approves"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning missing %q: %s", want, got)
		}
	}
	// Once-per-branch cap: a second push does not nag again.
	if again := a.checkPushReviewStaleness("run_command", args, zz4779PushReceipt); again != "" {
		t.Errorf("second push on same branch must stay silent, got: %s", again)
	}
}

func TestIssue4779_FreshApprovalSilent(t *testing.T) {
	wd := zz4779FakeGh(t, "4899", "APPROVED|APPROVED")
	a := zz4779Agent(t, wd)
	args := []byte(`{"command":"git push -u origin fix/issue-4779"}`)
	if got := a.checkPushReviewStaleness("run_command", args, zz4779PushReceipt); got != "" {
		t.Errorf("fresh approval must stay silent, got: %s", got)
	}
}

func TestIssue4779_NoPRAndNonPushSilent(t *testing.T) {
	wd := zz4779FakeGh(t, "", "") // gh answers empty: no open PR
	a := zz4779Agent(t, wd)
	if got := a.checkPushReviewStaleness("run_command", []byte(`{"command":"git push -u origin fix/issue-4779"}`), zz4779PushReceipt); got != "" {
		t.Errorf("no-PR push must stay silent, got: %s", got)
	}
	// Non-push command with push-shaped output: silent (shape gate on command).
	if got := a.checkPushReviewStaleness("run_command", []byte(`{"command":"git status"}`), zz4779PushReceipt); got != "" {
		t.Errorf("non-push command must stay silent, got: %s", got)
	}
	// Failed push (no ref-update receipt): silent.
	if got := a.checkPushReviewStaleness("run_command", []byte(`{"command":"git push -u origin fix/issue-4779"}`), "error: fetch failed"); got != "" {
		t.Errorf("failed push must stay silent, got: %s", got)
	}
}
