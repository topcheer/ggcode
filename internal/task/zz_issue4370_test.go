package task

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

	if got, _ := m.Get("task-3"); got.Status != StatusCompleted {
		t.Fatalf("task-3 status = %q, want completed (valid statuses must not be touched)", got.Status)
	}

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

func TestIssue4370BoardStatsConsistentAfterNormalize(t *testing.T) {
	m := NewManager()
	data := issue4370Snapshot(t, []persistedTask{
		{ID: "task-1", Subject: "was-invalid", Status: TaskStatus("weird")},
	})
	if err := m.RestoreJSON(data); err != nil {
		t.Fatalf("RestoreJSON: %v", err)
	}

	out, err := m.SnapshotJSON()
	if err != nil {
		t.Fatal(err)
	}
	completed, inProgress, pending, ok := BoardStats(out)
	if !ok || completed != 0 || inProgress != 0 || pending != 1 {
		t.Fatalf("BoardStats = (completed=%d, inProgress=%d, pending=%d, ok=%v), want pending=1 (pre-fix: all zeros)", completed, inProgress, pending, ok)
	}
}
