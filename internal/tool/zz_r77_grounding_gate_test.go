package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r77 evidence-grounding gate (arXiv:2605.08828): once
// detectChangedFilesFromCommand detects that a command externally modified a
// previously-read file, ChangedSince re-stats the mtime baseline - which used
// to make every subsequent stale check pass SILENTLY. The dirty flag now
// preserves the agent's knowledge boundary until re-read/own-write, and write
// tools refuse the path instead of acting on stale in-context content.

func r77ExternalCommandModify(t *testing.T, p, newContent string) {
	t.Helper()
	snap := defaultFileTracker.SnapshotTracked()
	if err := os.WriteFile(p, []byte(newContent), 0o644); err != nil {
		t.Fatalf("external modify: %v", err)
	}
	// Force mtime forward so the detection is deterministic on filesystems
	// with coarse mtime granularity.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if notice := detectChangedFilesFromCommand(snap); notice == "" {
		t.Fatal("setup: external modification was not detected")
	}
}

func TestR77DirtyFlagLifecycle(t *testing.T) {
	tracker := NewFileIntegrityTracker()
	p := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	tracker.RecordRead(p)
	if dirty, _ := tracker.CheckExternalMod(p); dirty {
		t.Fatal("fresh read must not be dirty")
	}

	// External modification detected by a command run.
	snap := tracker.SnapshotTracked()
	if err := os.WriteFile(p, []byte("v2-external"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, future, future)
	if changed := tracker.ChangedSince(snap); len(changed) != 1 {
		t.Fatalf("ChangedSince must report the change, got %v", changed)
	}
	if dirty, at := tracker.CheckExternalMod(p); !dirty {
		t.Fatal("externally-modified, not-re-read file must be dirty")
	} else if at.IsZero() {
		t.Fatal("dirty detection time must be set")
	}

	// CheckStale now passes (baseline re-stat) - the exact silent hole the
	// dirty flag closes.
	if stale, _ := tracker.CheckStale(p); stale {
		t.Fatal("baseline was re-stat'ed: CheckStale passes (documented behavior)")
	}

	// Re-observation lifts the gate.
	tracker.RecordRead(p)
	if dirty, _ := tracker.CheckExternalMod(p); dirty {
		t.Fatal("RecordRead must clear dirty")
	}

	// Own write is an authoritative observation.
	tracker.RecordRead(p)
	snap = tracker.SnapshotTracked()
	if err := os.WriteFile(p, []byte("v3-external"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, future.Add(time.Second), future.Add(time.Second))
	tracker.ChangedSince(snap)
	if dirty, _ := tracker.CheckExternalMod(p); !dirty {
		t.Fatal("second external modification must re-dirty")
	}
	tracker.RecordWrite(p)
	if dirty, _ := tracker.CheckExternalMod(p); dirty {
		t.Fatal("RecordWrite must clear dirty")
	}

	// RemoveTracking clears both maps.
	snap = tracker.SnapshotTracked()
	_ = os.WriteFile(p, []byte("v4"), 0o644)
	_ = os.Chtimes(p, future.Add(2*time.Second), future.Add(2*time.Second))
	tracker.ChangedSince(snap)
	tracker.RemoveTracking(p)
	if dirty, _ := tracker.CheckExternalMod(p); dirty {
		t.Fatal("RemoveTracking must clear dirty")
	}
	if tracker.HasBeenSeen(p) {
		t.Fatal("RemoveTracking must drop the path entirely")
	}

	// Reset clears everything.
	tracker.RecordRead(p)
	tracker.Reset()
	if dirty, _ := tracker.CheckExternalMod(p); dirty {
		t.Fatal("Reset must clear dirty")
	}
}

func TestR77EditFileGatedAfterExternalCommand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "code.txt")
	if err := os.WriteFile(p, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { defaultFileTracker.RemoveTracking(p) })

	// Agent reads the file, then runs a command that modifies it externally.
	defaultFileTracker.RecordRead(p)
	r77ExternalCommandModify(t, p, "alpha\nbeta\nEXTERNAL\n")

	// edit_file with old_text that STILL matches (the edit region survived
	// the external change): must be BLOCKED instead of silently proceeding.
	tool := EditFile{WorkingDir: t.TempDir()}
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+p+`","old_text":"alpha","new_text":"GAMMA"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("edit on externally-modified, not-re-read file must be blocked, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "external command") || !strings.Contains(res.Content, "read_file") {
		t.Fatalf("gate error must be actionable, got: %s", res.Content)
	}
	// The file must be untouched.
	if data, _ := os.ReadFile(p); string(data) != "alpha\nbeta\nEXTERNAL\n" {
		t.Fatalf("blocked edit must not touch the file, got %q", data)
	}

	// Re-read resolves the gate; the same edit then succeeds.
	defaultFileTracker.RecordRead(p)
	res, err = tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+p+`","old_text":"alpha","new_text":"GAMMA"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("edit after re-read must succeed, got: %s", res.Content)
	}
}

func TestR77MultiEditGatedAfterExternalCommand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "code.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { defaultFileTracker.RemoveTracking(p) })

	defaultFileTracker.RecordRead(p)
	r77ExternalCommandModify(t, p, "one\ntwo\nEXTERNAL\n")

	tool := MultiEditFile{WorkingDir: t.TempDir()}
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+p+`","edits":[{"old_text":"one","new_text":"1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "external command") {
		t.Fatalf("multi_edit must be gated after external modification, got: %s", res.Content)
	}

	defaultFileTracker.RecordRead(p)
	res, err = tool.Execute(context.Background(), json.RawMessage(
		`{"file_path":"`+p+`","edits":[{"old_text":"one","new_text":"1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("multi_edit after re-read must succeed, got: %s", res.Content)
	}
}

func TestR77WriteFileGatedAfterExternalCommand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "code.txt")
	if err := os.WriteFile(p, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { defaultFileTracker.RemoveTracking(p) })

	defaultFileTracker.RecordRead(p)
	r77ExternalCommandModify(t, p, "existing\nEXTERNAL\n")

	tool := WriteFile{WorkingDir: t.TempDir()}
	res, err := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"`+p+`","content":"blind overwrite"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "external command") {
		t.Fatalf("write_file must be gated after external modification, got: %s", res.Content)
	}

	defaultFileTracker.RecordRead(p)
	res, err = tool.Execute(context.Background(), json.RawMessage(
		`{"path":"`+p+`","content":"blind overwrite"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("write_file after re-read must succeed, got: %s", res.Content)
	}
}
