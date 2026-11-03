package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIssue3064_DiffChangedDetectsDeletion(t *testing.T) {
	base := map[string]fileFingerprint{
		"a.txt": {mtime: time.Unix(1, 0), size: 10},
		"b.txt": {mtime: time.Unix(1, 0), size: 20},
	}
	cur := map[string]fileFingerprint{

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

	time.Sleep(80 * time.Millisecond)
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

	time.Sleep(80 * time.Millisecond)
	os.Remove(del)
	os.WriteFile(mod, []byte("22222"), 0o644)
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

	time.Sleep(80 * time.Millisecond)
	os.WriteFile(file, []byte("v2-longer"), 0o644)
	if !waitFor(2*time.Second, func() bool { return fires.Load() == 1 }) {
		t.Fatalf("first fire missing, fires=%d", fires.Load())
	}

	os.WriteFile(file, []byte("v3-totally-different"), 0o644)
	time.Sleep(500 * time.Millisecond)
	if n := fires.Load(); n != 1 {
		t.Fatalf("cooldown violated: fires=%d during active window, want 1", n)
	}

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
