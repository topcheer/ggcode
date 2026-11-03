package memory

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3882_RecordOutcomeHoldsCrossProcessLock(t *testing.T) {
	dir := t.TempDir()
	a := NewProjectAutoMemory(dir)
	b := NewProjectAutoMemory(dir)
	if a == nil || b == nil {
		t.Fatal("auto memory nil")
	}

	for i := 0; i < 20; i++ {
		a.RecordOutcome("concurrent-key", "success")
		b.RecordUse([]string{"other-key"}, "test")
	}

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
