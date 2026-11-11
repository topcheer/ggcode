package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestCreateOneShotAtPersistsPendingNudge pins the restart-nudge durability
// contract: a durable one-shot created before a process restart is on disk
// with its explicit next_fire, and a fresh scheduler on the same store
// re-adopts it while still pending (fires it), instead of silently losing it.
func TestCreateOneShotAtPersistsPendingNudge(t *testing.T) {
	store := filepath.Join(t.TempDir(), "cron.json")

	fired := make(chan string, 1)
	s := NewScheduler(func(prompt string, queueIfBusy bool) {
		if !queueIfBusy {
			t.Errorf("restart nudge must be queue_if_busy=true")
		}
		fired <- prompt
	}, store)

	fireAt := time.Now().Add(3 * time.Minute)
	job, err := s.CreateOneShotAt(fireAt, "继续进行之前的任务", true)
	if err != nil {
		t.Fatalf("CreateOneShotAt: %v", err)
	}
	if job.Recurring || !job.Persist {
		t.Fatalf("nudge must be a persisted one-shot, got recurring=%t persist=%t", job.Recurring, job.Persist)
	}

	// On-disk state must carry the pending nudge with its explicit fire time.
	data, err := os.ReadFile(store)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var onDisk sessionStore
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("parse store: %v", err)
	}
	found := false
	for _, j := range onDisk.Jobs {
		if j.ID == job.ID {
			found = true
			if j.Persist != true {
				t.Errorf("disk entry %s must have persist=true", j.ID)
			}
			got, err := time.Parse(time.RFC3339, j.NextFire)
			if err != nil || got.Sub(fireAt) > time.Second {
				t.Errorf("disk entry next_fire=%q err=%v, want ~%s", j.NextFire, err, fireAt.Format(time.RFC3339))
			}
		}
	}
	if !found {
		t.Fatalf("pending nudge %s not persisted", job.ID)
	}

	// Simulate the restart: a fresh scheduler on the same store adopts the
	// pending nudge. Shrink the wait by rewriting NextFire to "now".
	s2 := NewScheduler(func(prompt string, queueIfBusy bool) {
		fired <- prompt
	}, store)
	s2.Load()
	adopted := s2.List()
	if len(adopted) != 1 || adopted[0].ID != job.ID {
		t.Fatalf("Load must adopt exactly the pending nudge, got %v", adopted)
	}
	// Shrink the wait: white-box move NextFire to imminent (test is in-package).
	s2.mu.Lock()
	pending := s2.jobs[job.ID]
	pending.NextFire = time.Now().Add(10 * time.Millisecond)
	s2.scheduleJobLocked(pending)
	s2.mu.Unlock()

	select {
	case p := <-fired:
		if p != "继续进行之前的任务" {
			t.Errorf("nudge prompt = %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("adopted nudge never fired after Load")
	}

	// After firing, the nudge must be removed from the store (no zombie on
	// the next restart). The enqueue happens before the removal is persisted,
	// so poll briefly for the store to settle.
	deadline := time.Now().Add(3 * time.Second)
	for {
		gone := true
		if data, err := os.ReadFile(store); err == nil {
			var after sessionStore
			if json.Unmarshal(data, &after) == nil {
				for _, j := range after.Jobs {
					if j.ID == job.ID {
						gone = false
					}
				}
			}
		}
		if gone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fired nudge %s still on disk after 3s", job.ID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestLoadDropsExpiredPersistedOneShot pins the #5428 contract: a nudge
// whose fire time passed during downtime is delivered exactly once on Load
// (never adopted, never resurrecting forever).
func TestLoadDropsExpiredPersistedOneShot(t *testing.T) {
	store := filepath.Join(t.TempDir(), "cron.json")
	fired := make(chan string, 1)
	s := NewScheduler(func(prompt string, _ bool) { fired <- prompt }, store)

	past := time.Now().Add(-time.Minute)
	if _, err := s.CreateOneShotAt(past, "stale", true); err != nil {
		t.Fatalf("CreateOneShotAt: %v", err)
	}
	// The fire already elapsed: drain any immediate fire from the creating
	// scheduler, then rebuild from disk and confirm no stale refire.
	select {
	case p := <-fired:
		if p != "stale" {
			t.Errorf("unexpected prompt %q", p)
		}
	case <-time.After(2 * time.Second):
		// acceptable: creating scheduler may or may not fire before Stop
	}

	s2 := NewScheduler(func(prompt string, _ bool) { fired <- prompt }, store)
	s2.Load()
	if jobs := s2.List(); len(jobs) != 0 {
		t.Errorf("expired nudge must not be adopted, got %d jobs", len(jobs))
	}
	// #5428 contract: an expired durable one-shot is DELIVERED once at Load
	// (the "continue the prior task" intent survives downtime) instead of
	// being silently dropped. At-least-once is the accepted tradeoff: the
	// only double-delivery window is a crash between the original enqueue
	// and its store prune, and the prompt itself is benign to repeat.
	select {
	case p := <-fired:
		if p != "stale" {
			t.Errorf("unexpected redelivered prompt %q", p)
		}
	default:
		t.Error("expired nudge must be delivered once at Load (#5428)")
	}
	// And exactly once: nothing further.
	select {
	case p := <-fired:
		t.Errorf("nudge delivered more than once after Load: %q", p)
	default:
	}
}

// TestVolatileOneShotNotPersisted guards the zero-semantic-change invariant:
// ordinary (Persist=false) one-shots stay session-scoped and never hit disk.
func TestVolatileOneShotNotPersisted(t *testing.T) {
	store := filepath.Join(t.TempDir(), "cron.json")
	s := NewScheduler(nil, store)

	// A normal one-shot created via the cron-expression path.
	if _, err := s.Create("0 0 1 1 *", "ordinary one-shot", false, false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A volatile-only store is REMOVED entirely (save with zero persistable
	// jobs deletes the file): both "no file" and "no non-recurring entries"
	// satisfy the invariant.
	data, err := os.ReadFile(store)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var onDisk sessionStore
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("parse store: %v", err)
	}
	for _, j := range onDisk.Jobs {
		if !j.Recurring {
			t.Errorf("volatile one-shot %s leaked to disk", j.ID)
		}
	}
}

// TestCreateOneShotWithIDReplacesPendingNudge pins the #5425 idempotence
// contract: re-arming the stable-ID resume nudge (double restart before the
// first nudge fires) must REPLACE the pending job (one instance, updated
// fire time), never stack a duplicate.
func TestCreateOneShotWithIDReplacesPendingNudge(t *testing.T) {
	store := filepath.Join(t.TempDir(), "cron.json")
	s := NewScheduler(func(string, bool) {}, store)

	first := time.Now().Add(3 * time.Minute)
	if _, err := s.CreateOneShotWithID(RestartResumeNudgeID, first, "继续进行之前的任务", true); err != nil {
		t.Fatalf("first arm: %v", err)
	}
	second := time.Now().Add(5 * time.Minute)
	if _, err := s.CreateOneShotWithID(RestartResumeNudgeID, second, "继续进行之前的任务", true); err != nil {
		t.Fatalf("second arm: %v", err)
	}

	// Exactly one job under the stable ID in memory...
	var pending []Job
	for _, j := range s.List() {
		if j.ID == RestartResumeNudgeID {
			pending = append(pending, j)
		}
	}
	if len(pending) != 1 {
		t.Fatalf("want exactly 1 nudge job after double arm, got %d", len(pending))
	}
	if d := pending[0].NextFire.Sub(second); d > time.Second {
		t.Errorf("re-armed nudge must fire at ~second arm time, drift=%s", d)
	}

	// ...and exactly one entry on disk.
	data, err := os.ReadFile(store)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var onDisk sessionStore
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("parse store: %v", err)
	}
	n := 0
	for _, j := range onDisk.Jobs {
		if j.ID == RestartResumeNudgeID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly 1 persisted nudge entry after double arm, got %d", n)
	}
}

// TestLoadDeliversExpiredNudgeOnce pins the #5428 contract: a durable resume
// nudge that expired while the process was down is delivered exactly once at
// Load (the "continue the prior task" intent survives long downtime) and is
// pruned from the store so a later restart cannot deliver it again.
func TestLoadDeliversExpiredNudgeOnce(t *testing.T) {
	store := filepath.Join(t.TempDir(), "cron.json")

	// Arm a nudge on a first scheduler, then shut it down before it fires.
	first := NewScheduler(func(string, bool) {}, store)
	due := time.Now().Add(-time.Minute) // already expired on disk
	if _, err := first.CreateOneShotWithID(RestartResumeNudgeID, due, "继续进行之前的任务", true); err != nil {
		t.Fatalf("arm: %v", err)
	}
	first.Shutdown()

	var mu sync.Mutex
	var got []string
	s := NewScheduler(func(prompt string, queueIfBusy bool) {
		mu.Lock()
		got = append(got, prompt)
		mu.Unlock()
		if !queueIfBusy {
			t.Errorf("expired nudge delivery must keep queue_if_busy=true")
		}
	}, store)
	defer s.Shutdown()
	s.Load()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "继续进行之前的任务" {
		t.Fatalf("want exactly 1 delivered expired nudge, got %d: %q", len(got), got)
	}

	// Store must be pruned: a second Load delivers nothing.
	s2 := NewScheduler(func(prompt string, _ bool) {
		t.Errorf("second Load must not re-deliver the expired nudge, got %q", prompt)
	}, store)
	defer s2.Shutdown()
	s2.Load()
}
