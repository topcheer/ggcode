package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Path returns the configured ledger path.
func TestLedgerPathAccessor(t *testing.T) {
	l, path := mkLedger(t)
	if l.Path() != path {
		t.Errorf("Path() = %q, want %q", l.Path(), path)
	}
}

// Open on a path whose parent directory does not exist surfaces a wrapped
// error instead of a later surprise on first Append.
func TestOpenMissingParentDirErrors(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing-dir", "audit.jsonl"), "s")
	if err == nil {
		t.Fatal("expected error opening a ledger in a missing directory")
	}
	if !strings.Contains(err.Error(), "audit: open ledger") {
		t.Errorf("err = %v, want wrapped 'audit: open ledger' error", err)
	}
}

// Event.Session overrides the ledger-wide session for that one entry; the
// ledger default resumes for subsequent entries.
func TestSessionOverride(t *testing.T) {
	l, _ := mkLedger(t)
	def, err := l.Append(ev("a", StatusOK))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if def.Session != "sess-1" {
		t.Errorf("default Session = %q, want sess-1", def.Session)
	}
	ov, err := l.Append(Event{Tool: "b", Status: StatusOK, InputHash: "h", Session: "sess-override"})
	if err != nil {
		t.Fatalf("Append override: %v", err)
	}
	if ov.Session != "sess-override" {
		t.Errorf("override Session = %q, want sess-override", ov.Session)
	}
	after, err := l.Append(ev("c", StatusOK))
	if err != nil {
		t.Fatalf("Append after override: %v", err)
	}
	if after.Session != "sess-1" {
		t.Errorf("Session after override = %q, want ledger default sess-1", after.Session)
	}
}

