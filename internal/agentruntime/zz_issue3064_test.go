package agentruntime

// Regression probes for #3064 (file_watch_trigger.go, r372):
//   V1: deletions never fired - diffChanged only swept `current`, so
//       baseline-only paths (deleted files) were invisible, and a fire
//       triggered by a sibling file silently consumed the deletion via
//       the snapshot replacement.
//   V2: a change settling inside the cooldown window was permanently
//       dropped - the settle branch advanced the baseline BEFORE checking
//       the cooldown, so the event could never be re-detected.

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestIssue3064_DiffChangedDetectsDeletion is the minimal unit anchor:
// a baseline-only path must surface as changed.
func TestIssue3064_DiffChangedDetectsDeletion(t *testing.T) {
	base := map[string]fileFingerprint{
		"a.txt": {mtime: time.Unix(1, 0), size: 10},
		"b.txt": {mtime: time.Unix(1, 0), size: 20},
	}
	cur := map[string]fileFingerprint{
		// a.txt deleted (baseline-only), b.txt modified.
		"b.txt": {mtime: time.Unix(2, 0), size: 99},
	}
	changed := diffChanged(base, cur)
	if !changed["a.txt"] {
		t.Error("deleted path (baseline-only) not detected as changed")
	}
	if !changed["b.txt"] {
		t.Error("modified path not detected as changed")
	}
	if len(changed) != 2 {
		t.Errorf("changed = %v, want exactly a.txt+b.txt", changed)
	}
}

// TestIssue3064_DeletedFileFires: deleting the watched file must fire the
// prompt (previously len(changed)==0 forever -> pending cleared, no fire).
func TestIssue3064_DeletedFileFires(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "gone.txt")
	if err := os.WriteFile(file, []byte("rotate me"), 0o644); err != nil {
		t.Fatal(err)
	}
	fired := make(chan string, 2)
	trig := newTestWatch(dir, "gone.txt", "logs rotated: {files}", 3600, func(p string, q bool) { fired <- p })
	trig.Start()
	defer trig.Stop()

	time.Sleep(80 * time.Millisecond) // prime
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-fired:
		if !strings.Contains(p, "gone.txt") {
			t.Errorf("fired prompt missing deleted path: %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deletion of watched file never fired (#3064 V1)")
	}
	select {
	case p := <-fired:
		t.Fatalf("duplicate fire for one deletion: %q", p)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestIssue3064_MixedDeleteAndModifyIncludesDeletedPath: when a deletion
// settles together with a sibling modification, the fired change set must
// contain the deleted path too (previously the sibling's fire advanced the
// snapshot and silently swallowed the deletion).
func TestIssue3064_MixedDeleteAndModifyIncludesDeletedPath(t *testing.T) {
	dir := t.TempDir()
	del := filepath.Join(dir, "del.txt")
	mod := filepath.Join(dir, "mod.txt")
	os.WriteFile(del, []byte("x"), 0o644)
	os.WriteFile(mod, []byte("1"), 0o644)
	fired := make(chan string, 2)
	trig := newTestWatch(dir, "*.txt", "changed: {files}", 3600, func(p string, q bool) { fired <- p })
	trig.Start()
	defer trig.Stop()

	time.Sleep(80 * time.Millisecond) // prime
	os.Remove(del)
	os.WriteFile(mod, []byte("22222"), 0o644) // different size => fingerprint change
	select {
	case p := <-fired:
		if !strings.Contains(p, "del.txt") {
			t.Errorf("mixed change set lost the deleted path: %q", p)
		}
		if !strings.Contains(p, "mod.txt") {
			t.Errorf("mixed change set lost the modified path: %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mixed delete+modify never fired")
	}
}

// TestIssue3064_CooldownDefersNotDrops: an independent edit settling inside
// the cooldown window must be deferred and fire once after the cooldown
// expires - not permanently dropped by a premature baseline advance.
func TestIssue3064_CooldownDefersNotDrops(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "e.txt")
	os.WriteFile(file, []byte("v1"), 0o644)
	var fires atomic.Int32
	last := make(chan string, 4)
	trig := newTestWatch(dir, "e.txt", "edit seen: {files}", 1, func(p string, q bool) {
		fires.Add(1)
		last <- p
	})
	trig.Start()
	defer trig.Stop()

	time.Sleep(80 * time.Millisecond) // prime
	os.WriteFile(file, []byte("v2-longer"), 0o644)
	if !waitFor(2*time.Second, func() bool { return fires.Load() == 1 }) {
		t.Fatalf("first fire missing, fires=%d", fires.Load())
	}

	// Independent edit with different content settles INSIDE the 1s cooldown.
	os.WriteFile(file, []byte("v3-totally-different"), 0o644)
	time.Sleep(500 * time.Millisecond)
	if n := fires.Load(); n != 1 {
		t.Fatalf("cooldown violated: fires=%d during active window, want 1", n)
	}

	// After the 1s cooldown expires the settled v3 change must fire exactly once.
	if !waitFor(4*time.Second, func() bool { return fires.Load() == 2 }) {
		t.Fatalf("independent edit inside cooldown was dropped forever: fires=%d, want 2 (#3064 V2)", fires.Load())
	}
	select {
	case p := <-last:
		if !strings.Contains(p, "e.txt") {
			t.Errorf("deferred fire missing path: %q", p)
		}
	default:
	}
	time.Sleep(300 * time.Millisecond)
	if n := fires.Load(); n != 2 {
		t.Errorf("deferred change fired more than once: fires=%d", n)
	}
}
