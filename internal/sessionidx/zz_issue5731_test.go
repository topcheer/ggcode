package sessionidx

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// zz_issue5731_test.go pins the grouped in-memory posting store.
//
// Evidence (issue #5731, memstats loop, pid 62415): addPostings held
// inuse=211959784 (212MB, 1337 live objects ~159KB each - []Posting append
// growth reallocations) while the instance climbed 1675->2225MB inuse
// (+550MB/2min sustained). Root cause: every (trigram, line) posting
// carried a full 24-byte copy of the line Offset/Length/Role/TsNano, so
// the in-memory map duplicated the line metadata per posting. The v2 disk
// format had already proven the fix shape (#5149: 938MB -> ~120MB, ~8x) by
// storing line metadata once + 4B trigram ids per line. #5731 adopts the
// same grouped shape IN MEMORY: postings are 4B line ids into ix.lines.

// msgLineAt is msgLine with a distinct timestamp (ordering assertions).
func msgLineAt(role, text, ts string) string {
	return fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":%q,"content":[{"type":"text","text":%q}]}}`, ts, role, text)
}

// countedPostings returns the number of postings for a trigram.
func countedPostings(ix *Index, tg string) int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	id, ok := ix.triID[tg]
	if !ok {
		return 0
	}
	return len(ix.posts[id])
}

// TestIssue5731GroupedStoreCutsResidentPostingHeap is the profile-confirmed
// reduction target: the retained heap per posting must stay far below the
// pre-fix 24B+ Posting duplication. Pre-fix this measures ~30B/posting
// (24B Posting + append slack); the grouped store pins at <= 12B/posting
// (4B ids + slack + one 24B lineRec per line + trigram keys).
func TestIssue5731GroupedStoreCutsResidentPostingHeap(t *testing.T) {
	// Repetitive corpus over a small distinct-trigram set: the shape that
	// maximized the pre-fix blowup (every posting duplicated metadata while
	// trigram keys stayed few).
	const nLines = 4000
	const sentence = "the quick brown fox jumps over the lazy dog keeping the session index compact "
	dir := t.TempDir()
	id := "20261117-023000-aaaa1111bbbb2222"
	var sb strings.Builder
	for i := 0; i < nLines; i++ {
		sb.WriteString(msgLineAt("user", sentence, "2026-11-17T02:30:00Z"))
		sb.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	ix.mu.RLock()
	postings := 0
	for _, ls := range ix.posts {
		postings += len(ls)
	}
	nLinesIndexed := len(ix.lines)
	nTrig := len(ix.triID)
	ix.mu.RUnlock()
	if postings < 100000 {
		t.Fatalf("corpus too small for a meaningful pin: %d postings", postings)
	}
	if nLinesIndexed != nLines {
		t.Fatalf("expected %d indexed lines, got %d", nLines, nLinesIndexed)
	}
	delta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if delta < 0 {
		delta = 0
	}
	perPosting := float64(delta) / float64(postings)
	t.Logf("#5731: postings=%d distinctTrigrams=%d retainedHeapDelta=%dB (%.2f B/posting)",
		postings, nTrig, delta, perPosting)
	if perPosting > 12 {
		t.Fatalf("#5731: resident posting heap %.1fB/posting exceeds the 12B grouped bound - per-posting Posting duplication is back", perPosting)
	}
	// Budget arithmetic must be unchanged: Size keeps the 40B/posting
	// counting unit, so approxBytes still tracks 40B per posting.
	if want := postings * 40; ix.Size() < want {
		t.Fatalf("Size()=%d below 40B/posting basis (%d postings)", ix.Size(), postings)
	}
}

// TestIssue5731QueryRoundtripTailAndMetadata guards the functional contract
// of the grouped store end to end: query correctness, Hit metadata
// (offset/length/role/ts now served from the shared line records),
// newest-first ordering, persistence roundtrip, and incremental tailing on
// top of a loaded grouped index (line-id continuity).
func TestIssue5731QueryRoundtripTailAndMetadata(t *testing.T) {
	dir := t.TempDir()
	id := "20261117-023100-aaaa1111bbbb2222"
	writeSession(t, dir, id, []string{
		msgLineAt("user", "alpha beta gamma sessionidx memory fix", "2026-11-17T02:31:00Z"),
		msgLineAt("assistant", "unrelated chatter about coffee", "2026-11-17T02:32:00Z"),
		msgLineAt("user", "the addPostings heap reduction target lands", "2026-11-17T02:33:00Z"),
	})
	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}

	hits := ix.Query([]string{"sessionidx"}, 10)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit for sessionidx term, got %d", len(hits))
	}
	src, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	linesIn := strings.Split(strings.TrimRight(string(src), "\n"), "\n")
	if hits[0].Offset != 0 {
		t.Fatalf("hit offset %d, want 0 (first line)", hits[0].Offset)
	}
	if hits[0].Length != int32(len(linesIn[0])) {
		t.Fatalf("hit length %d, want %d", hits[0].Length, len(linesIn[0]))
	}
	if hits[0].Role != 1 {
		t.Fatalf("hit role %d, want 1 (user)", hits[0].Role)
	}
	wantTs := time.Date(2026, 11, 17, 2, 31, 0, 0, time.UTC).UnixNano()
	if hits[0].TsNano != wantTs {
		t.Fatalf("hit ts %d, want %d (metadata must come from the line record)", hits[0].TsNano, wantTs)
	}
	// ReadLineAt must reconstruct the exact source line from the metadata.
	line, err := ix.ReadLineAt(hits[0].Offset, hits[0].Length)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), "sessionidx memory fix") {
		t.Fatalf("ReadLineAt returned wrong line: %.80s", line)
	}

	// Persistence roundtrip: Load again from disk and re-verify.
	ix2, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	hits2 := ix2.Query([]string{"sessionidx"}, 10)
	if len(hits2) != 1 || hits2[0].Offset != 0 || hits2[0].TsNano != wantTs {
		t.Fatalf("grouped roundtrip changed hits: %+v", hits2)
	}

	// Incremental tail on top of the loaded index: line ids must continue
	// the loaded sequence, not restart.
	ix2.mu.RLock()
	linesBefore := len(ix2.lines)
	ix2.mu.RUnlock()
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	newest := msgLineAt("user", "sessionidx newest tail line", "2026-11-17T02:34:00Z")
	if _, err := f.WriteString(newest + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := ix2.Refresh(); err != nil {
		t.Fatal(err)
	}
	ix2.mu.RLock()
	linesAfter := len(ix2.lines)
	lastOffset := int64(0)
	if linesAfter > 0 {
		lastOffset = ix2.lines[linesAfter-1].Offset
	}
	ix2.mu.RUnlock()
	if linesAfter != linesBefore+1 {
		t.Fatalf("tail must add exactly 1 line record: %d -> %d", linesBefore, linesAfter)
	}
	// Both sessionidx lines match: the appended line AND the original one.
	// (Query iterates a map accumulator, so multi-hit order is not
	// deterministic - pre-existing behavior; assert the offset set.)
	hits3 := ix2.Query([]string{"sessionidx"}, 10)
	if len(hits3) != 2 {
		t.Fatalf("expected 2 hits after tail, got %d", len(hits3))
	}
	got := map[int64]bool{}
	for _, h := range hits3 {
		got[h.Offset] = true
	}
	if !got[0] || !got[lastOffset] {
		t.Fatalf("hits must cover the original line (0) and the appended line (%d), got %v", lastOffset, got)
	}
}

