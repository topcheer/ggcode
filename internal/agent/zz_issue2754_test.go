package agent

// Issue #2754 regression tests: bare `test` (the POSIX shell builtin for
// conditionals like `test -f x`) must NOT count as a test run. It used to
// flip testsRan via `case "test", "pytest"` and silently disarm the
// git_commit/git push reversibility gate - the same false-verification
// family as #2255 ("build:" in a commit message) and #2552 (`make clean`
// counted as build).

import "testing"

func TestIssue2754TestBuiltinDoesNotDisarmGate(t *testing.T) {
	r := newReversibilityState()

	// A conditional check on a build artifact - not a test run.
	r.recordSafetySignal("run_command", "# check build artifact exists\ntest -f /tmp/ggcode || echo missing")
	if r.testsRan {
		t.Fatal("shell builtin `test -f` must NOT set testsRan (#2754)")
	}

	// The commit gate must still be armed.
	if got := r.checkPreAction("git_commit", `{"message":"wip"}`); got == "" {
		t.Fatal("git_commit warning must still fire after a bare `test` conditional (#2754)")
	}
	// The push gate must still be armed.
	if got := r.checkPreAction("run_command", "git push origin main"); got == "" {
		t.Fatal("git push warning must still fire after a bare `test` conditional (#2754)")
	}
}

func TestIssue2754DestructivePrecheckDoesNotDisarmGate(t *testing.T) {
	// `test -d dist && rm -rf dist` is a destructive pre-cleanup guarded by
	// a conditional - counting it as "verified" is the worst-case miss.
	r := newReversibilityState()
	r.recordSafetySignal("run_command", `{"command":"test -d dist && rm -rf dist"}`)
	if r.testsRan {
		t.Fatal("JSON-enveloped `test -d && rm -rf` must NOT set testsRan (#2754)")
	}
	if got := r.checkPreAction("run_command", "git push origin main"); got == "" {
		t.Fatal("git push warning must still fire after `test -d && rm -rf` (#2754)")
	}
}

func TestIssue2754BareTestRunnersStillCount(t *testing.T) {
	// The #1194 motivation - bare pytest with no `test` token following -
	// plus the common bare runners must keep counting.
	for _, args := range []string{
		"pytest -q scripts/",
		"py.test tests/",
		"vitest run",
		"jest --ci",
	} {
		r := newReversibilityState()
		r.recordSafetySignal("run_command", args)
		if !r.testsRan {
			t.Errorf("bare test runner %q must still set testsRan (#2754/#1194)", args)
		}
	}
}
