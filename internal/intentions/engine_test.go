package intentions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestEngine(t *testing.T) (*Store, *Engine, *[]string) {
	t.Helper()
	var prompts []string
	s := newTestStore(t)
	e := NewEngine(s, t.TempDir(), func(prompt string, queueIfBusy bool) {
		prompts = append(prompts, prompt)
	})
	return s, e, &prompts
}

func TestFileFamilyFireOnce(t *testing.T) {
	s, e, prompts := newTestEngine(t)
	base := e.workingDir
	target := filepath.Join(base, "watched.txt")
	os.WriteFile(target, []byte("v1"), 0o644)

	in, err := s.Put(Intention{
		Statement:     "run make verify",
		CueFamily:     CueFile,
		CuePattern:    "watched.txt",
		OriginExcerpt: "when watched.txt changes, run make verify",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	e.Poll() // prime baseline: must not fire
	if len(*prompts) != 0 {
		t.Fatalf("primed poll fired: %v", *prompts)
	}

	os.WriteFile(target, []byte("v2"), 0o644)
	newMtime := time.Now().Add(2 * time.Second)
	os.Chtimes(target, newMtime, newMtime)

	e.Poll()
	if len(*prompts) != 1 {
		t.Fatalf("expected exactly one fired prompt, got %d", len(*prompts))
	}
	p := (*prompts)[0]
	if !strings.Contains(p, in.ID) || !strings.Contains(p, "run make verify") || !strings.Contains(p, "watched.txt changes") {
		t.Fatalf("fired prompt missing provenance (id/statement/excerpt): %s", p)
	}
	if !strings.Contains(p, "realize") {
		t.Fatalf("fired prompt missing realize instruction: %s", p)
	}

	// fire-once: a second change must not re-fire.
	os.WriteFile(target, []byte("v3"), 0o644)
	newMtime2 := time.Now().Add(4 * time.Second)
	os.Chtimes(target, newMtime2, newMtime2)
	e.Poll()
	if len(*prompts) != 1 {
		t.Fatalf("fire-once violated: %d prompts", len(*prompts))
	}
	got, _ := s.FindByID(in.ID)
	if got.State != StateFired || got.FireCount != 1 {
		t.Fatalf("state=%s fireCount=%d, want fired/1", got.State, got.FireCount)
	}
}

func TestFileFamilyNoChangeNoFire(t *testing.T) {
	s, e, prompts := newTestEngine(t)
	os.WriteFile(filepath.Join(e.workingDir, "watched.txt"), []byte("v1"), 0o644)
	_, err := s.Put(Intention{
		Statement:     "noop task",
		CueFamily:     CueFile,
		CuePattern:    "watched.txt",
		OriginExcerpt: "when watched.txt changes, noop task",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	e.Poll()
	e.Poll()
	e.Poll()
	if len(*prompts) != 0 {
		t.Fatalf("fired without change: %v", *prompts)
	}
}

func TestRepeatCooldownSuppressesStorm(t *testing.T) {
	s, e, prompts := newTestEngine(t)
	in, err := s.Put(Intention{
		Statement:     "redeploy",
		CueFamily:     CueEvent,
		CuePattern:    "build finished",
		OriginExcerpt: "each time a build finishes, redeploy",
		Repeat:        true,
		CooldownSec:   3600,
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	e.Observe("ci", "build finished ok")
	if len(*prompts) != 1 {
		t.Fatalf("first observation should fire, got %d", len(*prompts))
	}
	e.Observe("ci", "another build finished event")
	if len(*prompts) != 1 {
		t.Fatalf("cooldown should suppress immediate refire, got %d", len(*prompts))
	}
	got, _ := s.FindByID(in.ID)
	if got.State != StatePending || got.FireCount != 1 {
		t.Fatalf("repeat intention state=%s fires=%d, want pending/1", got.State, got.FireCount)
	}
}

func TestEventFamilyMatching(t *testing.T) {
	s, e, prompts := newTestEngine(t)
	_, err := s.Put(Intention{
		Statement:     "ping the channel",
		CueFamily:     CueEvent,
		CuePattern:    "CI green on #5257",
		OriginExcerpt: "when CI is green on #5257, ping the channel",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	e.Observe("tool_result", "unrelated output")
	if len(*prompts) != 0 {
		t.Fatal("non-matching observation fired")
	}
	e.Observe("tool_result", "checks: CI green on #5257 (all passing)")
	if len(*prompts) != 1 {
		t.Fatalf("matching observation should fire once, got %d", len(*prompts))
	}
	e.Observe("tool_result", "still CI green on #5257")
	if len(*prompts) != 1 {
		t.Fatal("fire-once violated for event family")
	}
}

func TestTimeFamilyFiresWhenDue(t *testing.T) {
	s, e, prompts := newTestEngine(t)
	in, err := s.Put(Intention{
		Statement:     "weekly report",
		CueFamily:     CueTime,
		CuePattern:    "0 9 * * 1",
		OriginExcerpt: "every Monday at 9, weekly report",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	e.Poll() // schedules nextFire, does not fire
	if len(*prompts) != 0 {
		t.Fatal("time family fired before due")
	}
	// Force the schedule due.
	e.mu.Lock()
	e.nextFire[in.ID] = time.Now().Add(-time.Minute)
	e.mu.Unlock()
	e.Poll()
	if len(*prompts) != 1 {
		t.Fatalf("due time cue should fire, got %d", len(*prompts))
	}
	got, _ := s.FindByID(in.ID)
	if got.State != StateFired {
		t.Fatalf("state = %s, want fired", got.State)
	}
}

func TestQueueIfBusyPassedThrough(t *testing.T) {
	s, e, prompts := newTestEngine(t)
	var queueFlags []bool
	e.emit = func(prompt string, queueIfBusy bool) {
		*prompts = append(*prompts, prompt)
		queueFlags = append(queueFlags, queueIfBusy)
	}
	_, err := s.Put(Intention{
		Statement:     "task",
		CueFamily:     CueEvent,
		CuePattern:    "trigger",
		OriginExcerpt: "when trigger, task",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	e.Observe("test", "trigger happened")
	if len(queueFlags) != 1 || !queueFlags[0] {
		t.Fatalf("queueIfBusy = %v, want [true]", queueFlags)
	}
}

func TestNilAndEmptySafe(t *testing.T) {
	var e *Engine
	e.Poll()
	e.Observe("k", "v")
	e.Start()
	e.Stop()
	e.Stop() // double stop

	// Empty store engine.
	_, e2, _ := newTestEngine(t)
	e2.Poll()
	e2.Start()
	defer e2.Stop()
	time.Sleep(50 * time.Millisecond)
}