// TestIssue5731LegacyMapPayloadConvertsToGrouped pins the compatibility
// path: a Version==2 idx whose gob still carries the v1 Trigrams map (the
// in-memory shape before this fix was also the transient wire shape) must
// convert into the grouped store at Load and answer queries correctly.
func TestIssue5731LegacyMapPayloadConvertsToGrouped(t *testing.T) {
	dir := t.TempDir()
	id := "20261117-023200-aaaa1111bbbb2222"
	os.MkdirAll(IdxDir(dir), 0o700)
	d := indexFile{
		Version:   idxVersion,
		IndexedTo: 400,
		Trigrams: map[string][]Posting{
			"abc": {{Offset: 0, Length: 30, Role: 1, TsNano: 111}, {Offset: 200, Length: 30, Role: 2, TsNano: 222}},
			"bcd": {{Offset: 200, Length: 30, Role: 2, TsNano: 222}},
		},
	}
	fw, err := os.Create(IdxPath(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(fw).Encode(d); err != nil {
		t.Fatal(err)
	}
	fw.Close()

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Capped() || ix.corrupt || ix.stale {
		t.Fatal("legacy map payload must load as a usable index")
	}
	ix.mu.RLock()
	nTrig := len(ix.triID)
	nPost := len(ix.posts[ix.triID["abc"]]) + len(ix.posts[ix.triID["bcd"]])
	nLines := len(ix.lines)
	ix.mu.RUnlock()
	if nTrig != 2 || nPost != 3 {
		t.Fatalf("grouped conversion wrong: trigrams=%d postings(abc+bcd)=%d", nTrig, nPost)
	}
	if nLines != 2 || ix.lines[0].Offset != 0 || ix.lines[1].Offset != 200 {
		t.Fatal("conversion must materialize offset-ascending line records")
	}
	// abc hits both lines; abcd needs abc AND bcd on one line -> only 200.
	if hits := ix.Query([]string{"abc"}, 10); len(hits) != 2 {
		t.Fatalf("abc term expected 2 hits, got %d", len(hits))
	}
	if hits := ix.Query([]string{"abcd"}, 10); len(hits) != 1 || hits[0].Offset != 200 {
		t.Fatalf("abcd term expected exactly the offset-200 line, got %+v", hits)
	}
	// Metadata must survive conversion (multi-hit order is map-random).
	for _, h := range ix.Query([]string{"abc"}, 10) {
		wantTs := int64(111)
		if h.Offset == 200 {
			wantTs = 222
		}
		if h.TsNano != wantTs {
			t.Fatalf("line at %d carries ts %d, want %d", h.Offset, h.TsNano, wantTs)
		}
	}
	// The converted index must persist in grouped v2 form and reload.
	if err := ix.saveLocked(); err != nil {
		t.Fatal(err)
	}
	ix2, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if hits := ix2.Query([]string{"abcd"}, 10); len(hits) != 1 || hits[0].Offset != 200 {
		t.Fatalf("post-conversion roundtrip broke coverage: %+v", hits)
	}
}

// TestIssue5731LoadDuplicateTriIDsKeepsQueryDedup pins the Load-side
// integrity on legacy files whose lineRec.TriIDs carry pre-#5201
// duplicates: the rebuilt grouped store then holds the same line id twice
// in a trigram list, and Query distinct-trigram scoring must still refuse
// to substitute duplicates for a missing trigram.
func TestIssue5731LoadDuplicateTriIDsKeepsQueryDedup(t *testing.T) {
	dir := t.TempDir()
	id := "20261117-023300-aaaa1111bbbb2222"
	os.MkdirAll(IdxDir(dir), 0o700)
	d := indexFile{
		Version:   idxVersion,
		IndexedTo: 100,
		Dict:      []string{"abc"},
		Lines: []lineRec{
			{Offset: 0, Length: 30, Role: 1, TsNano: 1, TriIDs: []uint32{0, 0}}, // duplicate
		},
	}
	fw, err := os.Create(IdxPath(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(fw).Encode(d); err != nil {
		t.Fatal(err)
	}
	fw.Close()

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if n := countedPostings(ix, "abc"); n != 2 {
		t.Fatalf("legacy duplicate must load faithfully: 2 postings, got %d", n)
	}
	if hits := ix.Query([]string{"abcd"}, 10); len(hits) != 0 {
		t.Fatalf("duplicate legacy postings must not substitute for missing bcd trigram (hits=%d)", len(hits))
	}
	if hits := ix.Query([]string{"abc"}, 10); len(hits) != 1 {
		t.Fatalf("abc term itself must still match, got %d hits", len(hits))
	}
}

// TestIssue5731TruncationRebuildsGroupedStore pins the full-rebuild path on
// the new shape: a shrunk source must reset the grouped store (triID, posts,
// lines) instead of leaving stale line ids pointing at dead offsets.
func TestIssue5731TruncationRebuildsGroupedStore(t *testing.T) {
	dir := t.TempDir()
	id := "20261117-023400-aaaa1111bbbb2222"
	writeSession(t, dir, id, []string{
		msgLine("user", "first line keeps the keeper token"),
		msgLine("user", "second line carries the dropped token"),
	})
	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if hits := ix.Query([]string{"dropped"}, 10); len(hits) != 1 {
		t.Fatalf("pre-truncation: dropped term expected 1 hit, got %d", len(hits))
	}
	// Truncate to the first line only.
	src := filepath.Join(dir, id+".jsonl")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	cut := strings.Index(string(data), "\n") + 1
	if err := os.WriteFile(src, data[:cut], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if hits := ix.Query([]string{"dropped"}, 10); len(hits) != 0 {
		t.Fatalf("post-truncation: dropped term must be gone, got %d hits", len(hits))
	}
	if hits := ix.Query([]string{"keeper"}, 10); len(hits) != 1 {
		t.Fatalf("post-truncation: keeper term must still hit once, got %d", len(hits))
	}
	ix.mu.RLock()
	nLines := len(ix.lines)
	nTrig := len(ix.triID)
	ix.mu.RUnlock()
	if nLines != 1 {
		t.Fatalf("rebuild must leave exactly 1 line record, got %d", nLines)
	}
	if nTrig == 0 {
		t.Fatal("rebuild must re-ingest the surviving line")
	}
}

// TestIssue5731CappedRefreshDropsGroupedStore extends the #5710 pins to the
// grouped shape: crossing the self budget mid-build must empty triID,
// posts AND lines together (line ids without line records are garbage).
func TestIssue5731CappedRefreshDropsGroupedStore(t *testing.T) {
	// ~45B budget: approxBytes charges 40B per text byte, so the first
	// message line already crosses it mid-Refresh.
	t.Setenv("GGCODE_SESSIONIDX_SELF_BUDGET", "45")
	dir := t.TempDir()
	id := "20261117-023500-aaaa1111bbbb2222"
	writeSession(t, dir, id, []string{
		msgLine("user", "this line is long enough to blow the tiny budget immediately"),
		msgLine("user", "never indexed at all"),
	})
	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	ix.SetSelfBudget(DefaultSelfBudget())
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if !ix.Capped() {
		t.Fatal("tiny budget must cap the build")
	}
	ix.mu.RLock()
	nTrig := len(ix.triID)
	nPost := len(ix.posts)
	nLines := len(ix.lines)
	indexedTo := ix.indexedTo
	ix.mu.RUnlock()
	if nTrig != 0 || nPost != 0 || nLines != 0 {
		t.Fatalf("capped build must drop the whole grouped store: triID=%d posts=%d lines=%d", nTrig, nPost, nLines)
	}
	if indexedTo == 0 {
		t.Fatal("IndexedTo must keep advancing so the tail is never re-decoded")
	}
	if ix.Query([]string{"budget"}, 10) != nil {
		t.Fatal("capped index must serve no queries")
	}
}
