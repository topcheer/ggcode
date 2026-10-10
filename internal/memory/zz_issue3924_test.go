package memory

// #3924 probe: a key recreated via save after a delete is revived for as_of
// replay - the stale tombstone must be cleared by the recreate so it stops
// reporting not-found. The delete window itself keeps the #3880 semantics.

import (
	"testing"
	"time"
)

func TestIssue3924_RecreateClearsTombstoneForAsOf(t *testing.T) {
	dir := t.TempDir()
	am := NewProjectAutoMemory(dir)
	if am == nil {
		t.Fatal("auto memory nil")
	}
	if err := am.SaveMemory("revive-me", "v1"); err != nil {
		t.Fatal(err)
	}
	preDelete := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := am.DeleteMemory("revive-me"); err != nil {
		t.Fatal(err)
	}
	delAt := time.Now()
	time.Sleep(10 * time.Millisecond)
	if err := am.SaveMemory("revive-me", "v2-recreated"); err != nil {
		t.Fatal(err)
	}
	recreateAt := time.Now()
	time.Sleep(10 * time.Millisecond)

	// BUG (pre-fix): the tombstone survived the recreate, so ANY as_of after
	// delAt returned not-found forever - including instants after the
	// recreate, even though the live file exists with fresh content.
	content, _, found, err := am.ReadMemoryAsOf("revive-me", recreateAt)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("as_of after recreate must find the recreated content (stale tombstone)")
	}
	if content != "v2-recreated" {
		t.Fatalf("post-recreate content = %q, want v2-recreated", content)
	}
	// A plain now read must also be unaffected (tombstone removed, not live data).
	if got, err := am.LoadKey("revive-me"); err != nil || got != "v2-recreated" {
		t.Fatalf("LoadKey = %q, %v; want v2-recreated", got, err)
	}

	// The delete window (between delAt and the recreate) keeps the #3880
	// semantics: the tombstone covered that instant until the revive.
	if _, _, found, _ := am.ReadMemoryAsOf("revive-me", delAt); found {
		t.Fatal("as_at at delete instant must still find nothing (tombstone window)")
	}
	// And pre-delete instants still resolve the exact old version.
	if c, _, ok, _ := am.ReadMemoryAsOf("revive-me", preDelete); !ok || c != "v1" {
		t.Fatalf("pre-delete as_of = %q found=%v, want v1", c, ok)
	}
}

func TestIssue3924_FreshSaveNeverSeesTombstone(t *testing.T) {
	// A save on a key that was never deleted must not be affected by the
	// removal call (os.Remove returns IsNotExist -> silent no-op).
	dir := t.TempDir()
	am := NewProjectAutoMemory(dir)
	if am == nil {
		t.Fatal("auto memory nil")
	}
	if err := am.SaveMemory("never-deleted", "content"); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	c, _, found, err := am.ReadMemoryAsOf("never-deleted", at)
	if err != nil || !found || c != "content" {
		t.Fatalf("as_of = %q found=%v err=%v; want content", c, found, err)
	}
}
