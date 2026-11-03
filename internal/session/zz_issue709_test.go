package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTryAcquireSessionLock_BasicAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	l, err := TryAcquireSessionLock(dir, "issue709")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !l.Acquired() {
		t.Fatal("lock not acquired on a free file")
	}

	l2, err := TryAcquireSessionLock(dir, "issue709")
	if err != nil {
		t.Fatalf("second acquire attempt: %v", err)
	}
	if l2.Acquired() {
		t.Fatal("second acquire must fail while the first holder keeps the lock")
	}

	l.Release()
	l3, err := TryAcquireSessionLock(dir, "issue709")
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	if !l3.Acquired() {
		t.Fatal("lock must be acquirable after Release")
	}
	l3.Release()
}

func TestPruneInvalidIndexEntries_KeepsLoadErroredEntries(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	idx := []indexEntry{
		{ID: "load-error-session", Title: "t", CreatedAt: time.Now(), UpdatedAt: time.Now(), MsgCount: 5},
	}

	got, cleaned := s.pruneInvalidIndexEntries(idx)
	if cleaned {
		t.Fatal("load-errored entry must not trigger the cleaned flag (no file deletion expected)")
	}
	if len(got) != 1 || got[0].ID != "load-error-session" {
		t.Fatalf("load-errored entry must be retained in the index, got %+v", got)
	}
}

func TestFindMessageCutoff_ZeroTimestampTreatedConservatively(t *testing.T) {
	now := time.Now()
	var lines []byte
	for i := 0; i < 510; i++ {
		ts := now.Add(-1 * time.Hour)
		if i == 250 {

			lines = append(lines, []byte(fmt.Sprintf(`{"type":"message","message":{"role":"user","content":"m%d"}}`+"\n", i))...)
			continue
		}
		lines = append(lines, []byte(fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":"user","content":"m%d"}}`+"\n", ts.Format(time.RFC3339Nano), i))...)
	}

	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, lines, 0600); err != nil {
		t.Fatal(err)
	}

	cutoff, total, _ := findMessageCutoff(path)
	if total != 510 {
		t.Fatalf("total = %d, want 510", total)
	}

	if cutoff != 0 {
		t.Fatalf("cutoff = %d, want 0: zero-timestamp record mid-file pushed the cutoff past newer messages", cutoff)
	}
}
