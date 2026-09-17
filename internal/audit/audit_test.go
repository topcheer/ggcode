package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func mkLedger(t *testing.T) (*Ledger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := Open(path, "sess-1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l, path
}

func ev(tool, status string) Event {
	return Event{Tool: tool, Status: status, InputHash: "abc123", DurationMS: 42}
}

// Happy path: a chain of entries verifies clean.
func TestAppendVerifyHappyPath(t *testing.T) {
	l, path := mkLedger(t)
	for i := 0; i < 5; i++ {
		if _, err := l.Append(ev(fmt.Sprintf("tool_%d", i), StatusOK)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Entries != 5 {
		t.Errorf("Entries = %d, want 5", rep.Entries)
	}
	if rep.FirstBreak != nil {
		t.Errorf("unexpected break: %+v", rep.FirstBreak)
	}
	if rep.Truncated != nil {
		t.Errorf("unexpected truncation: %+v", rep.Truncated)
	}
	if !rep.OK() {
		t.Errorf("Report not OK: %+v", rep)
	}
}

// Chain structure: seqs are 1-based, genesis links to zeros, every hash is
// distinct and 64 hex chars.
func TestChainStructure(t *testing.T) {
	l, path := mkLedger(t)
	var lastHash string
	for i := 0; i < 3; i++ {
		e, err := l.Append(ev("read_file", StatusOK))
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if e.Seq != int64(i+1) {
			t.Errorf("Seq = %d, want %d", e.Seq, i+1)
		}
		wantPrev := GenesisPrevHash
		if i > 0 {
			wantPrev = lastHash
		}
		if e.PrevHash != wantPrev {
			t.Errorf("entry %d PrevHash = %s, want %s", e.Seq, e.PrevHash, wantPrev)
		}
		if len(e.Hash) != 64 {
			t.Errorf("hash length = %d, want 64", len(e.Hash))
		}
		if e.Hash == lastHash {
			t.Errorf("entry %d hash identical to predecessor", e.Seq)
		}
		lastHash = e.Hash
	}
	if _, err := Verify(path); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Editing an entry in place is detected at that exact seq.
func TestTamperMiddleDetected(t *testing.T) {
	l, path := mkLedger(t)
	for i := 0; i < 4; i++ {
		if _, err := l.Append(ev("tool", StatusOK)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	l.Close()
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Flip the status of entry 3 (1-based) from ok to error.
	lines[2] = strings.Replace(lines[2], `"status":"ok"`, `"status":"error"`, 1)
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.FirstBreak == nil {
		t.Fatal("expected a break after editing entry 3")
	}
	if rep.FirstBreak.Seq != 3 {
		t.Errorf("break at seq %d, want 3", rep.FirstBreak.Seq)
	}
	if !strings.Contains(rep.FirstBreak.Reason, "hash mismatch") {
		t.Errorf("reason = %q, want hash mismatch", rep.FirstBreak.Reason)
	}
}

// Reordering two entries breaks the prev-hash linkage.
func TestReorderDetected(t *testing.T) {
	l, path := mkLedger(t)
	for i := 0; i < 3; i++ {
		if _, err := l.Append(ev(fmt.Sprintf("tool_%d", i), StatusOK)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	l.Close()
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines[1], lines[2] = lines[2], lines[1]
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.FirstBreak == nil {
		t.Fatal("expected a break after swapping entries")
	}
}

// Deleting tail entries is detected when a head anchor exists.
func TestTailTruncationDetectedWithAnchor(t *testing.T) {
	l, path := mkLedger(t)
	for i := 0; i < 5; i++ {
		if _, err := l.Append(ev("tool", StatusOK)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.Anchor(); err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	l.Close()
	// Simulate tail truncation: keep only the first 3 entries.
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	os.WriteFile(path, []byte(strings.Join(lines[:3], "\n")+"\n"), 0o600)

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.FirstBreak != nil {
		t.Fatalf("unexpected break: %+v", rep.FirstBreak)
	}
	if !rep.HeadAnchored {
		t.Error("expected HeadAnchored = true")
	}
	if rep.Truncated == nil {
		t.Fatal("expected truncation detection")
	}
	if rep.Truncated.AnchoredSeq != 5 || rep.Truncated.FileSeq != 3 {
		t.Errorf("Truncated = %+v, want anchored 5 / file 3", rep.Truncated)
	}
	if rep.OK() {
		t.Error("OK() should be false for a truncated ledger")
	}
}

// Without an anchor, tail truncation is (by design) not detectable — the
// remaining prefix must still verify clean.
func TestTailTruncationWithoutAnchorStaysClean(t *testing.T) {
	l, path := mkLedger(t)
	for i := 0; i < 5; i++ {
		if _, err := l.Append(ev("tool", StatusOK)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	l.Close()
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	os.WriteFile(path, []byte(strings.Join(lines[:2], "\n")+"\n"), 0o600)

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK() {
		t.Errorf("unanchored prefix should verify clean, got %+v", rep.FirstBreak)
	}
	if rep.HeadAnchored {
		t.Error("no head file was written; HeadAnchored should be false")
	}
}

// Re-opening the same file continues the same chain (no fork).
func TestReopenContinuesChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l1, err := Open(path, "s")
	if err != nil {
		t.Fatalf("Open1: %v", err)
	}
	if _, err := l1.Append(ev("a", StatusOK)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := l1.Append(ev("b", StatusOK)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := l1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	l2, err := Open(path, "s")
	if err != nil {
		t.Fatalf("Open2: %v", err)
	}
	defer l2.Close()
	e, err := l2.Append(ev("c", StatusOK))
	if err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	if e.Seq != 3 {
		t.Errorf("Seq after reopen = %d, want 3", e.Seq)
	}
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK() || rep.Entries != 3 {
		t.Errorf("report = %+v, want clean 3-entry chain", rep)
	}
}

// Concurrent appends must produce a verifiable chain (mutex correctness).
func TestConcurrentAppends(t *testing.T) {
	l, path := mkLedger(t)
	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := l.Append(ev(fmt.Sprintf("tool_%d", i), StatusOK)); err != nil {
				t.Errorf("Append: %v", err)
			}
		}(i)
	}
	wg.Wait()
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Entries != n || rep.FirstBreak != nil {
		t.Errorf("report = %+v, want clean %d-entry chain", rep, n)
	}
}

// A malformed line is reported as a break, not a crash.
func TestMalformedLineDetected(t *testing.T) {
	l, path := mkLedger(t)
	if _, err := l.Append(ev("a", StatusOK)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	l.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("not json at all\n")
	f.Close()

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.FirstBreak == nil {
		t.Fatal("expected a break for a malformed line")
	}
	if rep.FirstBreak.Seq != 0 {
		t.Errorf("break seq = %d, want 0", rep.FirstBreak.Seq)
	}
	if !strings.Contains(rep.FirstBreak.Reason, "sequence gap") {
		t.Errorf("reason = %q, want sequence gap", rep.FirstBreak.Reason)
	}
}

// Anchoring an empty ledger records seq 0 and verifies without truncation.
func TestAnchorEmptyLedger(t *testing.T) {
	l, path := mkLedger(t)
	if err := l.Anchor(); err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.HeadAnchored || rep.Truncated != nil {
		t.Errorf("report = %+v, want anchored, untruncated", rep)
	}
}

// Appending after Close is an error, not a silent no-op.
func TestAppendAfterCloseErrors(t *testing.T) {
	l, _ := mkLedger(t)
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := l.Append(ev("a", StatusOK)); err == nil {
		t.Fatal("expected error appending to a closed ledger")
	}
}

// Verify on a missing file returns an empty clean report.
func TestVerifyMissingFile(t *testing.T) {
	rep, err := Verify(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Entries != 0 || !rep.OK() {
		t.Errorf("report = %+v, want empty clean", rep)
	}
}

// The hash must cover the error text: editing only the Err field breaks the
// chain (length-prefixed hashing cannot be confused by separator-bearing
// error messages either).
func TestHashCoversErrorText(t *testing.T) {
	e1 := Entry{Seq: 1, Time: "t", Tool: "run_command", Status: StatusError, Err: "boom|prev\x00junk", PrevHash: GenesisPrevHash}
	e2 := e1
	e2.Err = "boom"
	if hashEntry(e1) == hashEntry(e2) {
		t.Fatal("hashes must differ when only Err differs")
	}
}
