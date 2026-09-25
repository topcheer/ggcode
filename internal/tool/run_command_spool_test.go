package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// freshSpool builds a spool writer wired to the production bounded writer
// configuration, rooted in a temp directory.
func freshSpool(t *testing.T) (*spoolOutputWriter, string) {
	t.Helper()
	dir := t.TempDir()
	sw := newSpoolOutputWriter(newBoundedOutputWriter(2*maxOutputSize), dir)
	return sw, filepath.Join(dir, spoolDirName, spoolSubDir)
}

// TestSpoolWriter_UnderThreshold_NoFile verifies the lazy contract: short
// output never touches the disk and produces no annotation.
func TestSpoolWriter_UnderThreshold_NoFile(t *testing.T) {
	sw, spoolDir := freshSpool(t)
	payload := strings.Repeat("hello\n", 100) // 600B
	if n, err := sw.Write([]byte(payload)); err != nil || n != len(payload) {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(payload))
	}
	entry := sw.finishEntry("stdout")
	if entry.Path != "" {
		t.Fatalf("expected no spool path for sub-threshold output, got %q", entry.Path)
	}
	if _, err := os.Stat(spoolDir); !os.IsNotExist(err) {
		t.Fatalf("spool directory should not exist for sub-threshold output, stat err = %v", err)
	}
}

// TestSpoolWriter_ExactFullStreamPreserved verifies the file holds the
// exact raw byte stream across multiple writes once activation triggers.
func TestSpoolWriter_ExactFullStreamPreserved(t *testing.T) {
	sw, _ := freshSpool(t)
	first := strings.Repeat("A", 70*1024)  // crosses spoolThreshold
	second := strings.Repeat("B", 12*1024) // mirrored after activation
	if _, err := sw.Write([]byte(first)); err != nil {
		t.Fatal(err)
	}
	if _, err := sw.Write([]byte(second)); err != nil {
		t.Fatal(err)
	}
	entry := sw.finishEntry("stdout")
	if entry.Path == "" {
		t.Fatal("expected spool path for over-threshold output")
	}
	got, err := os.ReadFile(entry.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := first + second
	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("spool file is not the exact stream: got %d bytes, want %d", len(got), len(want))
	}
	if entry.Size != int64(len(want)) {
		t.Fatalf("entry.Size = %d, want %d", entry.Size, len(want))
	}
}

// TestSpoolWriter_BeyondBoundedCap_NoHole is the core regression test: when
// output exceeds the bounded writer's 200KB capture cap, the inline view
// loses the middle - but the spool file must not.
func TestSpoolWriter_BeyondBoundedCap_NoHole(t *testing.T) {
	sw, _ := freshSpool(t)
	total := 350 * 1024 // > head(100KB) + tail staging cap(200KB): real drops
	payload := make([]byte, total)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	// Write in chunks: a single write is retained one-shot by the bounded
	// writer (staging compaction only fires across writes).
	if _, err := sw.Write(payload[:200*1024]); err != nil {
		t.Fatal(err)
	}
	if _, err := sw.Write(payload[200*1024:]); err != nil {
		t.Fatal(err)
	}
	entry := sw.finishEntry("stdout")
	if entry.Path == "" {
		t.Fatal("expected spool path for over-cap output")
	}
	got, err := os.ReadFile(entry.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("spool file lost data: got %d bytes, want %d (exact match failed)", len(got), len(payload))
	}
	// Sanity: the bounded inline view really is lossy at this size, i.e. the
	// spool is carrying information the inline result cannot.
	if len(sw.bounded.String()) >= total {
		t.Fatalf("expected bounded view to be smaller than %d bytes", total)
	}
}

// TestSpoolWriter_ActivationFails_DegradesSilently verifies the failure
// path: an unusable spool root must never break command output capture.
func TestSpoolWriter_ActivationFails_DegradesSilently(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// spool root <blocker>/.ggcode/spool cannot be created under a file.
	sw := newSpoolOutputWriter(newBoundedOutputWriter(2*maxOutputSize), filepath.Join(dir, "not-a-dir"))
	payload := strings.Repeat("A", 70*1024)
	n, err := sw.Write([]byte(payload))
	if err != nil || n != len(payload) {
		t.Fatalf("Write after failed activation = (%d, %v), want (%d, nil)", n, err, len(payload))
	}
	entry := sw.finishEntry("stdout")
	if entry.Path != "" {
		t.Fatalf("degraded spool must not report a path, got %q", entry.Path)
	}
}

// TestSpoolWriter_PruneStale verifies best-effort housekeeping removes old
// spool artifacts when a new one is created.
func TestSpoolWriter_PruneStale(t *testing.T) {
	sw, spoolDir := freshSpool(t)
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(spoolDir, "run-stale.log")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-spoolMaxAge - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Skipf("cannot set file times on this platform: %v", err)
	}
	if _, err := sw.Write([]byte(strings.Repeat("A", 70*1024))); err != nil {
		t.Fatal(err)
	}
	sw.finishEntry("stdout")
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale spool file should have been pruned, stat err = %v", err)
	}
}

// TestSpoolNote_Formatting covers the annotation text for both streams,
// the size-less degraded variant, and the empty case.
func TestSpoolNote_Formatting(t *testing.T) {
	if got := spoolNote(); got != "" {
		t.Fatalf("empty entries should render nothing, got %q", got)
	}
	note := spoolNote(
		spoolEntry{Label: "stdout", Path: "/tmp/a.log", Size: 70 * 1024},
		spoolEntry{Label: "stderr", Path: "/tmp/b.log", Size: -1},
	)
	if !strings.Contains(note, "/tmp/a.log") || !strings.Contains(note, "70KB") {
		t.Fatalf("stdout entry missing: %q", note)
	}
	if !strings.Contains(note, "/tmp/b.log") {
		t.Fatalf("stderr entry missing: %q", note)
	}
	if !strings.Contains(note, "instead of re-running") {
		t.Fatalf("guidance missing: %q", note)
	}
	if strings.Contains(note, "(-1") {
		t.Fatalf("degraded size must not render as -1: %q", note)
	}
}

// TestRunCommand_SpoolsLongOutput wires the whole path: a command emitting
// more than maxOutputSize must produce a truncated inline result with a
// spool annotation pointing at a file holding the full log.
func TestRunCommand_SpoolsLongOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("awk-based payload generation is unix-only")
	}
	dir := t.TempDir()
	rc := RunCommand{WorkingDir: dir}
	// 200KB > maxOutputSize (100KB): inline truncation must trigger and the
	// stream must cross spoolThreshold.
	input, _ := json.Marshal(map[string]string{
		"command":     "awk 'BEGIN{for(i=0;i<200000;i++)printf \"A\"}'",
		"description": "emit long output",
	})
	res, err := rc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Skipf("command could not run (gate/sandbox): %s", res.Content)
	}
	if !strings.Contains(res.Content, "lines omitted") {
		t.Fatalf("expected inline truncation marker, got: %.200s", res.Content)
	}
	if !strings.Contains(res.Content, "full output saved") {
		t.Fatalf("expected spool annotation, got: %.400s", res.Content)
	}
	// Locate the spool artifact directly instead of parsing the annotation
	// string (paths may contain spaces).
	matches, err := filepath.Glob(filepath.Join(dir, spoolDirName, spoolSubDir, "run-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one spool file, got %v", matches)
	}
	got, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("annotated spool file unreadable: %v", err)
	}
	if len(got) != 200000 || strings.Count(string(got), "A") != 200000 {
		t.Fatalf("spool file wrong size/content: %d bytes", len(got))
	}
}
