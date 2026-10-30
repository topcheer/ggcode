package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Reproducer for the suspected bug: GC's forgotten-digest path removes the
// live file without archiving or writing a tombstone (#3880/#3924 contract),
// so ReadMemoryAsOf time-travels back to stale .history content after GC.
func TestGCForgottenDigestTimeTravelRepro(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	key := "build-process"

	// Two saves => one archived version in .history plus a live version.
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

	// Live file must be gone.
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
