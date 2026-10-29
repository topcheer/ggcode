package task

// #4370 regression: RestoreJSON validated the decode-failure path (see #4363)
// but performed zero CONTENT validation on snapshots that decode fine:
//   1. duplicate task IDs - the map write silently dropped every earlier
//      duplicate and returned nil (fewer tasks than the snapshot holds);
//   2. invalid Status values (empty string, old spellings, values from
//      external tools) - passed through untouched, then counted as pending
//      by digestStats every round and as nothing by BoardStats (all-zero
//      stats with ok=true), and re-persisted by SnapshotJSON forever.
//
// The fix mirrors the write path's defense at the restore boundary: skip
// duplicates with a debug log (first occurrence wins, deterministic), and
// normalize invalid statuses to pending with a debug log (best-effort
// recovery keeps the task instead of rejecting the whole snapshot).

import (
	"encoding/json"
	"testing"
)

func issue4370Snapshot(t *testing.T, tasks []persistedTask) []byte {
	t.Helper()
	raw, err := json.Marshal(snapshotFormat{Version: snapshotVersion, NextID: 100, Tasks: tasks})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestIssue4370DuplicateIDSkippedNotSilentlyDropped pins that a duplicate ID
// keeps the FIRST occurrence deterministically, skips the later one, and
// still restores every distinct task (before the fix the map write silently
// overwrote task-1's content with the duplicate's, no error surfaced).
func TestIssue4370DuplicateIDSkippedNotSilentlyDropped(t *testing.T) {
	m := NewManager()
	data := issue4370Snapshot(t, []persistedTask{
		{ID: "task-1", Subject: "first", Status: StatusPending},
		{ID: "task-2", Subject: "second", Status: StatusPending},
		{ID: "task-1", Subject: "duplicate-later", Status: StatusCompleted},
	})
	if err := m.RestoreJSON(data); err != nil {
		t.Fatalf("RestoreJSON: %v", err)
	}
	got, ok := m.Get("task-1")
	if !ok {
		t.Fatal("task-1 missing after restore")
	}
	if got.Subject != "first" {
		t.Fatalf("duplicate overwrite: task-1 subject = %q, want %q (first occurrence must win)", got.Subject, "first")
	}
	if _, ok := m.Get("task-2"); !ok {
		t.Fatal("task-2 missing after restore - duplicate skip must not drop distinct tasks")
	}
}

// TestIssue4370InvalidStatusNormalizedToPending pins that a task with an
// invalid status is kept but normalized to pending instead of leaking the
// raw value into digestStats/BoardStats/SnapshotJSON round trips.
func TestIssue4370InvalidStatusNormalizedToPending(t *testing.T) {
	m := NewManager()
	data := issue4370Snapshot(t, []persistedTask{
		{ID: "task-1", Subject: "bad-status", Status: TaskStatus("in_progress_old_spelling")},
		{ID: "task-2", Subject: "empty-status", Status: ""},
		{ID: "task-3", Subject: "valid", Status: StatusCompleted},
	})
	if err := m.RestoreJSON(data); err != nil {
		t.Fatalf("RestoreJSON: %v", err)
	}
	for _, id := range []string{"task-1", "task-2"} {
		got, ok := m.Get(id)
		if !ok {
			t.Fatalf("%s missing after restore", id)
		}
		if got.Status != StatusPending {
			t.Fatalf("%s status = %q, want pending (invalid status must be normalized)", id, got.Status)
		}
	}
	// Valid statuses must pass through untouched.
	if got, _ := m.Get("task-3"); got.Status != StatusCompleted {
		t.Fatalf("task-3 status = %q, want completed (valid statuses must not be touched)", got.Status)
	}
	// The normalization must survive the round trip: a re-snapshot and
	// restore yields only valid statuses.
	out, err := m.SnapshotJSON()
	if err != nil {
		t.Fatal(err)
	}
	m2 := NewManager()
	if err := m2.RestoreJSON(out); err != nil {
		t.Fatal(err)
	}
	for _, tk := range m2.List() {
		if !IsValidStatus(string(tk.Status)) {
			t.Fatalf("round trip re-persisted invalid status %q for %s", tk.Status, tk.ID)
		}
	}
}

// TestIssue4370BoardStatsConsistentAfterNormalize pins the downstream fix:
// after normalization BoardStats counts the restored tasks instead of
// returning an all-zero ok=true illusion for invalid-status tasks.
func TestIssue4370BoardStatsConsistentAfterNormalize(t *testing.T) {
	m := NewManager()
	data := issue4370Snapshot(t, []persistedTask{
		{ID: "task-1", Subject: "was-invalid", Status: TaskStatus("weird")},
	})
	if err := m.RestoreJSON(data); err != nil {
		t.Fatalf("RestoreJSON: %v", err)
	}
	// BoardStats operates on the persisted snapshot bytes: the normalized
	// board must re-snapshot into counts that include the restored task
	// instead of the pre-fix all-zero ok=true illusion.
	out, err := m.SnapshotJSON()
	if err != nil {
		t.Fatal(err)
	}
	completed, inProgress, pending, ok := BoardStats(out)
	if !ok || completed != 0 || inProgress != 0 || pending != 1 {
		t.Fatalf("BoardStats = (completed=%d, inProgress=%d, pending=%d, ok=%v), want pending=1 (pre-fix: all zeros)", completed, inProgress, pending, ok)
	}
}
