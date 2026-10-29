package tool

// #4545 companion tests for the code_index_manager triple review:
//   B) Stop must not steal the flock from an in-flight builder.
//   C) rebuildDirty's replace path must respect the term budget on the
//      distinct-term delta, and the budget unit must be len(tf) corpus-wide
//      (doBuild always counted distinct terms).
//   D) a first-build timeout must publish the completed subset - the old
//      early return left started=true/ready=false with nothing persisted,
//      a lifetime dead index (MarkDirty swallowed by rebuildDirty's !ready
//      gate, periodicCheck bailed on index==nil).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// B: Stop while a builder holds the lock - the lock must survive Stop and
// remain releasable by the builder's own unlock.
func TestIssue4545_StopDoesNotStealBuildersLock(t *testing.T) {
	dir := t.TempDir()
	m := NewCodeIndexManager(dir)
	defer m.Stop()

	// Simulate an in-flight builder (initial build / lazyLoad / rebuildDirty
	// all hold the flock only transiently and release via defer unlock).
	if !m.tryLock() {
		t.Fatal("tryLock failed on a fresh lock file")
	}
	m.Stop()

	m.mu.RLock()
	held := m.lockFile != nil
	m.mu.RUnlock()
	if !held {
		t.Fatal("#4545: Stop stole the in-flight builder's flock (fd closed early; the builder's own unlock became a nil no-op and cross-process mutual exclusion broke)")
	}

	// The builder's own release still works after Stop.
	m.unlock()
	m.mu.RLock()
	gone := m.lockFile == nil
	m.mu.RUnlock()
	if !gone {
		t.Fatal("builder unlock after Stop must still release the lock")
	}
}

// C: the replace path enforces the term budget on the distinct-term delta -
// repeatedly editing an already-indexed file must not be a #4428 side door.
func TestIssue4545_ReplaceRespectsTermBudget(t *testing.T) {
	dir := t.TempDir()
	writeVars := func(n int) {
		var b strings.Builder
		b.WriteString("package p\n")
		for j := 0; j < n; j++ {
			b.WriteString("var budgetVar" + itoa(j) + " int\n")
		}
		if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(b.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}

	writeVars(40)
	m := NewCodeIndexManager(dir)
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", "-1") // unlimited baseline build
	defer m.Stop()
	m.doBuild(context.Background())

	m.mu.RLock()
	base := len(m.index.docs[0].tf)
	m.mu.RUnlock()
	if base == 0 {
		t.Fatal("baseline index empty")
	}

	// Phase 1: growth within budget -> replace applies (exactly +3 distinct
	// terms over the 40-var baseline; the tokenizer's fixed keyword overhead
	// is part of measured base, so the delta is exact).
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", itoa(base+10))
	writeVars(43)
	m.MarkDirty([]string{"big.go"})
	m.rebuildDirty("test")
	m.mu.RLock()
	got := len(m.index.docs[0].tf)
	m.mu.RUnlock()
	if got != base+3 {
		t.Fatalf("within-budget replace must apply: tf=%d want %d", got, base+3)
	}

	// Phase 2: growth far beyond budget -> replace skipped, old doc kept
	// (47 new distinct terms vs a headroom of 7).
	writeVars(90)
	m.MarkDirty([]string{"big.go"})
	m.rebuildDirty("test")
	m.mu.RLock()
	got2 := len(m.index.docs[0].tf)
	m.mu.RUnlock()
	if got2 != base+3 {
		t.Fatalf("#4545: beyond-budget replace must be skipped (#4428 side door), tf=%d want %d (old doc)", got2, base+3)
	}
}

// D: a first-build timeout publishes the indexed subset instead of leaving a
// lifetime-dead index for this process.
func TestIssue4545_TimeoutPublishesIndexedSubset(t *testing.T) {
	dir := t.TempDir()
	// ~600k distinct terms across 100 files (~12MB): far beyond what a 50ms
	// deadline can index, so the timeout is guaranteed to fire mid-loop with
	// a substantial subset already accumulated.
	for i := 0; i < 100; i++ {
		var b strings.Builder
		b.WriteString("package p\n")
		for j := 0; j < 6000; j++ {
			b.WriteString("var slowVar" + itoa(i) + "_" + itoa(j) + " int\n")
		}
		if err := os.WriteFile(filepath.Join(dir, "s"+itoa(i)+".go"), []byte(b.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := NewCodeIndexManager(dir)
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", "-1") // isolate the timeout path from budget truncation
	defer m.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	m.doBuild(ctx)

	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.ready || m.index == nil {
		t.Fatal("#4545: first-build timeout left the index permanently dead - the completed subset must be published")
	}
	if n := len(m.index.docs); n == 0 || n >= 100 {
		t.Fatalf("#4545: expected a partial subset (0 < docs < 100), got %d", n)
	}
}
