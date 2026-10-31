package agent

import "testing"

// #4679: the release/lift path must use the same whole-token anchor match
// as checkBlocked (#4632) - approving the SAFER variant whose flag name
// merely contains the refused flag as a prefix must NOT release the
// original refusal, while an exact-token lift still releases.
func TestIssue4679_WholeTokenLiftSymmetry(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("never run git push --force when pushing", "nl", refusalKindDirect)
	before := len(l.data.Entries)

	// Strong lift naming only the SAFER lease variant: the refused flag is
	// a prefix inside the variant name - must NOT lift the entry.
	if n := l.release("ok, go ahead and use --force-with-lease now"); n != 0 {
		t.Fatalf("lift naming only the safer variant must not release the original refusal, released %d (#4679)", n)
	}
	if len(l.data.Entries) != before {
		t.Fatal("entry must survive the variant-only lift (#4679)")
	}

	// Exact-token lift still releases.
	if n := l.release("ok, go ahead and use --force now"); n == 0 {
		t.Fatal("exact-token lift must still release (#4679)")
	}
}
