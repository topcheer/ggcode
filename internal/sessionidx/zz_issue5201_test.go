package sessionidx

import (
	"fmt"
	"strings"
	"testing"
)

// twoBlockLine builds a message record whose content carries two text
// blocks — the structural shape at the center of #5201: indexLine calls
// addPostings once per block, so a per-call dedup map never sees a trigram
// repeated ACROSS blocks of the same line.
func twoBlockLine(role, text1, text2 string) string {
	return fmt.Sprintf(`{"type":"message","timestamp":"2026-11-07T10:00:00Z","message":{"role":%q,"content":[{"type":"text","text":%q},{"type":"text","text":%q}]}}`,
		role, text1, text2)
}

// TestIssue5201CrossBlockDoubleCount is the exact repro from the issue: a
// line whose two text blocks both contain "abc" but whose full text lacks
// the required trigram "bcd" (and the substring "abcd"). Pre-fix, "abc"
// contributed two postings for the line; Query("abcd") needs score >= 1
// with gr = [abc, bcd], so the duplicate alone crossed the threshold and
// the line was wrongly returned.
func TestIssue5201CrossBlockDoubleCount(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s5201", []string{
		twoBlockLine("user", "xxabcxx", "zzabczz"),
		msgLine("assistant", "unrelated filler content"), // offset variety
	})
	ix, err := Load(dir, "s5201")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}

	if hits := ix.Query([]string{"abcd"}, 10); len(hits) != 0 {
		t.Fatalf("BUG #5201: line lacking required trigram 'bcd' matched term 'abcd' (hits=%d, TermBits=%d)", len(hits), hits[0].TermBits)
	}
}

// TestIssue5201CrossBlockSinglePosting pins the indexing-layer fix: the
// trigram "abc" present in BOTH text blocks of one line must yield exactly
// ONE posting for that line (line-level dedup, per addPostings' documented
// "deduped per line" contract).
func TestIssue5201CrossBlockSinglePosting(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s5201b", []string{
		twoBlockLine("user", "xxabcxx", "zzabczz"),
	})
	ix, err := Load(dir, "s5201b")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	ix.mu.RLock()
	postings := ix.data.Trigrams["abc"]
	ix.mu.RUnlock()
	if len(postings) != 1 {
		t.Fatalf("trigram 'abc' from two blocks of one line must yield exactly 1 posting, got %d", len(postings))
	}
}

// TestIssue5201QuerySideDedupRepairsLegacyIndex pins the Query-side half of
// the fix: legacy on-disk (or in-memory) indexes built before the fix carry
// duplicate postings for the same (line, trigram) pair — written by v2
// grouped persistence and faithfully reloaded by Load. Query must count
// DISTINCT term trigrams per line, so such a legacy index stops producing
// false positives without any rebuild.
func TestIssue5201QuerySideDedupRepairsLegacyIndex(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s5201c", []string{
		twoBlockLine("user", "xxabcxx", "zzabczz"),
	})
	ix, err := Load(dir, "s5201c")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	// Simulate the legacy shape directly: hand-craft the duplicate posting
	// the pre-fix indexer would have written ("abc" twice for offset 0) and
	// nothing for the required-but-absent "bcd".
	ix.mu.Lock()
	ix.data.Trigrams = map[string][]Posting{
		"abc": {
			{Offset: 0, Length: ix.data.Trigrams["abc"][0].Length, Role: 1},
			{Offset: 0, Length: ix.data.Trigrams["abc"][0].Length, Role: 1},
		},
	}
	ix.mu.Unlock()

	if hits := ix.Query([]string{"abcd"}, 10); len(hits) != 0 {
		t.Fatalf("BUG #5201 (query-side): duplicate legacy postings must not substitute for missing trigram 'bcd' (hits=%d)", len(hits))
	}
	// Control: a line that genuinely contains all trigrams of the term must
	// still match under distinct-trigram scoring.
	if hits := ix.Query([]string{"abc"}, 10); len(hits) != 1 {
		t.Fatalf("single-trigram term 'abc' should still match the line, got %d hits", len(hits))
	}
}

// TestIssue5201CrossBlockFullCoverageStillMatches guards the intended
// semantics: when the blocks TOGETHER cover every trigram of the term
// (here "abcd" split as "xxabc" + "bcdzz" across blocks), the line matches.
// This is the documented trigram-set approximation and must not regress.
func TestIssue5201CrossBlockFullCoverageStillMatches(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s5201d", []string{
		twoBlockLine("user", "xxabc", "bcdzz"),
	})
	ix, err := Load(dir, "s5201d")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	hits := ix.Query([]string{"abcd"}, 10)
	if len(hits) == 0 {
		t.Fatal("line covering all trigrams of 'abcd' across two blocks must match")
	}
	if hits[0].TermBits != 1 {
		t.Fatalf("expected TermBits=1, got %d", hits[0].TermBits)
	}
}

