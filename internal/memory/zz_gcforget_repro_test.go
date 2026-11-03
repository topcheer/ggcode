package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGCForgottenDigestTimeTravelRepro(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	key := "build-process"

	if err := am.SaveMemoryWithSourceActor(key, "v1 content", "user", "session-a"); err != nil {
		t.Fatalf("save1: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := am.SaveMemoryWithSourceActor(key, "v2 content", "user", "session-a"); err != nil {
		t.Fatalf("save2: %v", err)
	}

	if err := am.ForgetKey(key); err != nil {
		t.Fatalf("forget: %v", err)
	}
	am.GarbageCollect()

	if _, err := os.Stat(filepath.Join(dir, "build-process.md")); !os.IsNotExist(err) {
		t.Fatalf("live file still present after GC: %v", err)
	}

	now := time.Now()
	content, _, found, err := am.ReadMemoryAsOf(key, now)
	if err != nil {
		t.Fatalf("asof read: %v", err)
	}
	tomb := filepath.Join(dir, ".history", "build-process.tombstone")
	_, tombErr := os.Stat(tomb)
	t.Logf("as_of(now): found=%v content=%q tombstoneExists=%v", found, content, tombErr == nil)
	if found {
		t.Errorf("BUG CONFIRMED: ReadMemoryAsOf returned stale content %q after ForgetKey+GC digest (no tombstone written)", content)
	}
}
