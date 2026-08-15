package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// #489: a late persist from a cancelled run must NEVER land in the NEW
// session's JSONL. Previously the persist handler read bridge fields at
// trigger time (runSes==nil after ClearCurrentSession → fell back to
// currentSes == the NEW session).
//
// This exercises the real handler path indirectly: we reproduce the state
// transitions the handler observes and assert the snapshot semantics.
func TestPersistSnapshot_SupersededRunDropsIntoVoid(t *testing.T) {
	b := &ChatBridge{}

	// t0: run starts against session A (snapshot installed).
	sesA := &session.Session{ID: "run-a"}
	b.mu.Lock()
	b.runSes = sesA
	b.mu.Unlock()
	b.setRunPersistSnapshot()

	// Snapshot captured A.
	b.mu.Lock()
	captured := b.persistSession
	b.mu.Unlock()
	if captured == nil || captured.ID != "run-a" {
		t.Fatalf("snapshot must capture run-start session A, got %+v", captured)
	}

	// t1: user hits New — Cancel + ClearCurrentSession bumps generation and
	// drops the snapshot; a NEW session B is installed.
	b.ClearCurrentSession()
	sesB := &session.Session{ID: "new-b"}
	b.mu.Lock()
	b.currentSes = sesB
	b.mu.Unlock()

	// t2: the old run's tail persist fires. With the fix, no snapshot
	// exists → dropped. (Old code: runSes==nil → fallback currentSes==B.)
	b.mu.Lock()
	lateSes := b.persistSession
	b.mu.Unlock()
	if lateSes != nil {
		t.Fatalf("late persist from superseded run must find NO snapshot (drop), got session %s", lateSes.ID)
	}
	if lateSes != nil && lateSes.ID == "new-b" {
		t.Fatal("cross-write re-armed: late persist would target the NEW session")
	}
}

// The generation must advance on session clear so emit() guard also drops
// stale-run stream events.
func TestRunGeneration_AdvancesOnClear(t *testing.T) {
	b := &ChatBridge{}
	b.mu.Lock()
	b.runSes = &session.Session{ID: "g1"}
	b.mu.Unlock()
	b.setRunPersistSnapshot()
	b.mu.Lock()
	genAfterStart := b.runGeneration
	b.mu.Unlock()

	b.ClearCurrentSession()

	b.mu.Lock()
	genAfterClear := b.runGeneration
	b.mu.Unlock()
	if genAfterClear <= genAfterStart {
		t.Fatalf("generation must advance on ClearCurrentSession: %d -> %d", genAfterStart, genAfterClear)
	}
}

// appendPersistMessage against the captured session writes to THAT
// session's JSONL (sanity: snapshot path preserves #270 semantics).
func TestAppendPersistMessage_TargetsCapturedSession(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	b := &ChatBridge{}
	ses := &session.Session{ID: "cap-1"}

	b.appendPersistMessage(store, ses, provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "captured"}},
	})

	// Read back the JSONL.
	raw, err := os.ReadFile(filepath.Join(dir, "cap-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("JSONL line not valid JSON: %v\n%s", err, raw)
	}
	if got["session_id"] != "cap-1" {
		t.Fatalf("persisted to wrong session: %v", got["session_id"])
	}
}
