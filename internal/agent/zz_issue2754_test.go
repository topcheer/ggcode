package agent

import "testing"

func TestIssue2754TestBuiltinDoesNotDisarmGate(t *testing.T) {
	r := newReversibilityState()

	r.recordSafetySignal("run_command", "# check build artifact exists\ntest -f /tmp/ggcode || echo missing")
	if r.testsRan {
		t.Fatal("shell builtin `test -f` must NOT set testsRan (#2754)")
	}

	if got := r.checkPreAction("git_commit", `{"message":"wip"}`); got == "" {
		t.Fatal("git_commit warning must still fire after a bare `test` conditional (#2754)")
	}

	if got := r.checkPreAction("run_command", "git push origin main"); got == "" {
		t.Fatal("git push warning must still fire after a bare `test` conditional (#2754)")
	}
}

func TestIssue2754DestructivePrecheckDoesNotDisarmGate(t *testing.T) {

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
