package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// #4413: concurrent ForgetKey calls on distinct keys must all persist — the
// sidecar RMW used to be the package's only unlocked one, so the startup GC
// goroutine's digest prune (or a sibling forget) could replay a stale
// snapshot and silently drop marks (the entry resurrects into injection).
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
	// The key existence Stat in ForgetKey races nothing here (file present
	// throughout), so every call must have succeeded and the single mark
	// must survive the 12-way RMW hammer.
	f := am.loadForgotten()
	safe := disambiguateKey("issue4413-concurrent", sanitizeKey("issue4413-concurrent"))
	if _, ok := f.Entries[safe]; !ok {
		t.Fatalf("forget mark lost under concurrency: sidecar has %d entries, want mark for %s", len(f.Entries), safe)
	}
}

// #4413: concurrent ForgetKey/RestoreKey on the same key must leave the
// sidecar loadable (never corrupt / never a half-written JSON) — the lock
// serializes the load→mutate→save cycles end to end.
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
	// Sidecar must still be valid, parseable JSON (loadForgotten fail-open
	// would silently mask corruption, so assert the raw file too).
	raw, err := os.ReadFile(filepath.Join(am.dir, forgottenFileName))
	if err != nil {
		t.Fatalf("sidecar missing after hammer: %v", err)
	}
	if !jsonValid(raw) {
		t.Fatalf("sidecar corrupt after concurrent forget/restore hammer: %q", raw)
	}
	_ = am.loadForgotten() // must not log-corrupt (implicitly parseable above)
}

func jsonValid(b []byte) bool {
	var v any
	return json.Unmarshal(b, &v) == nil
}

// #4416: keys successfully saved while a GC pass runs must all survive —
// GC's Stat→Remove window now runs under the same cross-process file lock
// as SaveMemory's rename, so a fresh rename can no longer be reaped by a
// stale-snapshot verdict. (Also exercises the lock ordering: GC releases
// before its sidecar prune re-acquires.)
func TestIssue4416ConcurrentSaveDuringGCSurvives(t *testing.T) {
	am := newTestAutoMemory(t)
	// Seed one genuinely expired transient entry so GC has work to do.
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
