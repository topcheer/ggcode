package sessionidx

import (
	"os"
	"path/filepath"
	"testing"
)

// Real-file probe (read-only): proves the skip-decode path fires on the
// actual oversized production idx. Gated by GGCODE_REAL_IDX_PROBE so CI
// never touches the developer's home dir (same contract as #5168).
func TestRealProbe_SkipDecodeOnProductionIdx(t *testing.T) {
	dir := os.Getenv("GGCODE_REAL_IDX_PROBE")
	if dir == "" {
		t.Skip("GGCODE_REAL_IDX_PROBE not set")
	}
	entries, err := os.ReadDir(IdxDir(dir))
	if err != nil {
		t.Fatalf("readdir idx/: %v", err)
	}
	var probed, total, invalid int
	var biggest string
	var biggestSz int64
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && fi.Size() > biggestSz && filepath.Ext(e.Name()) == ".idx" {
			biggestSz = fi.Size()
			biggest = e.Name()
		}
		id := filepath.Base(e.Name())
		if filepath.Ext(id) != ".idx" {
			continue
		}
		id = id[:len(id)-4]
		total++
		if !ValidSessionID(id) {
			invalid++
			continue
		}
		ix, err := Load(dir, id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if fi, _ := os.Stat(IdxPath(dir, id)); fi != nil && fi.Size() > idxSkipDecodeBytes {
			probed++
			if !ix.Capped() {
				t.Errorf("%s (%dMB): expected capped=true (skip-decode)", id, fi.Size()>>20)
			}
			if ix.Size() != 0 {
				t.Errorf("%s: expected zero decoded postings, got %d", id, ix.Size())
			}
		}
	}
	if probed == 0 {
		t.Skipf("no oversized idx files probed: total=%d invalid=%d biggest=%s(%dMB)", total, invalid, biggest, biggestSz>>20)
	}
	t.Logf("probe OK: %d oversized idx files skipped decode", probed)
}
