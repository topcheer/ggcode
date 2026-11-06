package sessionidx

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
)

// ---------- ③ dead-idx cleanup ----------

// A persisted idx past the skip threshold is permanently useless (every
// loader caps; capped Refresh is a no-op): Load must DELETE it instead of
// leaving frozen gigabytes on disk, and a second Load must behave as if the
// file never existed.
func TestDeadIdx_DeletedOnLoad(t *testing.T) {
	dir := t.TempDir()
	const id = "deadidx"
	seedSelfCapSession(t, dir, id, 10)

	old := idxSkipDecodeBytes
	idxSkipDecodeBytes = 1024
	defer func() { idxSkipDecodeBytes = old }()

	if err := os.MkdirAll(IdxDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 4096)
	if err := os.WriteFile(IdxPath(dir, id), big, 0o600); err != nil {
		t.Fatal(err)
	}

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if !ix.Capped() {
		t.Fatal("oversized idx must load capped")
	}
	if _, err := os.Stat(IdxPath(dir, id)); !os.IsNotExist(err) {
		t.Fatalf("dead idx must be deleted on load, stat err=%v", err)
	}

	// Second load: file gone -> behaves like a fresh index.
	ix2, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if ix2.Capped() {
		t.Fatal("missing file must load as fresh (uncapped) index")
	}
}

// ---------- ② grouped v2 format ----------

// Round-trip: Refresh persists v2 (dict+lines); reload rebuilds the same
// in-memory map; queries still hit; the on-disk size beats the v1 constant
// (~23B/posting) by a wide margin.
func TestGroupedV2_RoundTripAndShrink(t *testing.T) {
	dir := t.TempDir()
	const id = "v2a"
	seedSelfCapSession(t, dir, id, 40)

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	sizeMem := ix.Size()
	if sizeMem <= 0 {
		t.Fatal("seed must produce postings")
	}

	// Reload from the persisted v2 file.
	ix2, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if ix2.stale {
		t.Fatal("v2 file must not be stale")
	}
	if got := ix2.Size(); got != sizeMem {
		t.Fatalf("round-trip size mismatch: %d vs %d", got, sizeMem)
	}
	if len(ix2.Query([]string{"unicorns"}, 10)) == 0 {
		t.Fatal("query must hit after v2 round-trip")
	}

	// Shrink pin: file bytes must beat v1's measured ~23B/posting with
	// room to spare (grouped: ~2-6B/posting).
	fi, err := os.Stat(IdxPath(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	postings := sizeMem / 40
	if fi.Size() >= int64(postings)*15 {
		t.Fatalf("v2 file %dB >= 15B/posting x %d - grouped encoding not effective", fi.Size(), postings)
	}
}

// Legacy v1 (or any foreign version) loads stale and the owner-style
// Refresh rebuilds it in v2 (user order: no migration, direct rebuild).
func TestStaleForeignVersionRebuildsV2(t *testing.T) {
	dir := t.TempDir()
	const id = "v1x"
	seedSelfCapSession(t, dir, id, 20)

	if err := os.MkdirAll(IdxDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(IdxPath(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(f).Encode(&indexFile{Version: 99, IndexedTo: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if !ix.stale {
		t.Fatal("foreign version must load stale")
	}
	// Refresh rebuilds from offset 0 (stale loads carry fresh empty data).
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if ix.Size() <= 0 {
		t.Fatal("rebuild must produce postings")
	}
	ix2, err := Load(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if ix2.stale {
		t.Fatal("rebuilt file must load clean")
	}
}

// ---------- ① pure client + host tail ----------

func TestPureClientProbeSkipsSelfBuild(t *testing.T) {
	dir := t.TempDir()
	seedSelfCapSession(t, dir, "live1", 10)

	m1 := NewManager(dir, "live1")
	m1.SetSelfProbe(func() bool { return true })
	m1.Start()
	if m1.Self() != nil {
		t.Fatal("probe alive: self index must be skipped (pure client)")
	}
	m1.Stop()

	m2 := NewManager(dir, "live1")
	m2.SetSelfProbe(func() bool { return false })
	m2.Start()
	if m2.Self() == nil {
		t.Fatal("probe dead: legacy local self build expected")
	}
	m2.Stop()
}

func TestTailMissingSources(t *testing.T) {
	dir := t.TempDir()
	// a) session with an existing idx: untouched by the tail.
	seedSelfCapSession(t, dir, "tail_old", 20)
	ix, err := Load(dir, "tail_old")
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	fi0, err := os.Stat(IdxPath(dir, "tail_old"))
	if err != nil {
		t.Fatal(err)
	}
	// b) jsonl-only session: the tail builds it.
	seedSelfCapSession(t, dir, "tail_new", 20)
	// c) oversized source: skipped (admission bound).
	seedSelfCapSession(t, dir, "tail_big", 5)
	big := make([]byte, 64<<10)
	if err := os.WriteFile(filepath.Join(dir, "tail_big.jsonl"), big, 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir, "")
	built := m.TailMissingSources(32 << 10) // 32KB bound: skips tail_big
	if built != 1 {
		t.Fatalf("expected exactly 1 build, got %d", built)
	}
	if _, err := os.Stat(IdxPath(dir, "tail_new")); err != nil {
		t.Fatalf("tail must build the missing idx: %v", err)
	}
	fi1, err := os.Stat(IdxPath(dir, "tail_old"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi1.ModTime().Equal(fi0.ModTime()) {
		t.Fatal("existing idx must stay writer-owned (mtime untouched)")
	}
	if _, err := os.Stat(IdxPath(dir, "tail_big")); !os.IsNotExist(err) {
		t.Fatal("oversized source must be skipped by the tail")
	}
}
