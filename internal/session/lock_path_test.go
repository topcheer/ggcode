package session

import (
	"path/filepath"
	"strings"
	"testing"
)

// #411: LockFilePath must sanitize session IDs the same way as
// sessionPath (#401) — a traversal ID via --resume must not create or
// remove .lock files outside the store directory.
func TestLockFilePathSanitization(t *testing.T) {
	store := filepath.Join(t.TempDir(), "sessions")

	bad := []string{"../../foo", "../escape", "a/b", "a\\b", "..", "."}
	for _, id := range bad {
		p := LockFilePath(store, id)
		if filepath.Dir(p) != store {
			t.Errorf("LockFilePath(store, %q) = %q escapes store dir", id, p)
		}
		if !strings.HasSuffix(p, "_invalid_id_.lock") {
			t.Errorf("LockFilePath(store, %q) = %q, want placeholder for invalid ID", id, p)
		}
	}

	good := "abc-DEF_123"
	if got, want := LockFilePath(store, good), filepath.Join(store, good+".lock"); got != want {
		t.Errorf("LockFilePath(store, %q) = %q, want %q", good, got, want)
	}
}
