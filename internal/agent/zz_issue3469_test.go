package agent

import (
	"encoding/json"
	"testing"
)

// #3469: reaffirmations ("Ok, don't touch X, that's exactly right") must
// never release a persisted refusal - the deletion is persistent, silent,
// and unrecoverable.
func TestRefusalReleaseReaffirmationVeto(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch config.yaml", "user")

	vetoes := []string{
		// Issue primary: confirmation that opens with a weak lift word.
		"Ok, don't touch config.yaml, that's exactly right",
		// Issue variant.
		"Fine, keep avoiding --force",
		// Continuing refusal without any lift word at all.
		"Still don't touch config.yaml please",
		"Leave config.yaml alone forever",
		"config.yaml stays untouched",
		// Chinese reaffirmation.
		"好的，别动 config.yaml",
	}
	for _, v := range vetoes {
		if n := l.release(v); n != 0 {
			t.Errorf("reaffirmation %q must not release, removed %d", v, n)
		}
	}
	if len(l.data.Entries) != 1 {
		t.Fatalf("entry must survive reaffirmations, have %d", len(l.data.Entries))
	}
}

// #3469: a bare weak lift word without positive authorization is not
// release evidence; with authorization it releases.
func TestRefusalReleaseWeakLiftNeedsAuthorization(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch internal/auth/core.go", "user")

	// "ok ... core.go" alone: no authorization word - keep the block.
	if n := l.release("ok internal/auth/core.go"); n != 0 {
		t.Error("weak lift without authorization must not release")
	}
	// Weak lift + authorization: release.
	if n := l.release("ok, you can modify internal/auth/core.go now"); n != 1 {
		t.Errorf("weak lift + authorization must release, got %d", n)
	}
}

// #3469 strong lift phrases release without any weak-word ceremony.
func TestRefusalReleaseStrongLift(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never use git push --force", "user")
	if n := l.release("go ahead with git push --force"); n != 1 {
		t.Errorf("strong lift must release, got %d", n)
	}
}

// #3469 (note): read-only run_command shapes are exempt from the
// write-class block even when the refusal target appears in the command.
func TestRefusalCheckBlockedReadOnlyCmdExempt(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record(`don't touch build/output`, "user")

	grep := mustJSON3469(t, map[string]string{"command": "grep foo build/output"})
	if msg := l.checkBlocked("run_command", grep); msg != "" {
		t.Errorf("read-only grep must be exempt, got: %s", msg)
	}
	ls := mustJSON3469(t, map[string]string{"command": "ls build/output"})
	if msg := l.checkBlocked("run_command", ls); msg != "" {
		t.Errorf("read-only ls must be exempt, got: %s", msg)
	}
	// Mutating commands still block.
	rm := mustJSON3469(t, map[string]string{"command": "rm -rf build/output"})
	if msg := l.checkBlocked("run_command", rm); msg == "" {
		t.Error("mutating command on refusal target must block")
	}
}

func mustJSON3469(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
