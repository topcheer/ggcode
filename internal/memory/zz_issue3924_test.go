package memory

// #3924 probe: recreating a deleted key must invalidate the tombstone -
// as_of after the recreate resolves the NEW content, as_of inside the
// deleted gap resolves nothing.

import (
	"testing"
	"time"
)

func TestIssue3924_TombstoneInvalidatedOnRecreate(t *testing.T) {
	dir := t.TempDir()
	am := NewProjectAutoMemory(dir)
	if am == nil {
		t.Fatal("auto memory nil")
	}
	if err := am.SaveMemory("phoenix", "v1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := am.DeleteMemory("phoenix"); err != nil {
		t.Fatal(err)
	}
	delAt := time.Now()
	time.Sleep(10 * time.Millisecond)

	// Deleted gap: nothing.
	if _, _, found, _ := am.ReadMemoryAsOf("phoenix", delAt); found {
		t.Fatal("as_of in the deleted gap must find nothing")
	}

	// Recreate.
	time.Sleep(10 * time.Millisecond)
	if err := am.SaveMemory("phoenix", "v2-recreated"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	afterRecreate := time.Now()

	// After the recreate: the NEW content, not found=false.
	content, _, found, err := am.ReadMemoryAsOf("phoenix", afterRecreate)
	if err != nil || !found {
		t.Fatalf("as_of after recreate must resolve the new version: found=%v err=%v", found, err)
	}
	if content != "v2-recreated" {
		t.Fatalf("expected v2-recreated, got %q", content)
	}

	// The gap instant STILL resolves nothing (the recreate must not
	// retroactively resurrect the deleted interval).
	if _, _, found, _ := am.ReadMemoryAsOf("phoenix", delAt); found {
		t.Fatal("the deleted gap must stay empty even after a recreate")
	}
}
