package task

import (
	"testing"
	"time"
)

func TestTaskBoardSnapshotRestoreRoundTrip(t *testing.T) {
	m := NewManager()
	a := m.Create("first", "desc a", "doing a", map[string]string{"k": "v"})
	b := m.Create("second", "desc b", "doing b", nil)
	if _, err := m.Update(a.ID, UpdateOptions{Status: ptrStatus(StatusInProgress)}); err != nil {
		t.Fatalf("update a: %v", err)
	}
	if _, err := m.Update(b.ID, UpdateOptions{AddBlockedBy: []string{a.ID}}); err != nil {
		t.Fatalf("add dep: %v", err)
	}

	data, err := m.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON: %v", err)
	}

	// Restore into a FRESH manager (simulating a process restart / resume).
	m2 := NewManager()
	if err := m2.RestoreJSON(data); err != nil {
		t.Fatalf("RestoreJSON: %v", err)
	}
	got := m2.List()
	if len(got) != 2 {
		t.Fatalf("want 2 tasks after restore, got %d", len(got))
	}
	byID := map[string]Task{}
	for _, tk := range got {
		byID[tk.ID] = tk
	}
	ra, rb := byID[a.ID], byID[b.ID]
	if ra.Status != StatusInProgress || ra.Subject != "first" || ra.Metadata["k"] != "v" {
		t.Errorf("task a not restored faithfully: %+v", ra)
	}
	if len(rb.BlockedBy) != 1 || rb.BlockedBy[0] != a.ID {
		t.Errorf("dependency edge not restored: %+v", rb)
	}
	if len(ra.Blocks) != 1 || ra.Blocks[0] != b.ID {
		t.Errorf("reverse edge not restored: %+v", ra)
	}

	// nextID must continue where the snapshot left off, never restarting or
	// colliding with restored IDs (exact number is a Create() implementation
	// detail and must not leak into this test).
	c := m2.Create("third", "", "", nil)
	if c.ID == a.ID || c.ID == b.ID {
		t.Fatalf("restored manager reused an existing ID: %s", c.ID)
	}
	if numericTaskID(c.ID) <= numericTaskID(b.ID) {
		t.Errorf("new ID should advance past the highest restored ID, got %s (b=%s)", c.ID, b.ID)
	}
}

func TestTaskBoardRestoreEmptyResetsBoard(t *testing.T) {
	m := NewManager()
	m.Create("t", "", "", nil)
	if err := m.RestoreJSON(nil); err != nil {
		t.Fatalf("RestoreJSON(nil): %v", err)
	}
	if got := m.List(); len(got) != 0 {
		t.Errorf("want empty board after restoring empty snapshot, got %d tasks", len(got))
	}
	next := m.Create("fresh", "", "", nil)
	if next.ID != "task-1" {
		t.Errorf("counter should reset with the board, got %s", next.ID)
	}
}

func TestTaskBoardRestoreRejectsNewerVersionAndCorruptData(t *testing.T) {
	m := NewManager()
	keep := m.Create("live", "", "", nil)

	if err := m.RestoreJSON([]byte(`{"version":99,"next_id":1,"tasks":[]}`)); err == nil {
		t.Error("want error for future snapshot version")
	}
	if err := m.RestoreJSON([]byte(`not json`)); err == nil {
		t.Error("want error for corrupt snapshot")
	}
	// Failed restores must leave the live board untouched.
	if got := m.List(); len(got) != 1 || got[0].ID != keep.ID {
		t.Errorf("failed restore clobbered live board: %+v", got)
	}
}

func TestTaskBoardSnapshotIsDeterministicAndDecoupled(t *testing.T) {
	m := NewManager()
	m.Create("zeta", "", "", nil)
	m.Create("alpha", "", "", nil)
	d1, err := m.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON: %v", err)
	}
	d2, _ := m.SnapshotJSON()
	if string(d1) != string(d2) {
		t.Error("two snapshots of an unchanged board differ")
	}

	// Mutating the restored copy must not leak back into the live board.
	m2 := NewManager()
	_ = m2.RestoreJSON(d1)
	tasks := m2.List()
	tasks[0].Subject = "mutated"
	if got, _ := m2.Get(tasks[0].ID); got.Subject == "mutated" {
		t.Error("List() returned live references; mutation leaked into the manager")
	}
}

func TestNumericTaskID(t *testing.T) {
	cases := map[string]int{
		"task-1": 1, "task-42": 42, "": 0, "task-": 0, "other": 0, "task-x": 0,
	}
	for in, want := range cases {
		if got := numericTaskID(in); got != want {
			t.Errorf("numericTaskID(%q)=%d, want %d", in, got, want)
		}
	}
}

// ptrStatus is a small test helper mirroring the tool layer's option style.
func ptrStatus(s TaskStatus) *TaskStatus { return &s }

var _ = time.Now // keep time imported if unused by future edits

func TestBoardStats(t *testing.T) {
	m := NewManager()
	a := m.Create("done task", "", "", nil)
	b := m.Create("active task", "", "", nil)
	c := m.Create("queued task", "", "", nil)
	if _, err := m.Update(a.ID, UpdateOptions{Status: ptrStatus(StatusCompleted)}); err != nil {
		t.Fatalf("complete a: %v", err)
	}
	if _, err := m.Update(b.ID, UpdateOptions{Status: ptrStatus(StatusInProgress)}); err != nil {
		t.Fatalf("start b: %v", err)
	}
	_ = c // stays pending

	data, err := m.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON: %v", err)
	}
	completed, inProgress, pending, ok := BoardStats(data)
	if !ok {
		t.Fatal("BoardStats should decode a valid snapshot")
	}
	if completed != 1 || inProgress != 1 || pending != 1 {
		t.Errorf("got completed=%d inProgress=%d pending=%d, want 1/1/1", completed, inProgress, pending)
	}

	if _, _, _, ok := BoardStats(nil); ok {
		t.Error("empty snapshot should not be ok")
	}
	if _, _, _, ok := BoardStats([]byte("{corrupt")); ok {
		t.Error("corrupt snapshot should not be ok")
	}
}
