package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIssue4428LockFileFieldRaceClean(t *testing.T) {
	dir := t.TempDir()
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if m.tryLock() {
				m.unlock()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			m.unlock()
		}
	}()
	wg.Wait()

	m.mu.Lock()
	stale := m.lockFile
	m.mu.Unlock()
	if stale != nil {
		t.Fatal("lockFile left set after balanced acquire/release cycles")
	}
	if !m.tryLock() {
		t.Fatal("tryLock must succeed after hammer (no leaked flock)")
	}
	m.unlock()
}

func TestIssue4428DirtyChannelFiltersNonCodeFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("package p\nvar keepme int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())

	m.mu.RLock()
	before := len(m.index.docs)
	m.mu.RUnlock()

	bin := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(bin, []byte("uniquetermbinpayload uniqbinx termz"), 0644); err != nil {
		t.Fatal(err)
	}
	m.MarkDirty([]string{bin})
	m.rebuildDirty("test")

	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.index.docs) != before {
		t.Fatalf("non-code .bin entered the corpus via dirty channel: docs %d -> %d", before, len(m.index.docs))
	}
}

func TestIssue4428DirtyAppendHonorsTermBudget(t *testing.T) {
	dir := t.TempDir()
	seed := "package p\n"
	for j := 0; j < 20; j++ {
		seed += "var seedvar" + itoa(j) + " int\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.go"), []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", "30")
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())

	m.mu.RLock()
	before := len(m.index.docs)
	m.mu.RUnlock()

	extra := "package p\n"
	for j := 0; j < 40; j++ {
		extra += "var extravar" + itoa(j) + " int\n"
	}
	extraPath := filepath.Join(dir, "extra.go")
	if err := os.WriteFile(extraPath, []byte(extra), 0644); err != nil {
		t.Fatal(err)
	}
	m.MarkDirty([]string{extraPath})
	m.rebuildDirty("test")

	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.index.docs) != before {
		t.Fatalf("append bypassed term budget: docs %d -> %d", before, len(m.index.docs))
	}

	if m.index == nil || len(m.index.docs) == 0 {
		t.Fatal("index lost its seed doc after budget skip")
	}
}

func TestIssue4428DirtyChannelStillIndexesCodeFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\nvar first int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())

	b := filepath.Join(dir, "b.go")
	if err := os.WriteFile(b, []byte("package p\nvar addedmarker int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.MarkDirty([]string{b})
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.RLock()
		n := len(m.index.docs)
		m.mu.RUnlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dirty channel failed to index a legit .go file: docs=%d", n)
		}
		m.rebuildDirty("test")
		time.Sleep(10 * time.Millisecond)
	}

	results, err := m.Search("addedmarker", 5)
	found := false
	for _, r := range results {
		if strings.Contains(r.path, "b.go") {
			found = true
		}
	}
	if err == nil && !found {
		t.Fatalf("addedmarker not searchable: %+v (err=%v)", results, err)
	}
}
