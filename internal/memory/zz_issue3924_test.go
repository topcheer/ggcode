package memory

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

	if _, _, found, _ := am.ReadMemoryAsOf("phoenix", delAt); found {
		t.Fatal("as_of in the deleted gap must find nothing")
	}

	time.Sleep(10 * time.Millisecond)
	if err := am.SaveMemory("phoenix", "v2-recreated"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	afterRecreate := time.Now()

	content, _, found, err := am.ReadMemoryAsOf("phoenix", afterRecreate)
	if err != nil || !found {
		t.Fatalf("as_of after recreate must resolve the new version: found=%v err=%v", found, err)
	}
	if content != "v2-recreated" {
		t.Fatalf("expected v2-recreated, got %q", content)
	}

	if _, _, found, _ := am.ReadMemoryAsOf("phoenix", delAt); found {
		t.Fatal("the deleted gap must stay empty even after a recreate")
	}
}
