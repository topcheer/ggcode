package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func zz1167ReadRaw(t *testing.T, dir string) ([]byte, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "perf-baseline.json"))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func TestZZIssue1167_RoundTripNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	runs := []perfBaselineEntry{
		{RunID: "run-1", Iterations: 3, ToolCalls: 10, Success: true},
		{RunID: "run-2", Iterations: 4, ToolCalls: 12, Success: true},
	}

	savePerfBaseline(dir, runs)

	got := loadPerfBaseline(dir)
	if len(got) != 2 {
		t.Fatalf("loaded %d runs, want 2", len(got))
	}
	if got[0].RunID != "run-1" || got[1].RunID != "run-2" {
		t.Fatalf("loaded run IDs = %q, %q; want run-1, run-2", got[0].RunID, got[1].RunID)
	}

	leftovers, err := filepath.Glob(filepath.Join(dir, ".ggcode", ".ggcode-tmp-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temp files left behind after successful save: %v", leftovers)
	}
}

func TestZZIssue1167_ConcurrentSaveNoCorruptReads(t *testing.T) {
	dir := t.TempDir()

	const writers = 8
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			for round := 0; round < 10; round++ {
				savePerfBaseline(dir, []perfBaselineEntry{
					{RunID: "writer-" + string(rune('a'+i)), Success: true},
				})
			}
		}(i)
	}

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, ok := zz1167ReadRaw(t, dir)
			if !ok {
				continue
			}
			var d perfBaselineData
			if err := json.Unmarshal(data, &d); err != nil {
				head := data
				if len(head) > 64 {
					head = head[:64]
				}
				t.Errorf("concurrent read observed corrupt file: %v (len=%d head=%q)", err, len(data), string(head))
				return
			}
		}
	}()

	wg.Wait()
	close(stop)
	<-readerDone

	got := loadPerfBaseline(dir)
	if len(got) != 1 {
		t.Fatalf("final file has %d runs, want exactly 1 complete snapshot", len(got))
	}
	if len(got[0].RunID) < 6 || got[0].RunID[:6] != "writer" {
		t.Fatalf("final run ID = %q, want a complete writer snapshot", got[0].RunID)
	}
}

func TestZZIssue1167_ErrorPathLeavesNoPartial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only dir permission test not meaningful on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}

	dir := t.TempDir()
	keep := []perfBaselineEntry{{RunID: "keep", Success: true}}
	savePerfBaseline(dir, keep)

	ggDir := filepath.Join(dir, ".ggcode")
	if err := os.Chmod(ggDir, 0500); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}

	savePerfBaseline(dir, []perfBaselineEntry{{RunID: "bad", Success: true}})

	_ = os.Chmod(ggDir, 0755)

	got := loadPerfBaseline(dir)
	if len(got) != 1 || got[0].RunID != "keep" {
		t.Fatalf("baseline clobbered by failed save: %+v, want only 'keep'", got)
	}

	leftovers, err := filepath.Glob(filepath.Join(ggDir, ".ggcode-tmp-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("failed save left temp files behind: %v", leftovers)
	}
}
