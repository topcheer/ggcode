package agent

// #3469 probe: release() treated "release phrase + mentions target" as a
// lift, but a RESTATEMENT ("Ok, don't touch config.yaml, that's exactly
// right") hits both conditions and permanently deleted the block - silent,
// persistent, irreversible. A persistence-word guard must keep the entry.

import "testing"

func newIssue3469Ledger(t *testing.T) *refusalLedger {
	t.Helper()
	l := newRefusalLedger(t.TempDir())
	l.record("don't touch internal/config.yaml", "nl")
	if len(l.data.Entries) != 1 {
		t.Fatalf("seed failed: %d entries", len(l.data.Entries))
	}
	return l
}

func TestIssue3469_RestatementDoesNotRelease(t *testing.T) {
	cases := []string{
		"Ok, don't touch internal/config.yaml, that's exactly right",
		"Fine, keep avoiding internal/config.yaml",
		"Alright, internal/config.yaml stays untouched",
		"OK you can proceed, but never touch internal/config.yaml",
	}
	for _, lift := range cases {
		l := newIssue3469Ledger(t)
		if n := l.release(lift); n != 0 {
			t.Fatalf("restatement %q must NOT release, removed %d", lift, n)
		}
		if len(l.data.Entries) != 1 {
			t.Fatalf("restatement %q destroyed the entry", lift)
		}
	}
}

func TestIssue3469_CleanLiftStillReleases(t *testing.T) {
	l := newIssue3469Ledger(t)
	if n := l.release("ok you can touch internal/config.yaml now"); n != 1 {
		t.Fatalf("clean lift must release, removed %d", n)
	}
	if len(l.data.Entries) != 0 {
		t.Fatal("entry must be gone after a clean lift")
	}
	// Explicit lift phrasing without weak "ok".
	l2 := newIssue3469Ledger(t)
	if n := l2.release("go ahead and edit internal/config.yaml"); n != 1 {
		t.Fatalf("go-ahead lift must release, removed %d", n)
	}
}

func TestIssue3469_NonLiftTextUntouched(t *testing.T) {
	l := newIssue3469Ledger(t)
	if n := l.release("please review internal/config.yaml changes"); n != 0 {
		t.Fatalf("non-lift text must not release, removed %d", n)
	}
}
