package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r438 probes: the memory repair loop - RepairSuggestions reads the
// consolidation sidecar and formats unresolved findings for the next
// session's system prompt. Deterministic, non-destructive (suggests only),
// naturally convergent (fixing the entry clears the finding on rescan).

// writeSidecar plants a raw consolidation state for RepairSuggestions tests.
func writeSidecar(t *testing.T, am *AutoMemory, state *consolidationState) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(am.dir, consolidationStateFile), data, 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

func TestRepairSuggestionsEmptyAndClean(t *testing.T) {
	am := newTestMemory(t)
	if got := am.RepairSuggestions(); got != "" {
		t.Fatalf("clean store (no sidecar) produced suggestions: %q", got)
	}
	// Sidecar with no findings: still empty.
	writeSidecar(t, am, &consolidationState{LastRun: time.Now().Format(time.RFC3339)})
	if got := am.RepairSuggestions(); got != "" {
		t.Fatalf("no-finding state produced suggestions: %q", got)
	}
}

func TestRepairSuggestionsStaleAndAgedConflict(t *testing.T) {
	am := newTestMemory(t)
	old := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	fresh := time.Now().AddDate(0, 0, -1).Format(time.RFC3339)
	writeSidecar(t, am, &consolidationState{
		Stale: map[string][]string{"api-key": {"broken-path: /gone/old/path"}},
		Conflicts: []consolidationConflict{
			{A: "build-impl", B: "build-design", Subject: "build command", FirstSeen: old},
			{A: "x", B: "y", Subject: "recent churn", FirstSeen: fresh}, // below age gate
		},
	})
	got := am.RepairSuggestions()
	if got == "" {
		t.Fatal("expected suggestions for stale + aged conflict")
	}
	if !strings.Contains(got, `stale "api-key"`) || !strings.Contains(got, "/gone/old/path") {
		t.Fatalf("stale entry missing detail:\n%s", got)
	}
	if !strings.Contains(got, `"build command"`) || !strings.Contains(got, "supersede") {
		t.Fatalf("aged conflict missing supersede guidance:\n%s", got)
	}
	if strings.Contains(got, "recent churn") {
		t.Fatalf("fresh conflict leaked past the age gate:\n%s", got)
	}
	if len(got) > repairSuggestionMaxLen {
		t.Fatalf("block %d bytes exceeds cap %d", len(got), repairSuggestionMaxLen)
	}
	// Corrupt sidecar degrades to empty, never errors into the prompt.
	if err := os.WriteFile(filepath.Join(am.dir, consolidationStateFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := am.RepairSuggestions(); got != "" {
		t.Fatalf("corrupt sidecar produced suggestions: %q", got)
	}
}

func TestRepairSuggestionsTruncatesAndCaps(t *testing.T) {
	am := newTestMemory(t)
	old := time.Now().AddDate(0, 0, -90).Format(time.RFC3339)
	state := &consolidationState{
		Stale: map[string][]string{
			strings.Repeat("k1-", 60): {"broken-path: " + strings.Repeat("p", 300)},
			"k2":                      {"oversized: blob"},
			"k3":                      {"broken-path: never-shown"},
		},
		Conflicts: []consolidationConflict{
			{A: strings.Repeat("a", 100), B: "b", Subject: strings.Repeat("s", 100), FirstSeen: old},
			{A: "c", B: "d", Subject: "second conflict", FirstSeen: old},
		},
	}
	writeSidecar(t, am, state)
	got := am.RepairSuggestions()
	if len(got) > repairSuggestionMaxLen {
		t.Fatalf("block %d bytes exceeds cap %d", len(got), repairSuggestionMaxLen)
	}
	if strings.Count(got, "- ") > 4 {
		t.Fatalf("line cap exceeded (%d lines):\n%s", strings.Count(got, "- "), got)
	}
}

func TestRepairSuggestionsConvergeAfterFix(t *testing.T) {
	// End-to-end loop: real entries -> Consolidate detects -> RepairSuggestions
	// reports -> agent rewrites one side -> Consolidate again -> empty.
	am := newTestMemory(t)
	writeTestEntry(t, am, "build-impl", "build command: go build ./...")
	writeTestEntry(t, am, "build-design", "build command: go build -tags goolm ./...")

	first := am.Consolidate("")
	if first.ConflictPairs != 1 {
		t.Fatalf("ConflictPairs = %d, want 1", first.ConflictPairs)
	}
	// Age the recorded firstSeen past the gate so it surfaces.
	state := am.loadConsolidationState()
	if len(state.Conflicts) != 1 {
		t.Fatalf("sidecar conflicts = %d, want 1", len(state.Conflicts))
	}
	state.Conflicts[0].FirstSeen = time.Now().AddDate(0, 0, -14).Format(time.RFC3339)
	writeSidecar(t, am, state)
	if got := am.RepairSuggestions(); got == "" {
		t.Fatal("aged conflict did not surface as a repair suggestion")
	}
	// Human-in-the-loop fix: align the outdated side (or delete it).
	if err := am.DeleteMemory("build-impl"); err != nil {
		t.Fatalf("delete outdated side: %v", err)
	}
	second := am.Consolidate("")
	if second.ConflictPairs != 0 {
		t.Fatalf("after fix ConflictPairs = %d, want 0", second.ConflictPairs)
	}
	if got := am.RepairSuggestions(); got != "" {
		t.Fatalf("repair suggestions did not converge after fix:\n%s", got)
	}
}
