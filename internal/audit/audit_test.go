package audit

import (
	"encoding/json"
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

// Path returns exactly the path the ledger was opened with.
func TestLedgerPath(t *testing.T) {
	l, path := mkLedger(t)
	if l.Path() != path {
		t.Errorf("Path() = %q, want %q", l.Path(), path)
	}
}

// Opening a ledger whose parent directory does not exist fails with a
// wrapped error instead of panicking.
func TestOpenMissingDirErrors(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing", "audit.jsonl"), "s")
	if err == nil {
		t.Fatal("expected error opening a ledger in a missing directory")
	}
	if !strings.Contains(err.Error(), "open ledger") {
		t.Errorf("err = %v, want wrapped 'open ledger' error", err)
	}
}

// A write failure (here: the underlying fd closed behind the ledger's back,
// simulating an I/O error such as a yanked disk) is returned to the caller,
// yields a zero Entry, and must not advance the chain head.
func TestAppendWriteError(t *testing.T) {
	l, _ := mkLedger(t)
	first, err := l.Append(ev("a", StatusOK))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Close the raw file handle without going through Ledger.Close so the
	// next Append hits the write-error path with l.f still non-nil.
	if err := l.f.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}
	e, err := l.Append(ev("b", StatusOK))
	if err == nil {
		t.Fatal("expected write error on a closed fd")
	}
	if !strings.Contains(err.Error(), "write entry") {
		t.Errorf("err = %v, want wrapped 'write entry' error", err)
	}
	if e != (Entry{}) {
		t.Errorf("failed Append returned %+v, want zero Entry", e)
	}
	// The chain head must be unchanged: the in-memory seq/prev still point
	// at the last successfully persisted entry.
	if l.seq != first.Seq || l.prev != first.Hash {
		t.Errorf("chain head advanced past failed write: seq=%d prev=%s", l.seq, l.prev)
	}
}

// The per-event Session override wins over the ledger's session; an empty
// Event.Session falls back to the ledger's. Both persist correctly.
func TestSessionOverride(t *testing.T) {
	l, path := mkLedger(t) // ledger session "sess-1"
	overridden, err := l.Append(Event{Tool: "a", Status: StatusOK, Session: "sess-2"})
	if err != nil {
		t.Fatalf("Append override: %v", err)
	}
	fallback, err := l.Append(ev("b", StatusOK))
	if err != nil {
		t.Fatalf("Append fallback: %v", err)
	}
	if overridden.Session != "sess-2" {
		t.Errorf("overridden session = %q, want sess-2", overridden.Session)
	}
	if fallback.Session != "sess-1" {
		t.Errorf("fallback session = %q, want sess-1", fallback.Session)
	}
	entries, err := scanEntries(path)
	if err != nil {
		t.Fatalf("scanEntries: %v", err)
	}
	if entries[0].Session != "sess-2" || entries[1].Session != "sess-1" {
		t.Errorf("persisted sessions = %q, %q; want sess-2, sess-1", entries[0].Session, entries[1].Session)
	}
	if rep, err := Verify(path); err != nil || !rep.OK() {
		t.Errorf("Verify = %+v, %v; want clean", rep, err)
	}
}

// writeHeadFile surfaces tmp-write failures (missing parent directory).
func TestWriteHeadFileMissingDir(t *testing.T) {
	err := writeHeadFile(filepath.Join(t.TempDir(), "missing", "audit.jsonl.head"), headFile{Version: headVersion})
	if err == nil {
		t.Fatal("expected error writing head file into a missing directory")
	}
	if !strings.Contains(err.Error(), "write head") {
		t.Errorf("err = %v, want wrapped 'write head' error", err)
	}
}

// Verify fails hard (returns an error) only when the ledger file itself is
// unreadable — e.g. a directory — not for content-level problems.
func TestVerifyUnreadablePath(t *testing.T) {
	_, err := Verify(t.TempDir()) // a directory: os.ReadFile errors, not IsNotExist
	if err == nil {
		t.Fatal("expected an error verifying a directory path")
	}
	if !strings.Contains(err.Error(), "read ledger") {
		t.Errorf("err = %v, want wrapped 'read ledger' error", err)
	}
}

// A .head sidecar with an unknown schema version is recognized as an anchor
// (HeadAnchored=true) but its seq is ignored for truncation verdicts.
func TestHeadFileUnknownVersionIgnoredForTruncation(t *testing.T) {
	l, path := mkLedger(t)
	if _, err := l.Append(ev("a", StatusOK)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	l.Close()
	// Hand-craft a v999 anchor claiming seq 100 — far beyond the file.
	data, err := json.Marshal(headFile{Version: 999, Seq: 100, Hash: "deadbeef", Time: "t"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path+".head", data, 0o600); err != nil {
		t.Fatalf("write head: %v", err)
	}
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.HeadAnchored {
		t.Error("HeadAnchored should be true: the sidecar exists")
	}
	if rep.Truncated != nil {
		t.Errorf("Truncated = %+v, want nil (unknown anchor version ignored)", rep.Truncated)
	}
	if !rep.OK() {
		t.Errorf("OK() = false, want true: %+v", rep)
	}
}

// A corrupt (unparseable) .head sidecar does not fail Verify and does not
// claim anchoring.
func TestHeadFileCorruptJSON(t *testing.T) {
	l, path := mkLedger(t)
	if _, err := l.Append(ev("a", StatusOK)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	l.Close()
	if err := os.WriteFile(path+".head", []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write head: %v", err)
	}
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.HeadAnchored {
		t.Error("HeadAnchored should be false for an unparseable sidecar")
	}
	if rep.Truncated != nil {
		t.Errorf("Truncated = %+v, want nil", rep.Truncated)
	}
	if !rep.OK() {
		t.Errorf("OK() = false, want true: %+v", rep)
	}
}

// LastHash stays at the last VERIFIED entry: after an in-place edit at seq 2
// of 4, the report still counts 4 scanned entries but carries entry 1's hash.
func TestLastHashStopsAtBreak(t *testing.T) {
	l, path := mkLedger(t)
	var hash1 string
	for i := 0; i < 4; i++ {
		e, err := l.Append(ev(fmt.Sprintf("tool_%d", i), StatusOK))
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if i == 0 {
			hash1 = e.Hash
		}
	}
	l.Close()
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines[1] = strings.Replace(lines[1], `"tool_1"`, `"evil"`, 1)
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.FirstBreak == nil || rep.FirstBreak.Seq != 2 {
		t.Fatalf("FirstBreak = %+v, want break at seq 2", rep.FirstBreak)
	}
	if rep.Entries != 4 {
		t.Errorf("Entries = %d, want 4 (file scanned fully)", rep.Entries)
	}
	if rep.LastHash != hash1 {
		t.Errorf("LastHash = %s, want entry 1's hash %s", rep.LastHash, hash1)
	}
}

// Periodic anchoring mid-session: entries appended AFTER the anchor make the
// file longer than the anchor — the opposite of truncation — and must stay
// clean.
func TestAnchorThenMoreAppendsStaysClean(t *testing.T) {
	l, path := mkLedger(t)
	for i := 0; i < 2; i++ {
		if _, err := l.Append(ev("tool", StatusOK)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := l.Anchor(); err != nil {
		t.Fatalf("Anchor: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := l.Append(ev("tool", StatusOK)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	l.Close()
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK() {
		t.Errorf("report = %+v, want clean", rep)
	}
	if !rep.HeadAnchored {
		t.Error("expected HeadAnchored = true")
	}
}
