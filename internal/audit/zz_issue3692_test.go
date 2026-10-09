package audit

// #3692 probe: a Verify IO failure must fail CLOSED. Digest initialized
// RunDigest{VerifyOK: true} and only overwrote it when Verify returned
// err == nil - an IO error (TOCTOU between Digest's own scanEntries read
// and Verify's) left VerifyOK=true and Render() reported "VERIFIED intact"
// for a chain that could not even be read. The seam (verifyChain var)
// exists because the error window is only reachable via that TOCTOU.

import "testing"

func TestIssue3692_VerifyIOErrorFailsClosed(t *testing.T) {
	orig := verifyChain
	defer func() { verifyChain = orig }()
	verifyChain = func(path string) (bool, *Break, *Truncation) {
		return false, nil, nil // what the fail-closed fold yields on error
	}
	// Any existing ledger shape: the digest's chain fields come solely from
	// verifyChain in this probe.
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
	// The default seam must be the real Verify: a genuine chain verifies OK.
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
