package sessionidx

import (
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
)

// #5156: Load must distinguish a genuinely CORRUPT idx (gob decode fails ->
// the shared daemon degrades that session to a source JSONL scan) from a
// merely STALE one (idxVersion mismatch -> the owning instance rebuilds on
// its next Refresh; readers skip it). Conflating them would turn every
// format upgrade into a machine-wide source-scan storm.

func TestLoadFlagsCorruptIdx(t *testing.T) {
	dir := t.TempDir()
	// A source exists so the path is realistic.
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := os.MkdirAll(IdxDir(dir), 0o700); err != nil {
		t.Fatalf("mkdir idx: %v", err)
	}
	// Garbage bytes that gob cannot decode.
	if err := os.WriteFile(IdxPath(dir, "s1"), []byte("not-a-gob-stream"), 0o600); err != nil {
		t.Fatalf("write corrupt idx: %v", err)
	}

	ix, err := Load(dir, "s1")
	if err != nil {
		t.Fatalf("Load must not fail on a corrupt idx: %v", err)
	}
	if !ix.corrupt {
		t.Fatal("corrupt idx must set ix.corrupt (daemon degrades to source scan)")
	}
	if ix.stale {
		t.Fatal("corrupt idx must not be reported as merely stale")
	}
}

func TestLoadFlagsStaleIdxOnVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s2.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if err := os.MkdirAll(IdxDir(dir), 0o700); err != nil {
		t.Fatalf("mkdir idx: %v", err)
	}
	// A well-formed gob stream carrying a future idxVersion.
	f, err := os.Create(IdxPath(dir, "s2"))
	if err != nil {
		t.Fatalf("create idx: %v", err)
	}
	d := indexFile{Version: idxVersion + 1, Trigrams: map[string][]Posting{"abc": {{Offset: 0, Length: 2}}}}
	if err := gob.NewEncoder(f).Encode(&d); err != nil {
		f.Close()
		t.Fatalf("encode: %v", err)
	}
	f.Close()

	ix, err := Load(dir, "s2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.corrupt {
		t.Fatal("version mismatch must NOT be flagged corrupt (would trigger source-scan storm)")
	}
	if !ix.stale {
		t.Fatal("version mismatch must set ix.stale")
	}
	if isCorruptIdx(ix, 100) {
		t.Fatal("isCorruptIdx must be false for a stale (not corrupt) index")
	}
}