// TestIssue5201PersistenceRoundtripSinglePosting pins the v2 persistence
// half: after saveLocked + Load the line-level dedup survives the disk
// roundtrip — one posting per (line, trigram), so a restart keeps the fix.
func TestIssue5201PersistenceRoundtripSinglePosting(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s5201e", []string{
		twoBlockLine("user", "xxabcxx", "zzabczz"),
	})
	ix, err := Load(dir, "s5201e")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := ix.saveLocked(); err != nil {
		t.Fatal(err)
	}
	ix2, err := Load(dir, "s5201e")
	if err != nil {
		t.Fatal(err)
	}
	ix2.mu.RLock()
	postings := ix2.data.Trigrams["abc"]
	ix2.mu.RUnlock()
	if len(postings) != 1 {
		t.Fatalf("after save+Load roundtrip, trigram 'abc' must still have exactly 1 posting, got %d", len(postings))
	}
	if hits := ix2.Query([]string{"abcd"}, 10); len(hits) != 0 {
		t.Fatalf("BUG #5201 persisted: reloaded index still matches 'abcd' without 'bcd' (hits=%d)", len(hits))
	}
}

// TestIssue5201WithinBlockDedupUnchanged guards the pre-existing per-call
// behavior that already worked: a trigram repeated WITHIN one text block
// yields one posting. Renaming/re-scoping addPostings must not lose it.
func TestIssue5201WithinBlockDedupUnchanged(t *testing.T) {
	tg := map[string][]Posting{}
	seen := map[string]struct{}{}
	addPostings(tg, seen, "abc abc abc", 0, 10, 1, 0)
	if len(tg["abc"]) != 1 {
		t.Fatalf("within-block dedup: expected 1 posting for 'abc', got %d", len(tg["abc"]))
	}
	// The caller-owned seen map must now carry the trigram: a second block
	// with the same trigram must not add another posting.
	addPostings(tg, seen, "zzabczz", 0, 10, 1, 0)
	if len(tg["abc"]) != 1 {
		t.Fatalf("line-level dedup across blocks: expected still 1 posting for 'abc', got %d", len(tg["abc"]))
	}
	// A DIFFERENT line gets its own caller-owned seen map in production
	// (indexLine creates one per record), so its postings append normally.
	seen2 := map[string]struct{}{}
	addPostings(tg, seen2, "zzabczz", 50, 10, 1, 0)
	if len(tg["abc"]) != 2 {
		t.Fatalf("different line must add its own posting: expected 2, got %d", len(tg["abc"]))
	}
}

// TestIssue5201MultiTermQuery checks the AND path with the coverage
// arithmetic on multi-block lines: no term may be satisfied by duplicate
// postings, and terms genuinely covered on a line must hit. AND semantics
// require every term on the SAME line, so both terms live on line 1.
func TestIssue5201MultiTermQuery(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "s5201f", []string{
		twoBlockLine("user", "xxabcxx wxyz", "zzabczz wxyz"),
		msgLine("assistant", "no matching terms here"),
	})
	ix, err := Load(dir, "s5201f")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	// 'abcd' has no fully-covered line at all -> whole AND query returns nil,
	// even though 'wxyz' is covered on line 1.
	if hits := ix.Query([]string{"abcd", "wxyz"}, 10); len(hits) != 0 {
		t.Fatalf("AND query with uncovered term must return 0 hits, got %d", len(hits))
	}
	// Both terms fully covered on the same line -> exactly that line hits.
	hits := ix.Query([]string{"abc", "wxyz"}, 10)
	if len(hits) != 1 {
		t.Fatalf("fully covered AND query expected exactly 1 hit, got %d", len(hits))
	}
	line, err := ix.ReadLineAt(hits[0].Offset, hits[0].Length)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ToLower(string(line))
	if !strings.Contains(s, "abc") || !strings.Contains(s, "wxyz") {
		t.Fatalf("hit line missing a query term: %.80s", s)
	}
	if hits[0].TermBits != 3 {
		t.Fatalf("expected TermBits=3 (both terms), got %d", hits[0].TermBits)
	}
}
