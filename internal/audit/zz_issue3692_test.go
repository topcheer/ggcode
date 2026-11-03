package audit

import "testing"

func TestIssue3692_VerifyIOErrorFailsClosed(t *testing.T) {
	orig := verifyChain
	defer func() { verifyChain = orig }()
	verifyChain = func(path string) (bool, *Break, *Truncation) {
		return false, nil, nil
	}
	l, err := Open(t.TempDir()+"/ledger.jsonl", "probe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(Event{Tool: "probe"}); err != nil {
		t.Fatal(err)
	}
	d, err := Digest(l.Path(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	if d.VerifyOK {
		t.Fatal("Verify IO failure must yield VerifyOK=false, not the stale true default")
	}
}

func TestIssue3692_RealVerifyStillWired(t *testing.T) {
	l, err := Open(t.TempDir()+"/ledger.jsonl", "probe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(Event{Tool: "probe"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Anchor(); err != nil {
		t.Fatal(err)
	}
	d, err := Digest(l.Path(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	if !d.VerifyOK {
		t.Fatal("intact chain must still verify OK through the default seam")
	}
}
