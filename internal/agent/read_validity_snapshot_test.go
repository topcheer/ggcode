package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEventScopedHashSnapshot verifies the sa-35 dedup contract: within one
// tool-call event, multiple detectors fingerprinting the same file share a
// single underlying hash computation; after the event boundary (snapshot
// reset), the file is re-hashed — preserving sub-second race semantics.
// Trackers without an injected hashFn must keep hashing directly.
func TestEventScopedHashSnapshot(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	snap := map[string]uint64{}
	snapshotFn := func(path string) uint64 {
		if h, ok := snap[path]; ok {
			return h
		}
		calls++
		h := hashFilePrefix(path)
		snap[path] = h
		return h
	}

	tr := newReadHashTracker()
	tr.hashFn = snapshotFn
	rr := newRedundantReadState()
	rr.hashFn = snapshotFn

	// One read event: recordReadHash + redundant-read baseline fingerprinting
	// the same path must hash the underlying file exactly once.
	tr.recordReadHash(p)
	rr.recordReadMtimeStat(p, nil) // nil info → recordReadMtime → hashOf
	if calls != 1 {
		t.Fatalf("expected 1 underlying hash within one event, got %d", calls)
	}

	// Event boundary: reset clears the snapshot; the next event re-hashes.
	snap = map[string]uint64{}
	tr.recordReadHash(p)
	if calls != 2 {
		t.Fatalf("expected fresh hash after event reset, got %d underlying calls", calls)
	}

	// nil hashFn falls back to direct hashFilePrefix (backward compat).
	direct := newReadHashTracker()
	direct.recordReadHash(p)
	if _, ok := lookupReadHash(direct, p); !ok {
		t.Fatal("nil hashFn fallback should still record a content hash")
	}

	// recordReadMtimeStat with a caller-provided FileInfo skips the extra stat
	// path but must still pair mtime with a fingerprint.
	rr2 := newRedundantReadState()
	rr2.hashFn = snapshotFn
	if info, err := os.Stat(p); err == nil {
		rr2.recordReadMtimeStat(p, info)
		n := normalizePath(p)
		if _, ok := rr2.lastReadMtime[n]; !ok {
			t.Fatal("recordReadMtimeStat should record mtime from provided FileInfo")
		}
		if _, ok := rr2.lastReadHash[n]; !ok {
			t.Fatal("recordReadMtimeStat should pair mtime with a content fingerprint")
		}
	} else {
		t.Fatalf("stat %s: %v", p, err)
	}
}

// TestAgentFileHashSnapshotReset verifies the Agent-level snapshot lifecycle:
// lazy init, same-path dedup, and full invalidation on resetHashSnapshot.
func TestAgentFileHashSnapshotReset(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.go")
	if err := os.WriteFile(p, []byte("package agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{}
	if a.hashSnapshot != nil {
		t.Fatal("snapshot must start nil (lazy init)")
	}

	h1 := a.fileHashSnapshot(p)
	if h1 == 0 {
		t.Fatal("expected nonzero hash for readable file")
	}
	// Prime the cache directly to observe the dedup path (fileHashSnapshot
	// cannot count internally; a different value proves the map is consulted).
	a.hashSnapshot[p] = 42
	if got := a.fileHashSnapshot(p); got != 42 {
		t.Fatal("fileHashSnapshot must serve repeat lookups from the event snapshot")
	}

	a.resetHashSnapshot()
	if a.hashSnapshot != nil {
		t.Fatal("resetHashSnapshot must drop the snapshot")
	}
	if h2 := a.fileHashSnapshot(p); h2 != h1 {
		t.Fatal("post-reset hash must be recomputed from disk, not the stale entry")
	}

	// Empty path is a no-op (must not allocate or panic).
	if got := a.fileHashSnapshot(""); got != 0 {
		t.Fatal("empty path should return 0")
	}
}
