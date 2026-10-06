package agent

// Issue #2726 probe: Record's load->modify->save ran with only an
// instance-level mutex while recordPlaybook creates a FRESH Playbook per
// run. Two ggcode processes (or instances) recording near-simultaneously
// read the same on-disk state and the last writer erased the other's
// changes (lost update). The fix serializes the critical section with a
// cross-process file lock.
//
// Fingerprint caveat that shapes this probe: taskType|toolSeq|fileTypes
// collapses many distinct runs onto ONE entry (same prompt class + same
// tools + same extension). Both loss shapes are probed: same-fingerprint
// concurrent increments must all land (Uses == N), and genuinely distinct
// extensions must all survive as entries.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func runStatsFor(ext string) *RunStats {
	return &RunStats{
		Success:     true,
		UserPrompt:  "refactor the worker for correctness",
		ToolCalls:   map[string]int{"read_file": 2, "edit_file": 2},
		FilesEdited: []string{"/pkg/module" + ext},
		Iterations:  4,
		Duration:    30 * time.Second,
	}
}

// Ten concurrent Records with the SAME fingerprint all update one entry;
// every increment must land (Uses == 10). Under the lost-update bug most
// increments are erased (Uses well below 10).
func TestIssue2726ConcurrentIncrementsAllLand(t *testing.T) {
	dir := t.TempDir()
	const n = 10
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pb := NewPlaybook(dir)
			if pb == nil {
				t.Errorf("NewPlaybook returned nil")
				return
			}
			pb.Record(runStatsFor(".go"))
		}()
	}
	close(start)
	wg.Wait()

	entry := readSinglePlaybookEntry(t, dir)
	if entry.Uses != n {
		t.Fatalf("lost updates: single-entry Uses = %d, want %d (increments erased by last-writer-wins)", entry.Uses, n)
	}
}

// Ten concurrent Records with DISTINCT extensions produce ten entries; none
// may be erased by a later full-file writer.
func TestIssue2726ConcurrentDistinctEntriesAllSurvive(t *testing.T) {
	dir := t.TempDir()
	exts := []string{".go", ".py", ".ts", ".js", ".rs", ".rb", ".java", ".c", ".css", ".md"}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, ext := range exts {
		wg.Add(1)
		go func(ext string) {
			defer wg.Done()
			<-start
			pb := NewPlaybook(dir)
			if pb == nil {
				t.Errorf("NewPlaybook returned nil")
				return
			}
			pb.Record(runStatsFor(ext))
		}(ext)
	}
	close(start)
	wg.Wait()

	if got := countPlaybookEntries(t, dir); got != len(exts) {
		t.Fatalf("lost updates: %d/%d entries survived (last-writer-wins erased %d)", got, len(exts), len(exts)-got)
	}
}

func readPlaybookFile(t *testing.T, dir string) []PlaybookEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "playbook.json"))
	if err != nil {
		t.Fatalf("read playbook: %v", err)
	}
	var entries []PlaybookEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return entries
}

func countPlaybookEntries(t *testing.T, dir string) int {
	t.Helper()
	return len(readPlaybookFile(t, dir))
}

func readSinglePlaybookEntry(t *testing.T, dir string) PlaybookEntry {
	t.Helper()
	entries := readPlaybookFile(t, dir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry (collapsed fingerprint), got %d", len(entries))
	}
	return entries[0]
}

var _ = fmt.Sprintf // keep fmt if assertions change shape
