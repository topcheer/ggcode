package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r457: teammate experience ingest probes (read side).

func writeLedger(t *testing.T, dir string, entries []map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, _ := json.Marshal(e)
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "teammate-experience.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIngestTeammateExperience_FoldsEntries(t *testing.T) {
	dir := t.TempDir()
	ts := time.Now().Add(-time.Hour)
	writeLedger(t, dir, []map[string]any{
		{"ts": ts, "team_id": "t1", "teammate": "tm-1 (coder)", "digest": "refactored auth module"},
		{"ts": ts.Add(time.Minute), "team_id": "t1", "teammate": "tm-2 (tester)", "digest": "added e2e probes"},
	})
	s := newTrajIntelState()
	s.ingestTeammateExperience(dir)
	if len(s.learnings) != 2 {
		t.Fatalf("expected 2 ingested, got %d", len(s.learnings))
	}
	if s.learnings[0].Type != "teammate" || s.learnings[0].Category != "teammate_experience" {
		t.Fatalf("type/category wrong: %+v", s.learnings[0])
	}
	if !strings.Contains(s.learnings[1].Insight, "e2e probes") {
		t.Fatalf("insight should carry digest: %q", s.learnings[1].Insight)
	}
}

func TestIngestTeammateExperience_Idempotent(t *testing.T) {
	dir := t.TempDir()
	ts := time.Now().Add(-time.Hour)
	writeLedger(t, dir, []map[string]any{
		{"ts": ts, "team_id": "t1", "teammate": "tm-1", "digest": "did a thing"},
	})
	// First ingest folds into the pending buffer, then a persist writes it
	// to the main store (simulating the post-run flow).
	s := newTrajIntelState()
	s.ingestTeammateExperience(dir)
	s.filePath = filepath.Join(dir, ".ggcode", "trajectory-learnings.jsonl")
	if err := s.persistLocked(); err != nil {
		t.Fatalf("persist: %v", err)
	}
	// Second ingest must see the high-water mark in the FILE and skip all.
	s.learnings = nil
	s.ingestTeammateExperience(dir)
	if len(s.learnings) != 0 {
		t.Fatalf("re-ingest duplicated entries: %d", len(s.learnings))
	}
}

func TestIngestTeammateExperience_AbsentLedgerNoop(t *testing.T) {
	dir := t.TempDir()
	s := newTrajIntelState()
	s.ingestTeammateExperience(dir) // no ledger file - must be a clean no-op
	if len(s.learnings) != 0 {
		t.Fatal("absent ledger must ingest nothing")
	}
}
