package agentreliability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeStore(t *testing.T, path string, sf storeFile) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(sf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecordPersistsAndSummarizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ggcode", "agent-reliability.json")
	r := New(path)
	start := time.Now().Add(-2 * time.Minute)
	r.Record("codex", OutcomeCompleted, start, 2*time.Minute)
	r.Record("codex", OutcomeFailed, start, time.Minute)
	r.Record("claude", OutcomeCompleted, start, 30*time.Second)

	got := r.AgentSummary("codex")
	wantPrefix := "codex: 1/2 completed recently"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("AgentSummary = %q, want prefix %q", got, wantPrefix)
	}
	if !strings.Contains(got, "avg 1m") {
		t.Fatalf("AgentSummary = %q, want avg duration", got)
	}
	if r.AgentSummary("gemini") != "" {
		t.Fatalf("unknown agent should have empty summary")
	}

	// Persistence round-trip.
	r2 := New(path)
	if r2.AgentSummary("codex") == "" {
		t.Fatalf("expected persisted records to reload")
	}
}

func TestLoadCorruptStoreStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-reliability.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(path)
	if r.Overview() != "" {
		t.Fatalf("corrupt store should yield empty overview")
	}
	// Recording on top of a corrupt store must still work and persist.
	r.Record("claude", OutcomeCompleted, time.Now(), time.Second)
	r2 := New(path)
	if r2.AgentSummary("claude") == "" {
		t.Fatalf("expected recovery after corrupt store")
	}
}

func TestOverviewFormatsAllAgents(t *testing.T) {
	r := New(filepath.Join(t.TempDir(), "rel.json"))
	start := time.Now()
	r.Record("codex", OutcomeFailed, start, time.Second)
	r.Record("codex", OutcomeCancelled, start, time.Second)
	r.Record("codex", OutcomeFailed, start, time.Second)
	r.Record("codex", OutcomeCompleted, start, time.Second)
	r.Record("codex", OutcomeCompleted, start, time.Second)
	r.Record("codex", OutcomeCompleted, start, time.Second)
	r.Record("codex", OutcomeCompleted, start, time.Second)
	// Cancelled counts in total but neither as completed nor failed.
	got := r.Overview()
	if got != "codex 4/7 ok" {
		t.Fatalf("Overview = %q, want %q", got, "codex 4/7 ok")
	}
}

func TestRecentWindowOnlyCountsLatestTen(t *testing.T) {
	r := New(filepath.Join(t.TempDir(), "rel.json"))
	start := time.Now()
	for i := 0; i < 12; i++ {
		r.Record("claude", OutcomeFailed, start, time.Second)
	}
	for i := 0; i < 8; i++ {
		r.Record("claude", OutcomeCompleted, start, time.Second)
	}
	got := r.AgentSummary("claude")
	if !strings.Contains(got, "8/10 completed recently") {
		t.Fatalf("AgentSummary = %q, want 8/10 within recent window", got)
	}
}

func TestRingBufferCapsPerAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rel.json")
	r := New(path)
	start := time.Now()
	for i := 0; i < maxRecordsPerAgent+20; i++ {
		r.Record("claude", OutcomeCompleted, start, time.Second)
	}
	r.mu.Lock()
	got := len(r.records["claude"])
	r.mu.Unlock()
	if got != maxRecordsPerAgent {
		t.Fatalf("records = %d, want capped at %d", got, maxRecordsPerAgent)
	}
}

func TestNilReceiverAndEmptyNameAreNoOps(t *testing.T) {
	var r *Reliability
	r.Record("claude", OutcomeCompleted, time.Now(), time.Second) // must not panic
	if r.AgentSummary("claude") != "" || r.Overview() != "" {
		t.Fatalf("nil ledger should render empty summaries")
	}
	real := New("") // no persistence path
	real.Record("", OutcomeCompleted, time.Now(), time.Second)
	if real.Overview() != "" {
		t.Fatalf("empty agent name must be ignored")
	}
}

func TestAgentsSorted(t *testing.T) {
	r := New(filepath.Join(t.TempDir(), "rel.json"))
	start := time.Now()
	r.Record("zephyr", OutcomeCompleted, start, time.Second)
	r.Record("claude", OutcomeCompleted, start, time.Second)
	got := r.Agents()
	if len(got) != 2 || got[0] != "claude" || got[1] != "zephyr" {
		t.Fatalf("Agents = %v, want [claude zephyr]", got)
	}
}
