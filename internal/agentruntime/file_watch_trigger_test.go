package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// newTestWatch builds a trigger with a fast tick for deterministic tests.
func newTestWatch(dir, glob, prompt string, cooldownSec int, emit func(string, bool)) *FileWatchTrigger {
	t := NewFileWatchTrigger([]config.WatchTriggerConfig{{
		Globs:       []string{glob},
		Prompt:      prompt,
		CooldownSec: cooldownSec,
	}}, dir, emit)
	t.interval = 15 * time.Millisecond
	return t
}

// waitFor polls cond until true or the deadline passes.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestFileWatchTrigger_FiresOnSettledChange(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu chan string = make(chan string, 4)
	trig := newTestWatch(dir, "a.txt", "run tests for {files}", 3600, func(p string, q bool) {
		mu <- p
	})
	trig.Start()
	defer trig.Stop()

	// Startup must NOT fire: existing files only prime the baseline.
	select {
	case p := <-mu:
		t.Fatalf("fired on startup without any change: %q", p)
	case <-time.After(150 * time.Millisecond):
	}

	// Change the file; expect exactly one fire after the change settles.
	if err := os.WriteFile(file, []byte("v2-longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-mu:
		if !strings.Contains(p, "run tests for") || !strings.Contains(p, "a.txt") {
			t.Errorf("prompt missing template expansion: %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no fire after settled change")
	}
	select {
	case p := <-mu:
		t.Fatalf("duplicate fire for one change: %q", p)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestFileWatchTrigger_CooldownSuppressesStorm(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var fires atomic.Int32
	done := make(chan struct{})
	trig := newTestWatch(dir, "b.txt", "check", 3600, func(p string, q bool) {
		fires.Add(1)
	})
	trig.Start()
	defer trig.Stop()

	// Prime + settle one change.
	time.Sleep(80 * time.Millisecond)
	os.WriteFile(file, []byte("y1"), 0o644)
	if !waitFor(2*time.Second, func() bool { return fires.Load() == 1 }) {
		t.Fatalf("first fire missing, fires=%d", fires.Load())
	}
	// Storm: repeated edits inside the cooldown must not re-fire.
	for i := 0; i < 5; i++ {
		os.WriteFile(file, []byte("storm"+string(rune('a'+i))), 0o644)
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if n := fires.Load(); n != 1 {
		t.Errorf("cooldown violated: fires=%d, want 1", n)
	}
	close(done)
	<-done
}

func TestFileWatchTrigger_NoChangeNoFire(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("stable"), 0o644)
	fired := make(chan string, 1)
	trig := newTestWatch(dir, "c.txt", "should not fire", 0, func(p string, q bool) { fired <- p })
	trig.Start()
	defer trig.Stop()
	select {
	case p := <-fired:
		t.Fatalf("fired with no change: %q", p)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestFileWatchTrigger_NilAndEmptySafe(t *testing.T) {
	var nilTrig *FileWatchTrigger
	nilTrig.Start()
	nilTrig.Stop()
	empty := NewFileWatchTrigger(nil, t.TempDir(), nil)
	empty.Start() // no triggers: no goroutine
	empty.Stop()
}

func TestFileWatchTrigger_QueueIfBusyPassedThrough(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "d.txt")
	os.WriteFile(file, []byte("1"), 0o644)
	gotQ := make(chan bool, 2)
	trig := NewFileWatchTrigger([]config.WatchTriggerConfig{{
		Globs:       []string{"d.txt"},
		Prompt:      "p",
		QueueIfBusy: true,
	}}, dir, func(p string, q bool) { gotQ <- q })
	trig.interval = 15 * time.Millisecond
	trig.Start()
	defer trig.Stop()
	time.Sleep(80 * time.Millisecond)
	os.WriteFile(file, []byte("22"), 0o644)
	select {
	case q := <-gotQ:
		if !q {
			t.Error("queue_if_busy=false not passed through")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no fire")
	}
}
