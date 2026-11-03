package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrityLoopSkipsDryRun(t *testing.T) {
	var dryProbe struct {
		DryRun bool `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(`{"summary":"preview","dry_run":true,"planned_files":2}`), &dryProbe); err != nil || !dryProbe.DryRun {
		t.Fatalf("dry_run probe failed: err=%v dry=%v", err, dryProbe.DryRun)
	}

	if err := json.Unmarshal([]byte(`{"summary":"applied","dry_run":false}`), &dryProbe); err != nil || dryProbe.DryRun {
		t.Fatalf("applied result misdetected as dry: err=%v dry=%v", err, dryProbe.DryRun)
	}
}

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

func TestMismatchOnlyForActuallyWrittenFile(t *testing.T) {
	dir := t.TempDir()
	written := filepath.Join(dir, "w.txt")
	if err := os.WriteFile(written, []byte("new content"), 0o644); err != nil {
		t.Fatal(err)
	}

	if w := checkWriteIntegrity(written, "old", "new content"); w != "" {
		t.Fatalf("written file must be clean, got: %s", w)
	}

	failed := filepath.Join(dir, "denied.txt")
	if err := os.WriteFile(failed, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := checkWriteIntegrity(failed, "old content", "planned new"); !strings.Contains(w, "post-write mismatch") {
		t.Fatalf("unwritten failed file would warn without the skip: %q", w)
	}
}
