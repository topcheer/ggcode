package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestR413_RecordAggregatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	RecordFailureAttribution(dir, "fix the bug in parser.go", "edit_file", "internal/x/parser.go", 40)
	RecordFailureAttribution(dir, "fix another bug in parser.go", "edit_file", "internal/x/parser.go", 55)

	entries := loadFailureEntries(failureStorePath(dir))
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (aggregated)", len(entries))
	}
	e := entries[0]
	if e.Occurrences != 2 {
		t.Fatalf("occurrences = %d, want 2", e.Occurrences)
	}
	if e.MaxCRS != 55 {
		t.Fatalf("maxCRS = %d, want 55 (max across runs)", e.MaxCRS)
	}
	if e.TaskType == "" || e.SuspectFile != "internal/x/parser.go" {
		t.Fatalf("bad entry: %+v", e)
	}
}

func TestR413_DistinctFilesAndTaskTypes(t *testing.T) {
	dir := t.TempDir()
	RecordFailureAttribution(dir, "fix bug in parser.go", "edit_file", "a/parser.go", 40)
	RecordFailureAttribution(dir, "fix bug in other.go", "edit_file", "a/other.go", 30)

	RecordFailureAttribution(dir, "add feature to parser.go", "write_file", "a/parser.go", 50)

	entries := loadFailureEntries(failureStorePath(dir))
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
}

func TestR413_FailureHintsForPrompt(t *testing.T) {
	dir := t.TempDir()
	RecordFailureAttribution(dir, "fix the bug in parser.go", "edit_file", "internal/x/parser.go", 40)
	RecordFailureAttribution(dir, "fix the bug in parser.go", "edit_file", "internal/x/parser.go", 40)

	hints := FailureHintsForPrompt(dir, "please fix the bug in parser", 2)
	if hints == "" {
		t.Fatal("hints empty, want failure attribution block")
	}
	if !strings.Contains(hints, "internal/x/parser.go") {
		t.Fatalf("hints missing suspect file:\n%s", hints)
	}
	if !strings.Contains(hints, "CRS=40") || !strings.Contains(hints, "2") {
		t.Fatalf("hints missing CRS/occurrences:\n%s", hints)
	}
	if !strings.Contains(hints, "Failure Attribution Memory") {
		t.Fatalf("hints missing header:\n%s", hints)
	}
}

func TestR413_NoEntriesNoHints(t *testing.T) {
	dir := t.TempDir()
	if got := FailureHintsForPrompt(dir, "anything", 2); got != "" {
		t.Fatalf("hints = %q, want empty", got)
	}
}

func TestR413_CorruptStoreTolerated(t *testing.T) {
	dir := t.TempDir()
	path := failureStorePath(dir)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, []byte("{not json"), 0600)
	if got := FailureHintsForPrompt(dir, "anything", 2); got != "" {
		t.Fatalf("hints = %q, want empty on corrupt store", got)
	}

	RecordFailureAttribution(dir, "fix bug", "edit_file", "a.go", 30)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []PlaybookFailureEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("store not replaced with valid JSON: %v", err)
	}
}

func TestR413_TakeFinalSuspectSingleConsumption(t *testing.T) {
	s := newCausalAttributionState()
	step, crs := s.takeFinalSuspect()
	if step != nil || crs != 0 {
		t.Fatalf("empty state take = (%v, %d), want (nil, 0)", step, crs)
	}

	cp := causalEditStep{iteration: 3, toolName: "edit_file", filePath: "a.go", dirPath: "a"}
	s.lastSuspectStep = &cp
	s.lastSuspectCRS = 42

	step, crs = s.takeFinalSuspect()
	if step == nil || crs != 42 || step.filePath != "a.go" || step.toolName != "edit_file" {
		t.Fatalf("take = (%v, %d), want full suspect with CRS 42", step, crs)
	}
	step2, crs2 := s.takeFinalSuspect()
	if step2 != nil || crs2 != 0 {
		t.Fatalf("second take = (%v, %d), want consumed (nil, 0)", step2, crs2)
	}
}

func TestR413_ResetClearsSuspect(t *testing.T) {
	s := newCausalAttributionState()
	cp := causalEditStep{iteration: 1, toolName: "edit_file", filePath: "a.go"}
	s.lastSuspectStep = &cp
	s.lastSuspectCRS = 30
	s.reset()
	if s.lastSuspectStep != nil || s.lastSuspectCRS != 0 {
		t.Fatalf("reset left suspect state: (%v, %d)", s.lastSuspectStep, s.lastSuspectCRS)
	}
}

func TestR413_TerminalHookGates(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{}

	a.recordFailureAttribution(&RunStats{Success: true, UserPrompt: "fix bug"})
	a.recordFailureAttribution(&RunStats{Success: false, UserPrompt: "fix bug"})
	if _, err := os.Stat(failureStorePath(dir)); err == nil {
		t.Fatal("unexpected store created")
	}
}

func TestR413_PruneCapsEntries(t *testing.T) {
	now := time.Now()
	entries := make([]PlaybookFailureEntry, maxFailureEntries+5)
	for i := range entries {
		entries[i].Occurrences = i
		entries[i].LastSeen = now
	}
	got := pruneFailureEntries(entries)
	if len(got) != maxFailureEntries {
		t.Fatalf("len = %d, want %d", len(got), maxFailureEntries)
	}

	if got[0].Occurrences != 24 || got[len(got)-1].Occurrences != 5 {
		t.Fatalf("eviction wrong: first=%d last=%d, want 24..5", got[0].Occurrences, got[len(got)-1].Occurrences)
	}
	for _, e := range got {
		if e.Occurrences < 5 {
			t.Fatalf("weak entry survived: %+v", e)
		}
	}
}
