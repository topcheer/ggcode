package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestPersistSnapshot_SupersededRunDropsIntoVoid(t *testing.T) {
	b := &ChatBridge{}

	sesA := &session.Session{ID: "run-a"}
	b.mu.Lock()
	b.runSes = sesA
	b.mu.Unlock()
	b.setRunPersistSnapshot()

	b.mu.Lock()
	captured := b.persistSession
	b.mu.Unlock()
	if captured == nil || captured.ID != "run-a" {
		t.Fatalf("snapshot must capture run-start session A, got %+v", captured)
	}

	b.ClearCurrentSession()
	sesB := &session.Session{ID: "new-b"}
	b.mu.Lock()
	b.currentSes = sesB
	b.mu.Unlock()

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

func TestEmitIfCurrent_DropsStaleRunEvents(t *testing.T) {
	b := &ChatBridge{}
	b.mu.Lock()
	b.runSes = &session.Session{ID: "g1"}
	b.mu.Unlock()
	b.setRunPersistSnapshot()
	runGen := b.currentRunGeneration()

	delivered := false
	b.OnStreamEvent = func(eventType string, data json.RawMessage) {
		delivered = true
	}

	b.ClearCurrentSession()

	b.emitIfCurrent(runGen, provider.StreamEvent{Type: provider.StreamEventText, Text: "ghost"})
	if delivered {
		t.Fatal("stale-run event leaked into the new session's stream")
	}

	b.emitIfCurrent(b.currentRunGeneration(), provider.StreamEvent{Type: provider.StreamEventText, Text: "live"})
	if !delivered {
		t.Fatal("current-run event was dropped by the stale guard")
	}
}

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
