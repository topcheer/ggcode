package session

// Regression test for GitHub issue #1291: backfill rewrites must be
// SKIPPED when the session lock cannot be acquired - running the
// read->tmp->rename rewrite anyway bypassed the #558 D cross-process
// lost-update guard.
//
// Note: lockIndexFile uses a BLOCKING flock, so live contention simply
// waits (safe). The dangerous path is the lock *failing* (e.g. the .flock
// sidecar cannot be created). We inject that failure by pre-creating the
// .flock path as a directory - os.OpenFile(O_CREATE) then fails with
// EISDIR deterministically.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue1291_BackfillSkipsWhenLockFails(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Records in the real JSONL schema: Type "message" with no "ts" field
	// (zero timestamp) - the backfillTimestamps trigger condition.
	const body = `{"type":"message","role":"user","content":"hi","id":""}
{"type":"message","role":"assistant","content":"yo","id":""}
`
	const sessionID = "sess1291"
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Force lockSessionFile to FAIL: the .flock sidecar must be a file,
	// pre-create it as a directory.
	if err := os.Mkdir(path+".flock", 0o755); err != nil {
		t.Fatal(err)
	}

	// Both backfills must no-op instead of rewriting (old code logged the
	// lock failure and rewrote anyway - lost update).
	store.backfillTimestamps(sessionID)
	store.backfillIDs(sessionID, map[string]provider.Message{
		"": {Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}},
	})

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("#1291: file rewritten despite lock failure (lost-update window open):\n%s", after)
	}
	// No stray tmp files either (the rewrite must never have started).
	matches, _ := filepath.Glob(path + "*.tmp*")
	if len(matches) > 0 {
		t.Fatalf("rewrite left temp files despite lock failure: %v", matches)
	}

	// Sanity: with a working lock the backfill does run (file changes).
	if err := os.Remove(path + ".flock"); err != nil {
		t.Fatal(err)
	}
	store.backfillTimestamps(sessionID)
	freed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(freed) == body {
		t.Fatalf("backfill did not run even with an acquirable lock: %s", freed)
	}
}
