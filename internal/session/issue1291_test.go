package session

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

	const body = `{"type":"message","role":"user","content":"hi","id":""}
{"type":"message","role":"assistant","content":"yo","id":""}
`
	const sessionID = "sess1291"
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(path+".flock", 0o755); err != nil {
		t.Fatal(err)
	}

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

	matches, _ := filepath.Glob(path + "*.tmp*")
	if len(matches) > 0 {
		t.Fatalf("rewrite left temp files despite lock failure: %v", matches)
	}

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
