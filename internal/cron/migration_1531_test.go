package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// #1531 case B: a tombstone stamped BEFORE the new-store write closes the
// crash window between the two atomic writes - the next instance must skip
// when the tombstone target exists, and re-migrate only when it vanished.
func Test1531TombstoneSkipsWhenTargetExists(t *testing.T) {
	dir := t.TempDir()
	oldStore := filepath.Join(dir, "old.json")
	target := filepath.Join(dir, "sessions", "s1.json")
	ws := t.TempDir()

	// Old store with the tombstone already pointing at a landed target.
	writeOldStore(t, oldStore, ws, target)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"jobs":[]}`), 0644); err != nil {
		t.Fatal(err)
	}

	MigrateWorkspaceJobs(oldStore, filepath.Join(dir, "sessions", "s2.json"), ws)

	// The bucket must remain untouched (skip, not delete) and the new
	// session store must NOT have been created.
	data, err := os.ReadFile(oldStore)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("old store vanished on skip")
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "s2.json")); err == nil {
		t.Fatal("skip must not write a second session store (double fire)")
	}
}

// #1531 case B: tombstone whose target never landed (crash between the two
// writes) must re-migrate into the CURRENT session exactly once.
func Test1531TombstoneRetriesWhenTargetMissing(t *testing.T) {
	dir := t.TempDir()
	oldStore := filepath.Join(dir, "old.json")
	deadTarget := filepath.Join(dir, "gone.json")
	ws := t.TempDir()

	writeOldStore(t, oldStore, ws, deadTarget) // target file does not exist

	MigrateWorkspaceJobs(oldStore, filepath.Join(dir, "s2.json"), ws)

	// Re-migrated: the bucket leaves the old store. With no other buckets
	// left the whole file is removed (len(sf)==0 path).
	data, err := os.ReadFile(oldStore)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(data) != 0 && string(data) != "{}" {
		t.Fatalf("bucket must be removed after successful re-migration, got %s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "s2.json")); err != nil {
		t.Fatal("re-migration must land the new session store")
	}
}

func writeOldStore(t *testing.T, path, ws, migratedTo string) {
	t.Helper()
	bucket := workspaceBucket{Workspace: ws, Jobs: []jobJSON{
		{ID: "j1", CronExpr: "0 * * * *", Prompt: "hi", Recurring: true},
	}, MigratedTo: migratedTo}
	sf := oldStoreFile{workspaceKey(ws): bucket}
	out, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatal(err)
	}
}
