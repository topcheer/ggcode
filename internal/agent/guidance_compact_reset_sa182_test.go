package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sa-182: after a context compaction, the file baselines held by
// redundantReadState refer to read results that may no longer be in context
// (compaction replaces raw tool results with summaries). The post-compaction
// reset table must re-arm the guard, otherwise a re-read used for
// re-grounding is mis-flagged as redundant "context waste" — pushing the
// agent to rely on context that was just compacted away and setting up an
// unread-edit-guard conflict loop on the next edit attempt.
func TestRedundantReadRearmedAfterCompactionReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	// Above redundantReadMinSize (2KB) so the guard actually evaluates it.
	body := strings.Repeat("// filler line for size\n", 120)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	a := &Agent{redundantRead: newRedundantReadState()}

	// First read seeds the baseline: no hint.
	if hint := a.redundantRead.checkRedundantRead(path, false); hint != "" {
		t.Fatalf("first read should not warn, got: %s", hint)
	}
	// Second read with no intervening change: flagged as redundant.
	if hint := a.redundantRead.checkRedundantRead(path, false); hint == "" {
		t.Fatal("second unchanged read should be flagged as redundant")
	}

	// Compaction happens: prior read results may have been dropped from
	// context. resetGuidanceCounters must re-arm redundantRead.
	a.resetGuidanceCounters()

	// Re-read after compaction: legitimate re-grounding, must not warn.
	if hint := a.redundantRead.checkRedundantRead(path, false); hint != "" {
		t.Fatalf("re-read after compaction reset should not warn, got: %s", hint)
	}
	// The guard must be functional again for a genuine unchanged re-read.
	if hint := a.redundantRead.checkRedundantRead(path, false); hint == "" {
		t.Fatal("guard should re-arm and flag unchanged re-reads after reset")
	}
}

// Nil-safety: resetGuidanceCounters must tolerate a zero-value Agent whose
// guard states were never initialized (table entries are all nil-checked).
func TestCompactionResetNilSafe(t *testing.T) {
	a := &Agent{}
	a.resetGuidanceCounters() // must not panic
}
