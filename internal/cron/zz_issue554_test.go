package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestIssue554G_SwitchSessionResetsDebounce(t *testing.T) {
	dir := t.TempDir()
	oldStore := filepath.Join(dir, "old_session.json")
	newStore := filepath.Join(dir, "new_session.json")

	var fired int32
	s := NewScheduler(func(prompt string, _ bool) { atomic.AddInt32(&fired, 1) }, oldStore)
	t.Cleanup(s.Shutdown)

	if _, err := s.Create("*/5 * * * *", "old job", true, false); err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.lastEnqueue["cron-1"] = time.Now()
	s.mu.Unlock()

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

	job, ok := s.Get("cron-1")
	if !ok {
		t.Fatal("new session job cron-1 not loaded")
	}
	if job.Prompt != "new session job" {
		t.Fatalf("cron-1 should be the NEW session's job; got prompt %q", job.Prompt)
	}

	s.mu.Lock()
	jobPtr := s.jobs["cron-1"]
	gen := s.generations["cron-1"]
	s.mu.Unlock()
	if jobPtr == nil {
		t.Fatal("cron-1 not present in scheduler map after Load")
	}
	s.fireJob(jobPtr, gen)
	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("new session's first fire was swallowed (fired=%d, want 1)", got)
	}
}
