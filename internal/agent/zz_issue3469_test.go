package agent

import (
	"encoding/json"
	"testing"
)

func TestRefusalReleaseReaffirmationVeto(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch config.yaml", "nl", refusalKindDirect)

	vetoes := []string{

		"Ok, don't touch config.yaml, that's exactly right",

		"Fine, keep avoiding --force",

		"Still don't touch config.yaml please",
		"Leave config.yaml alone forever",
		"config.yaml stays untouched",

		"好的，别动 config.yaml",

		"可以了，不过 config.yaml 继续保持别碰",

		"Ok, config.yaml is forbidden, that's exactly right",
		"Alright, config.yaml stays off-limits",
		"解除吧，其实不要动了，保持现状",
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

func TestRefusalReleaseWeakLiftNeedsAuthorization(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)

	if n := l.release("ok internal/auth/core.go"); n != 0 {
		t.Error("weak lift without authorization must not release")
	}

	if n := l.release("ok, you can modify internal/auth/core.go now"); n != 1 {
		t.Errorf("weak lift + authorization must release, got %d", n)
	}
}

func TestRefusalReleaseStrongLift(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never use git push --force", "nl", refusalKindDirect)
	if n := l.release("go ahead with git push --force"); n != 1 {
		t.Errorf("strong lift must release, got %d", n)
	}
}

func TestRefusalCheckBlockedReadOnlyCmdExempt(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record(`don't touch build/output`, "nl", refusalKindDirect)

	grep := mustJSON3469(t, map[string]string{"command": "grep foo build/output"})
	if msg := l.checkBlocked("run_command", grep); msg != "" {
		t.Errorf("read-only grep must be exempt, got: %s", msg)
	}
	ls := mustJSON3469(t, map[string]string{"command": "ls build/output"})
	if msg := l.checkBlocked("run_command", ls); msg != "" {
		t.Errorf("read-only ls must be exempt, got: %s", msg)
	}

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
