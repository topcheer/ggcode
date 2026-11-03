package agent

import "testing"

func TestIssue4679_WholeTokenLiftSymmetry(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("never run git push --force when pushing", "nl", refusalKindDirect)
	before := len(l.data.Entries)

	if n := l.release("ok, go ahead and use --force-with-lease now"); n != 0 {
		t.Fatalf("lift naming only the safer variant must not release the original refusal, released %d (#4679)", n)
	}
	if len(l.data.Entries) != before {
		t.Fatal("entry must survive the variant-only lift (#4679)")
	}

	if n := l.release("ok, go ahead and use --force now"); n == 0 {
		t.Fatal("exact-token lift must still release (#4679)")
	}
}
