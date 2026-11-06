package sessionidx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #5156 R1 pins: total-decoded-bytes admission with LRU eviction. The
// per-file externalLimit alone is not a memory bound - the total budget
// is. These tests drive lazy admission + eviction through the public
// Manager API with small files (budget in bytes, not MB).

func seedIdx(t *testing.T, dir, id string, lines int) {
	t.Helper()
	// sessions layout: <dir>/<id>.jsonl (source) and <dir>/idx/<id>.idx
	src := filepath.Join(dir, id+".jsonl")
	f, err := os.Create(src)
	if err != nil {
		t.Fatalf("create %s: %v", src, err)
	}
	for i := 0; i < lines; i++ {
		_, _ = f.WriteString(`{"type":"message","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"token-alpha token-beta token-gamma"}]}}` + "\n")
	}
	f.Close()
	ix, err := Load(dir, id)
	if err != nil {
		t.Fatalf("load %s: %v", id, err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatalf("refresh %s: %v", id, err)
	}
}

func TestTotalBudget_OverCapFileNeverAdmitted(t *testing.T) {
	dir := t.TempDir()
	seedIdx(t, dir, "huge", 200)
	// budget smaller than the idx file itself
	st, err := os.Stat(IdxPath(dir, "huge"))
	if err != nil {
		t.Fatalf("stat idx: %v", err)
	}
	m := NewManager(dir, "")
	m.SetTotalBudget(st.Size() - 1) // file alone exceeds budget
	m.scanExternal()
	if got := m.TotalDecodedBytes(); got != 0 {
		t.Fatalf("over-cap file must never be decoded: totalDecoded=%d", got)
	}
	// And querying must not decode it either.
	_ = m.Query([]string{"token-alpha"}, "", 5)
	if got := m.TotalDecodedBytes(); got != 0 {
		t.Fatalf("query must not admit over-cap file: totalDecoded=%d", got)
	}
}

func TestTotalBudget_LRUEvictionAndEvictionsCounter(t *testing.T) {
	dir := t.TempDir()
	seedIdx(t, dir, "aaa", 50)
	seedIdx(t, dir, "bbb", 50)
	// Both fit individually; budget fits only one at a time.
	stA, _ := os.Stat(IdxPath(dir, "aaa"))
	stB, _ := os.Stat(IdxPath(dir, "bbb"))
	budget := stA.Size()
	if stB.Size() > budget {
		budget = stB.Size()
	}
	m := NewManager(dir, "")
	m.SetTotalBudget(budget)
	m.scanExternal()

	// Query session aaa specifically -> decodes aaa only.
	_ = m.Query([]string{"token-alpha"}, "aaa", 3)
	if m.TotalDecodedBytes() != stA.Size() {
		t.Fatalf("after aaa query decoded=%d want %d", m.TotalDecodedBytes(), stA.Size())
	}
	// Query bbb -> must evict aaa (LRU) to fit.
	_ = m.Query([]string{"token-alpha"}, "bbb", 3)
	evictedBytes, evictedCount := m.Evictions()
	if evictedCount == 0 {
		t.Fatal("expected at least one LRU eviction when the second index was admitted")
	}
	if evictedBytes == 0 {
		t.Fatal("evicted bytes must be reported for /status")
	}
	if m.TotalDecodedBytes() > budget {
		t.Fatalf("total decoded %d exceeds budget %d", m.TotalDecodedBytes(), budget)
	}
}

func TestTotalBudget_RefreshReDecodesLazily(t *testing.T) {
	dir := t.TempDir()
	seedIdx(t, dir, "ccc", 40)
	m := NewManager(dir, "")
	m.SetTotalBudget(1 << 20)
	m.scanExternal()
	_ = m.Query([]string{"token-alpha"}, "ccc", 2)
	if m.TotalDecodedBytes() == 0 {
		t.Fatal("ccc should be decoded after query")
	}
	// Touch the idx file with a newer mtime+size and refresh: decoded
	// copy must be invalidated (re-decode on next query, not eagerly).
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(IdxPath(dir, "ccc"), future, future)
	m.refreshExternal()
	// decoded flag was reset; totalDecoded must have been decremented.
	if got := m.TotalDecodedBytes(); got != 0 {
		t.Fatalf("refresh must invalidate decoded copy lazily: totalDecoded=%d", got)
	}
	_ = m.Query([]string{"token-alpha"}, "ccc", 2)
	if m.TotalDecodedBytes() == 0 {
		t.Fatal("ccc must re-decode on next query after refresh")
	}
}
