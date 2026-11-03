package tool

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

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

	if err := atomicWriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	mu.Lock()
	a, b := hitsA, hitsB
	mu.Unlock()
	if a != 1 || b != 1 {
		t.Fatalf("expected both hooks to fire once, got A=%d B=%d", a, b)
	}

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
