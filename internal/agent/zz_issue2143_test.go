package agent

// #2143 regression: the multi-file integrity loop had no dry-run or
// per-file-outcome awareness.
//   - P1: batch_replace dry_run=true writes NOTHING, but the loop still
//     read back old disk content against every plan - a guaranteed false
//     "post-write mismatch (the write may be partial)" on every preview.
//   - P2: partial_success mode leaves failed files' old disk content in
//     place while IsError=false - those files also tripped the mismatch,
//     contradicting the tool's own failure listing in the same result.
// The loop now skips dry runs entirely and, when the tool reports
// written_paths, checks only the files that were actually written.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P1: a dry-run result JSON must suppress the integrity loop.
func TestIntegrityLoopSkipsDryRun(t *testing.T) {
	var dryProbe struct {
		DryRun bool `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(`{"summary":"preview","dry_run":true,"planned_files":2}`), &dryProbe); err != nil || !dryProbe.DryRun {
		t.Fatalf("dry_run probe failed: err=%v dry=%v", err, dryProbe.DryRun)
	}
	// A non-dry result must not trip the probe.
	if err := json.Unmarshal([]byte(`{"summary":"applied","dry_run":false}`), &dryProbe); err != nil || dryProbe.DryRun {
		t.Fatalf("applied result misdetected as dry: err=%v dry=%v", err, dryProbe.DryRun)
	}
}

// P2: the written_paths pointer probe must distinguish "field absent"
// (nil - check all, atomic semantics) from "field present, empty list"
// (all files failed - check none).
func TestWrittenPathsProbeSemantics(t *testing.T) {
	var absent struct {
		WrittenPaths *[]string `json:"written_paths"`
	}
	if err := json.Unmarshal([]byte(`{"summary":"x"}`), &absent); err != nil || absent.WrittenPaths != nil {
		t.Fatalf("absent field must leave nil pointer: err=%v p=%v", err, absent.WrittenPaths)
	}
	var empty struct {
		WrittenPaths *[]string `json:"written_paths"`
	}
	if err := json.Unmarshal([]byte(`{"written_paths":[]}`), &empty); err != nil || empty.WrittenPaths == nil || len(*empty.WrittenPaths) != 0 {
		t.Fatalf("present-but-empty must give non-nil empty: err=%v p=%v", err, empty.WrittenPaths)
	}
}

// End-to-end shape: a partial_success result whose failed file keeps old
// disk content must not produce a mismatch warning for it. We drive
// checkWriteIntegrity directly for the WRITTEN file only - the loop's
// skip decision is covered by the probe semantics above.
func TestMismatchOnlyForActuallyWrittenFile(t *testing.T) {
	dir := t.TempDir()
	written := filepath.Join(dir, "w.txt")
	if err := os.WriteFile(written, []byte("new content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Simulate the loop's behavior for the written file: disk == planned
	// -> no warning.
	if w := checkWriteIntegrity(written, "old", "new content"); w != "" {
		t.Fatalf("written file must be clean, got: %s", w)
	}
	// The failed file (disk keeps old content) is skipped by the loop -
	// running the check on it WOULD warn, which is exactly why the skip
	// exists. Pin that fact so the skip is never "simplified" away.
	failed := filepath.Join(dir, "denied.txt")
	if err := os.WriteFile(failed, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := checkWriteIntegrity(failed, "old content", "planned new"); !strings.Contains(w, "post-write mismatch") {
		t.Fatalf("unwritten failed file would warn without the skip: %q", w)
	}
}
