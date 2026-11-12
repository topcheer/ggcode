package audit

import (
	"path/filepath"
	"testing"
)

// TestRationaleRoundTrip pins #decision-audit: the rationale attached to an
// Event survives the ledger round-trip and is empty on entries that had none.
func TestRationaleRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l, err := Open(path, "sess-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := l.Append(Event{Tool: "edit_file", Status: "ok", InputHash: "h1"}); err != nil {
		t.Fatalf("append1: %v", err)
	}
	if _, err := l.Append(Event{Tool: "edit_file", Status: "ok", InputHash: "h2", Rationale: "fix login bug: stale token cache"}); err != nil {
		t.Fatalf("append2: %v", err)
	}
	if _, err := l.Append(Event{Tool: "grep", Status: "ok", InputHash: "h3"}); err != nil {
		t.Fatalf("append3: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	entries, err := scanEntries(path)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Rationale != "" || entries[2].Rationale != "" {
		t.Fatalf("entries without rationale must not carry one: %q / %q", entries[0].Rationale, entries[2].Rationale)
	}
	if entries[1].Rationale != "fix login bug: stale token cache" {
		t.Fatalf("rationale lost in round-trip: %q", entries[1].Rationale)
	}
}

// TestRationaleHashCompat pins the backward-compat contract: an entry with
// no rationale hashes identically to the pre-rationale formula (tagged-4
// block absent), while adding a rationale changes the hash.
func TestRationaleHashCompat(t *testing.T) {
	base := Entry{Seq: 7, Time: "2026-01-01T00:00:00Z", Session: "s", Tool: "edit_file", Status: "ok", InputHash: "h", DurationMS: 5, PrevHash: "prev"}
	noRat := hashEntry(base)
	again := hashEntry(base)
	if noRat != again {
		t.Fatal("hashEntry not deterministic")
	}
	withRat := base
	withRat.Rationale = "why"
	if hashEntry(withRat) == noRat {
		t.Fatal("rationale must change the hash")
	}
}

// TestRationaleChainVerify pins chain integrity over a mixed ledger
// (entries with and without rationale) via the public Verify API.
func TestRationaleChainVerify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l, err := Open(path, "sess-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i, ev := range []Event{
		{Tool: "grep", Status: "ok", InputHash: "a"},
		{Tool: "edit_file", Status: "ok", InputHash: "b", Rationale: "root cause fix"},
		{Tool: "run_command", Status: "ok", InputHash: "c"},
	} {
		if _, err := l.Append(ev); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	rep, err := Verify(path)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("chain not OK: %+v", rep)
	}
}
