package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestIssue4413ConcurrentForgetsAllPersisted(t *testing.T) {
	am := newTestAutoMemory(t)
	const n = 12
	for i := 0; i < n; i++ {
		if err := am.SaveMemory("issue4413-concurrent", "payload "+time.Now().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = am.ForgetKey("issue4413-concurrent")
		}(i)
	}
	wg.Wait()

	f := am.loadForgotten()
	safe := disambiguateKey("issue4413-concurrent", sanitizeKey("issue4413-concurrent"))
	if _, ok := f.Entries[safe]; !ok {
		t.Fatalf("forget mark lost under concurrency: sidecar has %d entries, want mark for %s", len(f.Entries), safe)
	}
}

func TestIssue4413ForgetRestoreConcurrentConsistency(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("issue4413-fr", "payload"); err != nil {
		t.Fatalf("save: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_ = am.ForgetKey("issue4413-fr")
			} else {
				_ = am.RestoreKey("issue4413-fr")
			}
		}(i)
	}
	wg.Wait()

	raw, err := os.ReadFile(filepath.Join(am.dir, forgottenFileName))
	if err != nil {
		t.Fatalf("sidecar missing after hammer: %v", err)
	}
	if !jsonValid(raw) {
		t.Fatalf("sidecar corrupt after concurrent forget/restore hammer: %q", raw)
	}
	_ = am.loadForgotten()
}

func jsonValid(b []byte) bool {
	var v any
	return json.Unmarshal(b, &v) == nil
}

func TestIssue4416ConcurrentSaveDuringGCSurvives(t *testing.T) {
	am := newTestAutoMemory(t)

	old := filepath.Join(am.dir, sanitizeKey("issue4416-expired")+".md")
	if err := os.WriteFile(old, []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		am.GarbageCollect()
	}()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := am.SaveMemory("issue4416-live", "fresh content "+time.Now().Format(time.RFC3339Nano)); err != nil {
				t.Errorf("concurrent save: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, _, found, err := am.ReadMemoryAsOf("issue4416-live", time.Now()); err != nil || !found {
		t.Fatalf("freshly saved memory lost to concurrent GC (found=%v err=%v)", found, err)
	}
}
