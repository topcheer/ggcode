package checkpoint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file closes the remaining coverage gaps of the checkpoint package that
// survive the issue-specific zz_* tests. It exercises the durable-execution /
// checkpoint-restore safety contract with real file IO in t.TempDir() only --
// no seam swapping, no internal mocking (sa-129 research round, anchored on
// 2025-2026 durable-execution literature: LangGraph checkpointer semantics,
// Hermes-Agent shadow-store rollback, replay-safety contracts).

// TestLastEmptyReturnsNil covers Manager.Last on an empty manager.
func TestLastEmptyReturnsNil(t *testing.T) {
	m := NewManager(10)
	if cp := m.Last(); cp != nil {
		t.Fatalf("expected nil Last on empty manager, got %+v", cp)
	}
}

// TestLastReturnsMostRecentCopy covers Last's tail lookup and its copy
// semantics: callers must never be able to alias internal checkpoint state.
func TestLastReturnsMostRecentCopy(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fp := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(fp, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(10)
	m.Save(fp, "v1", "v2", "edit_file")
	m.Save(fp, "v2", "v3", "edit_file")

	last := m.Last()
	if last == nil {
		t.Fatal("expected non-nil Last after saves")
	}
	if last.OldContent != "v2" || last.NewContent != "v3" {
		t.Fatalf("expected tail checkpoint v2->v3, got %q->%q", last.OldContent, last.NewContent)
	}

	// Copy semantics: mutating the returned value must not leak into state.
	last.NewContent = "tampered"
	if again := m.Last(); again.NewContent != "v3" {
		t.Fatalf("Last returned aliased internal state: %q", again.NewContent)
	}

	// After Undo the tail is the previous checkpoint.
	if _, err := m.Undo("user"); err != nil {
		t.Fatalf("undo: %v", err)
	}
	last = m.Last()
	if last == nil || last.NewContent != "v2" {
		t.Fatalf("expected tail v1->v2 after undo, got %+v", last)
	}
}

// TestFirstExistedDefaultsTrueForUnknownPaths covers firstExisted's
// conservative default (#1539 case D): paths never recorded must be reported
// as pre-existing, never as new.
func TestFirstExistedDefaultsTrueForUnknownPaths(t *testing.T) {
	m := NewManager(10)
	if !m.firstExisted(filepath.Join(t.TempDir(), "never-saved.txt")) {
		t.Fatal("unknown paths must default to existed=true")
	}

	// A recorded existed=false wins over the default.
	created := filepath.Join(t.TempDir(), "created.txt")
	m.SaveWithExistence(created, "", "new", "write_file", false)
	if m.firstExisted(created) {
		t.Fatal("recorded existed=false must be reported")
	}
	if !m.firstExisted(filepath.Join(t.TempDir(), "other.txt")) {
		t.Fatal("only the exact recorded path may report existed=false")
	}
}

// TestRestoreCheckpointStateRemoveFailure covers the os.Remove error path
// (non-ErrNotExist failures must surface, while an already-gone file keeps
// undo idempotent).
func TestRestoreCheckpointStateRemoveFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(filepath.Join(blocked, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	// existed=false + empty old content asks for removal, but os.Remove
	// cannot delete a non-empty directory -- the error must propagate.
	err := restoreCheckpointState(blocked, "", false)
	if err == nil {
		t.Fatal("expected removal error for a non-empty directory")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removal failure misclassified as not-exist: %v", err)
	}

	// Idempotency: restoring "file absent" when it is already absent is a no-op.
	if err := restoreCheckpointState(filepath.Join(dir, "gone.txt"), "", false); err != nil {
		t.Fatalf("removing a missing file must be a no-op, got %v", err)
	}
}

// TestRedoWriteFailureKeepsRedoStack covers Redo's AtomicWriteFile failure
// path and its recovery contract: a failed redo must neither lose the redo
// stack nor resurrect the checkpoint, so the user can repair and retry.
func TestRedoWriteFailureKeepsRedoStack(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "workspace")
	fp := filepath.Join(sub, "f.txt")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fp, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(10)
	m.Save(fp, "old", "new", "edit_file")
	if _, err := m.Undo("user"); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !m.CanRedo() {
		t.Fatal("expected redo availability after undo")
	}

	// Make the write target impossible: parent directory removed.
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	cp, err := m.Redo()
	if err == nil {
		t.Fatal("expected redo to fail when the parent directory is gone")
	}
	if cp != nil {
		t.Fatalf("failed redo must not return a checkpoint, got %+v", cp)
	}
	if !strings.Contains(err.Error(), "failed to write file") {
		t.Fatalf("expected wrapped write error, got %v", err)
	}
	if !m.CanRedo() {
		t.Fatal("failed redo must keep the redo stack intact")
	}
	if got := len(m.List()); got != 0 {
		t.Fatalf("failed redo must not resurrect the checkpoint, list has %d", got)
	}

	// Recovery: recreate the directory, retry succeeds and re-applies new content.
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	rcp, err := m.Redo()
	if err != nil {
		t.Fatalf("redo after repair: %v", err)
	}
	if rcp.NewContent != "new" {
		t.Fatalf("redo applied wrong content %q", rcp.NewContent)
	}
	data, rerr := os.ReadFile(fp)
	if rerr != nil || string(data) != "new" {
		t.Fatalf("disk content after redo: %q err=%v", data, rerr)
	}
}

// TestNewManagerDefaultsLimit covers NewManager's default for non-positive
// limits: eviction must still bound history at the implicit cap of 50.
func TestNewManagerDefaultsLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		m := NewManager(limit)
		fp := filepath.Join(t.TempDir(), "f.txt")
		for i := 0; i < 51; i++ {
			m.Save(fp, "a", "b", "edit_file")
		}
		if got := len(m.List()); got != 50 {
			t.Fatalf("NewManager(%d): expected default cap 50, list has %d", limit, got)
		}
	}
}

