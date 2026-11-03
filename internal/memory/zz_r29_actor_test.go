package memory

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRecordProvenanceActorFirstWriteWins(t *testing.T) {
	am := newProvTestMemory(t)
	am.RecordProvenanceActor("alpha", "save_memory:project", "agent-7")

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
	am.RecordProvenance("legacy-key", "save_memory:project")
	am.RecordProvenanceActor("legacy-key", "save_memory:project", "agent-9")
	rec, _ := am.UsageOf("legacy-key")
	if rec.Actor != "agent-9" {
		t.Fatalf("legacy entry actor not backfilled: %q", rec.Actor)
	}

	am.RecordProvenanceActor("legacy-key", "save_memory:project", "")
	if rec2, _ := am.UsageOf("legacy-key"); rec2.Actor != "agent-9" {
		t.Fatalf("empty actor write must not clear actor: %q", rec2.Actor)
	}
}

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

	if err := am.SaveMemoryWithSource("main-written", "body", "save_memory:project"); err != nil {
		t.Fatalf("SaveMemoryWithSource: %v", err)
	}
	rec, _ := am.UsageOf("main-written")
	if rec.Actor != "" {
		t.Fatalf("legacy API must not record actor: %q", rec.Actor)
	}

	if s := provenanceSuffix(usageInfo{Uses: 3, Source: "save_memory:project", Actor: "tm-2"}); !strings.Contains(s, "actor=tm-2") {
		t.Fatalf("index marker missing actor segment: %q", s)
	}
	if s := provenanceSuffix(usageInfo{Uses: 3, Source: "save_memory:project"}); strings.Contains(s, "actor=") {
		t.Fatalf("index marker must stay legacy-compact without actor: %q", s)
	}
}

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
