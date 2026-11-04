package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #5026 (S-19 B-half, #5019 ruling option 2): a tracked path vacated by
// a run_command mv/rm must hard-fail write_file until acknowledged; read_file
// on the vacated path returns not-found + move hint and clears the marker.

func vacSetup5026(t *testing.T, dir, name string) string {
	t.Helper()
	defaultFileTracker.Reset()
	t.Cleanup(func() { defaultFileTracker.Reset() })

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("original content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Session read: the path becomes tracked.
	defaultFileTracker.RecordRead(path)
	snap := defaultFileTracker.SnapshotTracked()
	// Simulate run_command "mv <path> <other>" post-exec reconciliation: the
	// tracked key now fails os.Stat.
	os.Remove(path)
	defaultFileTracker.RecordVacated(snap)
	return path
}

func TestIssue5026FaceAWriteVacatedPathRefused(t *testing.T) {
	dir := t.TempDir()
	path := vacSetup5026(t, dir, "shared.md")

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": path, "content": "appended tail only"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("write to vacated tracked path must be refused (face A, #5026)")
	}
	if !strings.Contains(result.Content, "vacated by an external move") {
		t.Fatalf("refusal must carry the move hint, got: %s", result.Content)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("refused write must not create a truncated twin at the old path")
	}
}

func TestIssue5026AckRouteReadClearsThenWriteProceeds(t *testing.T) {
	dir := t.TempDir()
	path := vacSetup5026(t, dir, "shared.md")

	// Ack: read the vacated path -> not-found enriched with the move hint.
	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": path})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("read of a missing path must error")
	}
	if !strings.Contains(result.Content, "vacated by a move/rm during this session") {
		t.Fatalf("not-found must carry the move hint, got: %s", result.Content)
	}
	if defaultFileTracker.VacancyCheck(path) {
		t.Fatal("ack read must clear the vacancy marker")
	}

	// Subsequent intentional write proceeds as fresh creation, no refusal.
	w := WriteFile{}
	wInput, _ := json.Marshal(map[string]string{"path": path, "content": "fresh content"})
	wResult, err := w.Execute(context.Background(), wInput)
	if err != nil {
		t.Fatal(err)
	}
	if wResult.IsError {
		t.Fatalf("post-ack write must proceed as fresh creation, got: %s", wResult.Content)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "fresh content" {
		t.Fatalf("fresh content mismatch: %q", data)
	}
}

func TestIssue5026FaceBMovedTargetWarningStaysTrue(t *testing.T) {
	dir := t.TempDir()
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	old := filepath.Join(dir, "shared.md")
	moved := filepath.Join(dir, "moved.md")
	os.WriteFile(old, []byte("original content\n"), 0o644)
	defaultFileTracker.RecordRead(old)
	snap := defaultFileTracker.SnapshotTracked()
	os.Rename(old, moved)
	defaultFileTracker.RecordVacated(snap)

	// Writing the moved target was never read at that path: the blind-write
	// warning fires and is semantically TRUE (accepted residual, #5026 scope
	// point 6) -- one read of the target clears it.
	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": moved, "content": "original content\nappended tail"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("write to moved target must succeed, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "without having read it in this session") {
		t.Fatalf("face B blind-write warning must remain (now semantically true), got: %s", result.Content)
	}
}

func TestIssue5026RmVacatedAlsoRefused(t *testing.T) {
	dir := t.TempDir()
	path := vacSetup5026(t, dir, "doomed.md") // os.Remove == rm coverage, QA-2

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": path, "content": "recreate"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content, "vacated by an external move") {
		t.Fatalf("rm-vacated path must be refused too (delete-then-recreate), got: %+v", result)
	}
}

func TestIssue5026ExternallyRecreatedPathNotRefused(t *testing.T) {
	dir := t.TempDir()
	path := vacSetup5026(t, dir, "recreated.md")

	// Someone (another agent) recreates the path externally: stat succeeds,
	// the vacancy gate must not fire; normal blind rules apply.
	if err := os.WriteFile(path, []byte("recreated by other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if defaultFileTracker.VacancyCheck(path) {
		t.Fatal("recreated path must not be considered vacated (stat-gated)")
	}

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": path, "content": "overwritten"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError && strings.Contains(result.Content, "vacated by an external move") {
		t.Fatalf("recreated path must not hit the vacancy refusal, got: %s", result.Content)
	}
}

func TestIssue5026FileOpsMoveKeepsFrictionless(t *testing.T) {
	dir := t.TempDir()
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	src := filepath.Join(dir, "src.md")
	dst := filepath.Join(dir, "dst.md")
	os.WriteFile(src, []byte("tracked content\n"), 0o644)
	defaultFileTracker.RecordRead(src)

	// file_ops move migrates tracking itself: source tracking removed, the
	// destination recorded. No vacancy marker may appear on either path.
	os.Rename(src, dst)
	defaultFileTracker.RemoveTracking(src)
	defaultFileTracker.RecordWrite(dst)

	if defaultFileTracker.VacancyCheck(src) || defaultFileTracker.VacancyCheck(dst) {
		t.Fatal("file_ops move path must stay frictionless (no vacancy markers)")
	}
	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": dst, "content": "tracked content\nplus tail"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("write to file_ops move target must succeed frictionlessly, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "without having read it in this session") {
		t.Fatalf("file_ops target was tracked at write time; no blind warning expected, got: %s", result.Content)
	}
}
