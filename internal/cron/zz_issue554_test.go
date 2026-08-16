package cron

// Feature test for GitHub issue #554 G (ver-41 probe): SwitchSession kept the
// old session's lastEnqueue debounce timestamps while nextID reset to 0, so a
// job in the new session reusing an ID like "cron-1" had its first fire
// swallowed by the stale 5s debounce (probe observed fired==0).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestIssue554G_SwitchSessionResetsDebounce verifies that after SwitchSession
// the debounce map no longer carries timestamps from the old session, so a
// same-named job (cron-1) in the new session can fire immediately.
func TestIssue554G_SwitchSessionResetsDebounce(t *testing.T) {
	dir := t.TempDir()
	oldStore := filepath.Join(dir, "old_session.json")
	newStore := filepath.Join(dir, "new_session.json")

	var fired int32
	s := NewScheduler(func(prompt string, _ bool) { atomic.AddInt32(&fired, 1) }, oldStore)
	t.Cleanup(s.Shutdown)

	// Old session had a job "cron-1" that fired just now (fresh debounce hit).
	if _, err := s.Create("*/5 * * * *", "old job", true, false); err != nil {
		t.Fatal(err)
	}

	// Simulate the old-session fire that leaves a fresh debounce timestamp,
	// exactly like the racing fire the ver-41 probe observed.
	s.mu.Lock()
	s.lastEnqueue["cron-1"] = time.Now()
	s.mu.Unlock()

	// Write a new-session store containing its own cron-1 so Load() rebinds
	// the same-named job.
	store := sessionStore{Jobs: []jobJSON{{
		ID:          "cron-1",
		CronExpr:    "*/5 * * * *",
		Prompt:      "new session job",
		Recurring:   true,
		QueueIfBusy: false,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}}}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newStore, data, 0644); err != nil {
		t.Fatal(err)
	}

	s.SwitchSession(newStore, oldStore, dir)

	s.mu.Lock()
	stale, hasStale := s.lastEnqueue["cron-1"]
	n := len(s.lastEnqueue)
	s.mu.Unlock()
	if hasStale || n != 0 {
		t.Fatalf("SwitchSession must reset lastEnqueue (new session's cron-1 first fire would be debounced by old timestamp %v; entries=%d)", stale, n)
	}

	// The new session's job must exist and be schedulable.
	job, ok := s.Get("cron-1")
	if !ok {
		t.Fatal("new session job cron-1 not loaded")
	}
	if job.Prompt != "new session job" {
		t.Fatalf("cron-1 should be the NEW session's job; got prompt %q", job.Prompt)
	}

	// Directly exercise the debounce path the probe showed failing: fire the
	// newly loaded job right away. Before the fix this was a no-op because of
	// the stale lastEnqueue entry.
	s.mu.Lock()
	jobPtr := s.jobs["cron-1"]
	gen := s.generations["cron-1"]
	s.mu.Unlock()
	if jobPtr == nil {
		t.Fatal("cron-1 not present in scheduler map after Load")
	}
	s.fireJob(jobPtr, gen)
	// enqueue runs synchronously inside fireJob for the test's closure.
	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("new session's first fire was swallowed (fired=%d, want 1)", got)
	}
}
