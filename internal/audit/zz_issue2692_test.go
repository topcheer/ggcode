package audit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func openLedger2692(t *testing.T, path string) *Ledger {
	t.Helper()
	l, err := Open(path, "sess-2692")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

func append2692(t *testing.T, l *Ledger, tool string) Entry {
	t.Helper()
	e, err := l.Append(Event{Tool: tool, Status: StatusOK, InputHash: "00"})
	if err != nil {
		t.Fatalf("Append(%s): %v", tool, err)
	}
	return e
}

func mustRead2692(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// #2692: a crash-torn trailing partial JSON line was scanned into the
// malformed sentinel, and Open unconditionally took it as the chain tail
// (seq=0, prev=""), so post-crash Append wrote a broken entry and Verify
// failed forever - exactly the crash-recovery scenario the package doc
// calls "when the tail matters most". The torn line never committed; Open
// must heal it (truncate to the last complete line) and continue the chain.
func TestIssue2692_TornTailHealsChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l := openLedger2692(t, path)
	append2692(t, l, "t1")
	append2692(t, l, "t2")
	e3 := append2692(t, l, "t3")
	l.Close()

	// Simulate the crash-torn tail: a partial JSON line without a newline.
	torn := append(mustRead2692(t, path), []byte(`{"seq":3,"tool":"par`)...)
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	l2 := openLedger2692(t, path)
	got := append2692(t, l2, "t4")
	l2.Close()

	if got.Seq != e3.Seq+1 {
		t.Errorf("post-heal Append seq = %d, want %d (continue chain after last durable entry)", got.Seq, e3.Seq+1)
	}
	if got.PrevHash != e3.Hash {
		t.Errorf("post-heal Append PrevHash = %q, want hash of last durable entry", got.PrevHash)
	}
	if bytes.Contains(mustRead2692(t, path), []byte(`"par`)) {
		t.Error("torn partial line still present after heal; expected truncation to last complete line")
	}
	rep, err := Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Errorf("Verify after heal = break@%+v trunc@%+v, want OK (torn tail healed, chain intact)", rep.FirstBreak, rep.Truncated)
	}
}

// Mid-file corruption - garbage BETWEEN valid entries - is tamper evidence,
// NOT a torn tail: the tail is a real entry, no heal fires, and Verify
// keeps reporting it.
func TestIssue2692_MidfileCorruptionPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l := openLedger2692(t, path)
	append2692(t, l, "t1")
	append2692(t, l, "t2")
	l.Close()

	// Splice garbage between the two valid lines (edit-in-place evidence).
	raw := mustRead2692(t, path)
	nl := bytes.IndexByte(raw, '\n')
	spliced := append(append(append([]byte{}, raw[:nl+1]...), []byte("GARBAGE-NOT-JSON\n")...), raw[nl+1:]...)
	if err := os.WriteFile(path, spliced, 0o600); err != nil {
		t.Fatal(err)
	}

	l2 := openLedger2692(t, path)
	if e := append2692(t, l2, "t3"); e.Seq != 3 {
		t.Errorf("seq = %d, want 3 (chain tail was the real last entry, no heal)", e.Seq)
	}
	l2.Close()

	rep, err := Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() {
		t.Error("Verify = OK with mid-file garbage, want corruption reported (heal must not mask tamper evidence)")
	}
}

// Multiple trailing torn lines (torn write + later garbage appends) all
// heal; the chain continues from the last complete entry.
func TestIssue2692_MultipleTornTailLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l := openLedger2692(t, path)
	e1 := append2692(t, l, "t1")
	l.Close()
	torn := append(mustRead2692(t, path), []byte("{\"partial\nALSO-GARBAGE")...)
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	l2 := openLedger2692(t, path)
	got := append2692(t, l2, "t2")
	l2.Close()
	if got.Seq != e1.Seq+1 || got.PrevHash != e1.Hash {
		t.Errorf("post-heal entry = seq %d prev %q, want seq %d chaining from t1", got.Seq, got.PrevHash, e1.Seq+1)
	}
	if rep, err := Verify(path); err != nil || !rep.OK() {
		t.Errorf("Verify after multi-torn heal = %+v err=%v, want OK", rep, err)
	}
}
