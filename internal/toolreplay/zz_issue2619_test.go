package toolreplay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// #2619: Save must use a unique temp file per call and hold the write lock.
// Two saves racing on the same path used to share one fixed ".tmp" file and
// could ship a truncated tape. The fixed race here is: N savers round-trip
// through the same path; every intermediate LoadTape of the final file must
// parse, and no stray tmp files may remain.
func TestIssue2619ConcurrentSaveKeepsTapeParseable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.tape.json")

	// Seed: two distinct tapes, each saved by its own goroutine many times.
	mkTape := func(n int) *Tape {
		tp := NewTape()
		for i := 0; i < n; i++ {
			tp.Record(Entry{
				ToolName:  "read_file",
				Input:     json.RawMessage(`{"path":"/x"}`),
				InputHash: HashInput(json.RawMessage(`{"path":"/x"}`)),
				Result:    Result{Content: "ok"},
			})
		}
		return tp
	}
	tapes := []*Tape{mkTape(3), mkTape(7)}

	var wg sync.WaitGroup
	for _, tp := range tapes {
		for r := 0; r < 25; r++ {
			wg.Add(1)
			go func(tp *Tape) {
				defer wg.Done()
				if err := tp.Save(path); err != nil {
					t.Errorf("Save: %v", err)
				}
			}(tp)
		}
	}
	wg.Wait()

	// Final file must be valid JSON parseable by LoadTape (whole-file
	// content from exactly one writer - never a truncated interleave).
	loaded, err := LoadTape(path)
	if err != nil {
		t.Fatalf("final tape corrupt after concurrent saves: %v", err)
	}
	if loaded.Len() != 3 && loaded.Len() != 7 {
		t.Fatalf("loaded tape has %d entries; want 3 or 7 (one writer's whole file)", loaded.Len())
	}

	// No stray temp files may remain next to the tape.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "shared.tape.json" && e.Name() != "shared.tape.json.divergence.json" {
			t.Errorf("stray temp file left behind: %s", e.Name())
		}
	}
}

// Same-Tape-instance concurrent saves must also be safe (Lock, not RLock):
// the marshalled snapshot each Save writes must be internally consistent.
func TestIssue2619SameInstanceConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "one.tape.json")
	tp := NewTape()
	for i := 0; i < 5; i++ {
		tp.Record(Entry{
			ToolName:  "run_command",
			Input:     json.RawMessage(`{"command":"ls"}`),
			InputHash: HashInput(json.RawMessage(`{"command":"ls"}`)),
			Result:    Result{Content: "out"},
		})
	}
	var wg sync.WaitGroup
	for r := 0; r < 30; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tp.Save(path); err != nil {
				t.Errorf("Save: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := LoadTape(path); err != nil {
		t.Fatalf("tape corrupt after same-instance concurrent saves: %v", err)
	}
}
