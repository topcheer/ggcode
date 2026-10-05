package memory

// r29 actor-aware provenance probes: the WRITER identity (main / sub-agent
// id / swarm teammate) is recorded alongside the source label, first-write
// wins per field, legacy entries backfill, and the index marker renders it.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Group 1: RecordProvenanceActor - first-write-wins per field + backfill.
func TestRecordProvenanceActorFirstWriteWins(t *testing.T) {
	am := newProvTestMemory(t)
	am.RecordProvenanceActor("alpha", "save_memory:project", "agent-7")
	// A later write by a DIFFERENT actor must not move the recorded
	// origin: provenance traces creation, not the latest editor.
	am.RecordProvenanceActor("alpha", "run-reflection", "main")
	rec, ok := am.UsageOf("alpha")
	if !ok {
		t.Fatal("expected usage record for alpha")
	}
	if rec.Source != "save_memory:project" || rec.Actor != "agent-7" {
		t.Fatalf("actor provenance moved on overwrite: src=%q actor=%q", rec.Source, rec.Actor)
	}
}

func TestRecordProvenanceActorBackfillsLegacy(t *testing.T) {
	am := newProvTestMemory(t)
	am.RecordProvenance("legacy-key", "save_memory:project") // pre-r29 write: no actor
	am.RecordProvenanceActor("legacy-key", "save_memory:project", "agent-9")
	rec, _ := am.UsageOf("legacy-key")
	if rec.Actor != "agent-9" {
		t.Fatalf("legacy entry actor not backfilled: %q", rec.Actor)
	}
	// Source stays (was already set) and an empty actor write never clears.
	am.RecordProvenanceActor("legacy-key", "save_memory:project", "")
	if rec2, _ := am.UsageOf("legacy-key"); rec2.Actor != "agent-9" {
		t.Fatalf("empty actor write must not clear actor: %q", rec2.Actor)
	}
}

// Group 2: SaveMemoryWithSourceActor end-to-end - actor lands in the
// sidecar JSON (backward-compatible omitempty) and the index marker.
func TestSaveMemoryWithSourceActorSidecar(t *testing.T) {
	am := newProvTestMemory(t)
	if err := am.SaveMemoryWithSourceActor("sub-written", "body", "save_memory:project", "tm-2"); err != nil {
		t.Fatalf("SaveMemoryWithSourceActor: %v", err)
	}
	raw, err := os.ReadFile(am.dir + "/.usage.json")
	if err != nil {
		t.Fatalf("sidecar read: %v", err)
	}
	if !strings.Contains(string(raw), `"actor":"tm-2"`) {
		t.Fatalf("actor missing from sidecar JSON: %s", raw)
	}
	// Legacy 3-arg API keeps byte-identical behavior: no actor field.
	if err := am.SaveMemoryWithSource("main-written", "body", "save_memory:project"); err != nil {
		t.Fatalf("SaveMemoryWithSource: %v", err)
	}
	rec, _ := am.UsageOf("main-written")
	if rec.Actor != "" {
		t.Fatalf("legacy API must not record actor: %q", rec.Actor)
	}
	// Index marker renders the actor segment only when present.
	if s := provenanceSuffix(usageInfo{Uses: 3, Source: "save_memory:project", Actor: "tm-2"}); !strings.Contains(s, "actor=tm-2") {
		t.Fatalf("index marker missing actor segment: %q", s)
	}
	if s := provenanceSuffix(usageInfo{Uses: 3, Source: "save_memory:project"}); strings.Contains(s, "actor=") {
		t.Fatalf("index marker must stay legacy-compact without actor: %q", s)
	}
}

// Group 3: JSON round-trip of the extended schema.
func TestUsageInfoActorJSONRoundTrip(t *testing.T) {
	idx := usageIndex{Version: usageVersion, Entries: map[string]*usageInfo{
		"k": {Source: "save_memory:project", Actor: "agent-3"},
	}}
	b, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	var back usageIndex
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Entries["k"].Actor != "agent-3" {
		t.Fatalf("actor lost in round-trip: %+v", back.Entries["k"])
	}
}