// TestSnapshotRoundTripRecoversCorruptedFile exercises the snapshot
// round-trip over real IO: save -> external corruption -> Undo restores the
// pristine pre-edit bytes -> Redo re-applies the edit on top.
func TestSnapshotRoundTripRecoversCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fp := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(fp, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(10)
	m.Save(fp, "original", "mutated", "edit_file")

	// Simulate an external writer / partial crash corrupting the file.
	if err := os.WriteFile(fp, []byte("CORRUPTED"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Undo("user"); err != nil {
		t.Fatalf("undo: %v", err)
	}
	data, err := os.ReadFile(fp)
	if err != nil || string(data) != "original" {
		t.Fatalf("undo must restore pristine pre-edit bytes, got %q err=%v", data, err)
	}

	if _, err := m.Redo(); err != nil {
		t.Fatalf("redo: %v", err)
	}
	data, err = os.ReadFile(fp)
	if err != nil || string(data) != "mutated" {
		t.Fatalf("redo must re-apply the edit, got %q err=%v", data, err)
	}
}

// TestSessionIsolationAcrossManagers verifies multi-session isolation:
// separate Managers (one per session/workspace) undo independently, never
// touching each other's files or corrections.
func TestSessionIsolationAcrossManagers(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	fa := filepath.Join(dirA, "a.txt")
	fb := filepath.Join(dirB, "b.txt")
	if err := os.WriteFile(fa, []byte("a1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fb, []byte("b1"), 0o644); err != nil {
		t.Fatal(err)
	}

	mA, mB := NewManager(10), NewManager(10)
	mA.Save(fa, "a1", "a2", "edit_file")
	mB.Save(fb, "b1", "b2", "edit_file")
	// Save only records the checkpoint; the tool applies the edit itself.
	if err := os.WriteFile(fa, []byte("a2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fb, []byte("b2"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mA.Undo("user"); err != nil {
		t.Fatalf("undo A: %v", err)
	}
	data, err := os.ReadFile(fa)
	if err != nil || string(data) != "a1" {
		t.Fatalf("session A undo restored %q err=%v", data, err)
	}
	data, err = os.ReadFile(fb)
	if err != nil || string(data) != "b2" {
		t.Fatalf("session B must be untouched by A's undo, got %q err=%v", data, err)
	}

	// Corrections are per-session: B recorded nothing, A recorded the revert.
	if cs := mB.RecentCorrections(); cs != nil {
		t.Fatalf("session B recorded %d corrections it never made", len(cs))
	}
	cs := mA.RecentCorrections()
	if len(cs) != 1 || cs[0].Source != "user" || len(cs[0].Files) != 1 || cs[0].Files[0] != fa {
		t.Fatalf("session A corrections mismatch: %+v", cs)
	}
}
