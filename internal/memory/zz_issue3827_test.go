package memory

// #3827 companion: duplicate_check must not flag same-tokens-different-order
// or subset keys as duplicates (order-sensitive bigram Jaccard), and
// consumption scanning must not count a short key cited only as part of a
// longer sibling key (word boundary).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3827_OrderSwappedKeysNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cmd-build.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	am := &AutoMemory{dir: dir}
	dup := am.CheckDuplicate("build-cmd", "value")
	if dup.IsDuplicate() {
		t.Fatalf("order-swapped key must not be judged duplicate: %+v (old set-Jaccard scored 1.0)", dup)
	}
}

func TestIssue3827_SubsetPrefixKeyNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "http-timeout-detection.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	am := &AutoMemory{dir: dir}
	dup := am.CheckDuplicate("timeout-detection", "value")
	// Old unigram-set Jaccard = 2/3 ≈ 0.667 ≥ 0.6 → false duplicate.
	// Bigram sets {http-timeout, timeout-detection} vs {timeout-detection}
	// = 1/2 = 0.5 < 0.6.
	if dup.IsDuplicate() {
		t.Fatalf("subset key must not be judged duplicate: %+v", dup)
	}
}

func TestIssue3827_GenuinelySimilarOrderedKeysStillFlagged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "build-process.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	am := &AutoMemory{dir: dir}
	dup := am.CheckDuplicate("build-process", "value")
	if !dup.IsDuplicate() || dup.Similarity < 1.0 {
		t.Fatalf("exact key must still be detected at 1.0: %+v", dup)
	}
}

func TestIssue3827_ConsumptionWordBoundary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "release-process.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "release-process", "body")
	forceUses(t, am, "release-process", 2)
	// Citing only the LONGER sibling must NOT count for the short key.
	if got := am.ScanConsumption("see memory release-process-impl for details"); got != 0 {
		t.Fatalf("sibling-key citation must not count: matched=%d", got)
	}
	// Standalone citation must still count.
	if got := am.ScanConsumption("per release-process, run version sync first"); got != 1 {
		t.Fatalf("standalone citation must count: matched=%d", got)
	}
}