// Entries without Session/Err omit those JSON keys (omitempty), and the
// resulting file still verifies clean after a re-parse. The ledger itself
// must be opened with an empty session - a ledger-wide session would be
// stamped onto every entry regardless of the event's own (empty) Session.
func TestJSONRoundTripOmitsEmptyFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := Open(path, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	if _, err := l.Append(Event{Tool: "t", Status: StatusOK, InputHash: "h"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	l.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	line := strings.TrimSpace(string(data))
	if strings.Contains(line, `"session"`) {
		t.Errorf("empty session should be omitted, got %s", line)
	}
	if strings.Contains(line, `"err"`) {
		t.Errorf("empty err should be omitted, got %s", line)
	}
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK() || rep.Entries != 1 {
		t.Errorf("report = %+v, want clean 1-entry chain", rep)
	}
}

// Timestamps are RFC3339Nano in UTC and parse back losslessly.
func TestEntryTimeRFC3339NanoUTC(t *testing.T) {
	l, _ := mkLedger(t)
	e, err := l.Append(ev("t", StatusOK))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	ts, err := time.Parse(time.RFC3339Nano, e.Time)
	if err != nil {
		t.Fatalf("entry time %q not RFC3339Nano: %v", e.Time, err)
	}
	if _, offset := ts.Zone(); offset != 0 {
		t.Errorf("entry time offset = %d, want 0 (UTC)", offset)
	}
	if d := time.Since(ts); d < 0 || d > time.Minute {
		t.Errorf("entry time %q not near now (%v)", e.Time, d)
	}
}

// Field values are length-prefixed before hashing: two entries whose fields
// concatenate to the identical byte string without length prefixes must
// still produce different hashes.
func TestLengthPrefixPreventsBoundaryCollision(t *testing.T) {
	a := Entry{Seq: 1, Time: "t", Tool: "ab", Status: "c", PrevHash: GenesisPrevHash}
	b := Entry{Seq: 1, Time: "t", Tool: "a", Status: "bc", PrevHash: GenesisPrevHash}
	if a.Tool+a.Status != b.Tool+b.Status {
		t.Fatal("precondition: raw concatenations should be identical")
	}
	if hashEntry(a) == hashEntry(b) {
		t.Fatal("hash collision across a field boundary despite length prefixes")
	}
}

// Partial re-chain attack: an attacker edits entry 2 and recomputes its own
// hash (making entry 2 self-consistent) but does not update entry 3's
// prev_hash. Verify must catch it at seq 3 with a prev_hash link mismatch -
// the branch that seq-gap and hash-mismatch cases never reach.
func TestPrevHashLinkMismatchDetected(t *testing.T) {
	l, path := mkLedger(t)
	for i := 0; i < 3; i++ {
		if _, err := l.Append(ev("tool", StatusOK)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	l.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var e2 Entry
	if err := json.Unmarshal([]byte(lines[1]), &e2); err != nil {
		t.Fatalf("Unmarshal entry 2: %v", err)
	}
	e2.Tool = "forged"
	e2.Hash = hashEntry(e2) // self-consistent forged entry
	forged, err := json.Marshal(e2)
	if err != nil {
		t.Fatalf("Marshal forged entry: %v", err)
	}
	lines[1] = string(forged)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.FirstBreak == nil {
		t.Fatal("expected a break after partially re-chaining entry 2")
	}
	if rep.FirstBreak.Seq != 3 {
		t.Errorf("break at seq %d, want 3", rep.FirstBreak.Seq)
	}
	if !strings.Contains(rep.FirstBreak.Reason, "prev_hash link mismatch") {
		t.Errorf("reason = %q, want prev_hash link mismatch", rep.FirstBreak.Reason)
	}
	if rep.OK() {
		t.Error("OK() should be false for a re-chained ledger")
	}
	// The forged entry 2 is self-consistent, so it still VERIFIES; the chain
	// breaks at seq 3, and LastHash is the forged seq-2 hash - proving the
	// report pinpoints exactly how far the chain remained trustworthy.
	if rep.LastHash != e2.Hash {
		t.Errorf("LastHash = %q, want forged seq-2 hash %q", rep.LastHash, e2.Hash)
	}
	if rep.LastHash == GenesisPrevHash {
		t.Error("LastHash should record the last verified entry, not genesis")
	}
}

// Verify reports only the first break: LastHash stays at the last VERIFIED
// entry while Entries counts every parsed line.
func TestVerifyStopsAtFirstBreak(t *testing.T) {
	l, path := mkLedger(t)
	var h1 string
	for i := 0; i < 4; i++ {
		e, err := l.Append(ev("tool", StatusOK))
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if i == 0 {
			h1 = e.Hash
		}
	}
	l.Close()
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines[1] = strings.Replace(lines[1], `"duration_ms":42`, `"duration_ms":43`, 1)
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.FirstBreak == nil || rep.FirstBreak.Seq != 2 {
		t.Fatalf("FirstBreak = %+v, want break at seq 2", rep.FirstBreak)
	}
	if rep.LastHash != h1 {
		t.Errorf("LastHash = %q, want seq-1 hash %q", rep.LastHash, h1)
	}
	if rep.Entries != 4 {
		t.Errorf("Entries = %d, want 4", rep.Entries)
	}
}

// Verify returns a hard error only for unreadable files; a directory path
// takes the non-IsNotExist branch of scanEntries.
func TestVerifyUnreadableFileErrors(t *testing.T) {
	_, err := Verify(t.TempDir())
	if err == nil {
		t.Fatal("expected hard error verifying a directory path")
	}
	if !strings.Contains(err.Error(), "audit: read ledger") {
		t.Errorf("err = %v, want wrapped 'audit: read ledger' error", err)
	}
}

// A stale anchor (the file grew past the anchored head) must not flag
// truncation - the anchor only proves a lower bound on progress.
func TestStaleAnchorNoTruncation(t *testing.T) {
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
			t.Fatalf("Append post-anchor: %v", err)
		}
	}
	l.Close()

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.HeadAnchored {
		t.Error("expected HeadAnchored = true")
	}
	if rep.Truncated != nil {
		t.Errorf("unexpected truncation for a file that grew past its anchor: %+v", rep.Truncated)
	}
	if !rep.OK() || rep.Entries != 5 {
		t.Errorf("report = %+v, want clean 5-entry chain", rep)
	}
}

// A .head sidecar with an unknown schema version disables truncation
// checking (the guard short-circuits before the seq comparison) but the
// anchor's presence is still reported.
func TestHeadVersionMismatchSkipsTruncation(t *testing.T) {
	l, path := mkLedger(t)
	for i := 0; i < 3; i++ {
		if _, err := l.Append(ev("tool", StatusOK)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	l.Close()
	future := `{"version":999,"seq":9,"hash":"x","time":"t"}`
	if err := os.WriteFile(path+".head", []byte(future), 0o600); err != nil {
		t.Fatalf("WriteFile head: %v", err)
	}

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.HeadAnchored {
		t.Error("expected HeadAnchored = true even with an unknown version")
	}
	if rep.Truncated != nil {
		t.Errorf("unknown head version must skip truncation checks, got %+v", rep.Truncated)
	}
}

// A corrupt .head sidecar (invalid JSON) is not an anchor: verification
// proceeds as if none existed.
func TestCorruptHeadNotAnchored(t *testing.T) {
	l, path := mkLedger(t)
	if _, err := l.Append(ev("tool", StatusOK)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	l.Close()
	if err := os.WriteFile(path+".head", []byte("not json{"), 0o600); err != nil {
		t.Fatalf("WriteFile head: %v", err)
	}

	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.HeadAnchored {
		t.Error("corrupt head must not count as anchored")
	}
	if !rep.OK() {
		t.Errorf("report = %+v, want clean (corrupt anchor ignored)", rep)
	}
}

// Anchor failures surface to the caller: when the destination path is
// occupied by a non-empty directory, the atomic rename fails and the tmp
// sidecar is cleaned up.
func TestAnchorRenameFailureSurfaces(t *testing.T) {
	l, path := mkLedger(t)
	if _, err := l.Append(ev("tool", StatusOK)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	headDir := path + ".head"
	if err := os.MkdirAll(filepath.Join(headDir, "keep"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(headDir) })

	if err := l.Anchor(); err == nil {
		t.Fatal("expected rename failure when .head is a non-empty directory")
	}
	if _, serr := os.Stat(path + ".head.tmp"); serr == nil {
		t.Error("tmp sidecar should be removed after a failed rename")
	}
}

// Anchor failures surface when the sidecar cannot be created at all
// (read-only parent directory).
func TestAnchorWriteFailureSurfaces(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permission bits don't restrict writes on Windows; root ignores them elsewhere")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	l, err := Open(path, "s")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	if _, err := l.Append(ev("tool", StatusOK)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	err = l.Anchor()
	if err == nil {
		t.Fatal("expected write failure for the head sidecar in a read-only dir")
	}
	if !strings.Contains(err.Error(), "audit: write head") {
		t.Errorf("err = %v, want wrapped 'audit: write head' error", err)
	}
}
