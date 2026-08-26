package tool

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestAddPreWriteHookFanOut guards #1047: multiple scoped hooks must ALL
// observe pre-writes (the old single package-global meant concurrent ACP
// sessions overwrote each other's hook - last-writer-wins).
func TestAddPreWriteHookFanOut(t *testing.T) {
	var mu sync.Mutex
	hitsA, hitsB := 0, 0

	removeA := AddPreWriteHook(func(path, old, new, call string) error {
		mu.Lock()
		hitsA++
		mu.Unlock()
		return nil
	})
	removeB := AddPreWriteHook(func(path, old, new, call string) error {
		mu.Lock()
		hitsB++
		mu.Unlock()
		return nil
	})
	defer removeB()

	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	// A write through the atomic path must reach both hooks with old content.
	if err := atomicWriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	mu.Lock()
	a, b := hitsA, hitsB
	mu.Unlock()
	if a != 1 || b != 1 {
		t.Fatalf("expected both hooks to fire once, got A=%d B=%d", a, b)
	}

	// Removing one hook must not affect the other.
	removeA()
	if err := atomicWriteFile(path, []byte("newer"), 0644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	mu.Lock()
	a, b = hitsA, hitsB
	mu.Unlock()
	if a != 1 {
		t.Fatalf("removed hook must not fire again, got A=%d", a)
	}
	if b != 2 {
		t.Fatalf("remaining hook must keep firing, got B=%d", b)
	}
}

// TestAddPreWriteHookAbortsOnError ensures a scoped hook can still abort the
// write, matching the single-slot SetPreWriteHook semantics.
func TestAddPreWriteHookAbortsOnError(t *testing.T) {
	remove := AddPreWriteHook(func(path, old, new, call string) error {
		return errors.New("deny")
	})
	defer remove()

	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(path, []byte("new"), 0644); err == nil {
		t.Fatal("expected write to be aborted by hook error")
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatalf("file must be unchanged after aborted write, got %q", got)
	}
}
