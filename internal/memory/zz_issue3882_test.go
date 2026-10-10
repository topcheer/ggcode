package memory

// #3882 probe: RecordOutcome holds the cross-process lock during its
// read-modify-write of .usage.json (the 3120 contract), so a concurrent
// RecordUse from a second AutoMemory instance (simulated second process)
// cannot be lost to last-writer-wins.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3882_RecordOutcomeHoldsCrossProcessLock(t *testing.T) {
	dir := t.TempDir()
	a := NewProjectAutoMemory(dir)
	b := NewProjectAutoMemory(dir) // "second process"
	if a == nil || b == nil {
		t.Fatal("auto memory nil")
	}

	// Interleave outcome writes (instance a) with usage writes (instance b).
	for i := 0; i < 20; i++ {
		a.RecordOutcome("concurrent-key", "success")
		b.RecordUse([]string{"other-key"}, "test")
	}

	// Both records must survive: the RMW under FileLock must not clobber
	// the sibling writer's entries.
	idx := a.loadUsage()
	if idx.Entries["concurrent-key"] == nil {
		t.Fatal("outcome entry lost")
	}
	if idx.Entries["other-key"] == nil {
		t.Fatal("concurrent RecordUse entry lost to last-writer-wins (lock missing)")
	}
}

func TestIssue3882_LockOrderSourceContract(t *testing.T) {
	raw, err := os.ReadFile("usage.go")
	if err != nil {
		t.Fatal(err)
	}
	fnStart := strings.Index(string(raw), "func (am *AutoMemory) RecordOutcome")
	if fnStart < 0 {
		t.Fatal("RecordOutcome not found")
	}
	fn := string(raw)[fnStart:]
	lockAt := strings.Index(fn, "util.FileLock(am.dir")
	muAt := strings.Index(fn, "am.mu.Lock()")
	if lockAt < 0 || muAt < 0 || lockAt > muAt {
		t.Fatal("RecordOutcome must acquire util.FileLock before am.mu (lock outer, mutex inner)")
	}
}
