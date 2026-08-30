package task

import (
	"testing"
)

func TestCreateAndGet(t *testing.T) {
	m := NewManager()
	created := m.Create("Fix auth bug", "Login fails on Safari", "Fixing auth bug", nil)

	if created.ID != "task-1" {
		t.Errorf("expected task-1, got %s", created.ID)
	}
	if created.Subject != "Fix auth bug" {
		t.Errorf("unexpected subject: %s", created.Subject)
	}
	if created.Status != StatusPending {
		t.Errorf("expected pending, got %s", created.Status)
	}

	got, ok := m.Get("task-1")
	if !ok {
		t.Fatal("task not found")
	}
	if got.Subject != created.Subject {
		t.Errorf("subject mismatch: %s vs %s", got.Subject, created.Subject)
	}

	_, ok = m.Get("task-999")
	if ok {
		t.Error("expected not found for nonexistent task")
	}
}

func TestList(t *testing.T) {
	m := NewManager()
	m.Create("A", "", "", nil)
	m.Create("B", "", "", nil)

	list := m.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(list))
	}
}

func TestUpdate(t *testing.T) {
	m := NewManager()
	created := m.Create("Write tests", "", "", nil)

	inProgress := StatusInProgress
	updated, err := m.Update(created.ID, UpdateOptions{Status: &inProgress})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusInProgress {
		t.Errorf("expected in_progress, got %s", updated.Status)
	}

	completed := StatusCompleted
	updated, err = m.Update(created.ID, UpdateOptions{Status: &completed})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", updated.Status)
	}
}

func TestUpdateSubject(t *testing.T) {
	m := NewManager()
	created := m.Create("Old subject", "", "", nil)

	newSubj := "New subject"
	updated, err := m.Update(created.ID, UpdateOptions{Subject: &newSubj})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Subject != "New subject" {
		t.Errorf("expected 'New subject', got %s", updated.Subject)
	}
}

func TestBlockDependencies(t *testing.T) {
	m := NewManager()
	a := m.Create("Task A", "", "", nil)
	b := m.Create("Task B", "", "", nil)

	_, err := m.Update(a.ID, UpdateOptions{AddBlocks: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}

	// Verify A blocks B
	gotA, _ := m.Get(a.ID)
	if len(gotA.Blocks) != 1 || gotA.Blocks[0] != b.ID {
		t.Errorf("expected A to block B, got blocks=%v", gotA.Blocks)
	}

	// Verify B is blocked by A (reverse link)
	gotB, _ := m.Get(b.ID)
	if len(gotB.BlockedBy) != 1 || gotB.BlockedBy[0] != a.ID {
		t.Errorf("expected B blocked by A, got blockedBy=%v", gotB.BlockedBy)
	}
}

func TestUpdateMetadata(t *testing.T) {
	m := NewManager()
	created := m.Create("Task", "", "", map[string]string{"priority": "high"})

	meta := map[string]string{"assignee": "alice"}
	updated, err := m.Update(created.ID, UpdateOptions{Metadata: meta})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Metadata["priority"] != "high" {
		t.Error("existing metadata lost")
	}
	if updated.Metadata["assignee"] != "alice" {
		t.Error("new metadata not merged")
	}
}

func TestDelete(t *testing.T) {
	m := NewManager()
	m.Create("To delete", "", "", nil)

	if !m.Delete("task-1") {
		t.Error("expected delete to succeed")
	}
	if m.Delete("task-1") {
		t.Error("expected second delete to fail")
	}
}

func TestUpdateNonexistent(t *testing.T) {
	m := NewManager()
	_, err := m.Update("task-999", UpdateOptions{})
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestBlockNonexistent(t *testing.T) {
	m := NewManager()
	m.Create("A", "", "", nil)
	_, err := m.Update("task-1", UpdateOptions{AddBlocks: []string{"task-999"}})
	if err == nil {
		t.Error("expected error for nonexistent blocked task")
	}
}

// #1297: all three wouldCreateCycle call sites had swapped args, so a
// real 3-node cycle passed validation and persisted - idle_runner's
// single-level check then deadlocked every task in the cycle forever.
func TestUpdateCycleDetectionNotSwapped(t *testing.T) {
	// Repro 1 (AddBlockedBy path): A blocks B, B blocks C, then
	// A blocked-by C closes the cycle A→B→C→A - must be rejected.
	m := NewManager()
	a := m.Create("A", "", "", nil).ID
	b := m.Create("B", "", "", nil).ID
	c := m.Create("C", "", "", nil).ID
	if _, err := m.Update(b, UpdateOptions{AddBlockedBy: []string{a}}); err != nil {
		t.Fatalf("A blocks B: %v", err)
	}
	if _, err := m.Update(c, UpdateOptions{AddBlockedBy: []string{b}}); err != nil {
		t.Fatalf("B blocks C: %v", err)
	}
	if _, err := m.Update(a, UpdateOptions{AddBlockedBy: []string{c}}); err == nil {
		t.Fatal("cycle A→B→C→A via AddBlockedBy must be rejected (swapped-args bug)")
	}

	// Repro 2 (AddBlocks path): same cycle built with AddBlocks.
	m2 := NewManager()
	a2 := m2.Create("A", "", "", nil).ID
	b2 := m2.Create("B", "", "", nil).ID
	c2 := m2.Create("C", "", "", nil).ID
	if _, err := m2.Update(a2, UpdateOptions{AddBlocks: []string{b2}}); err != nil {
		t.Fatalf("A blocks B: %v", err)
	}
	if _, err := m2.Update(b2, UpdateOptions{AddBlocks: []string{c2}}); err != nil {
		t.Fatalf("B blocks C: %v", err)
	}
	if _, err := m2.Update(c2, UpdateOptions{AddBlocks: []string{a2}}); err == nil {
		t.Fatal("cycle via AddBlocks must be rejected (swapped-args bug)")
	}

	// Sanity: legitimate edges still accepted.
	m3 := NewManager()
	x := m3.Create("X", "", "", nil).ID
	y := m3.Create("Y", "", "", nil).ID
	if _, err := m3.Update(y, UpdateOptions{AddBlockedBy: []string{x}}); err != nil {
		t.Fatalf("legit edge rejected: %v", err)
	}
}

// #1345: an Update whose addBlocks+addBlockedBy combination forms a
// cross-array cycle must fail BEFORE any mutation - the old code wrote
// the AddBlocks edges (and Status) first and only detected the cycle in
// the AddBlockedBy apply loop, leaving partial state behind.
func TestUpdateCrossArrayCycleNotModified(t *testing.T) {
	m := NewManager()
	m.Create("X", "", "", nil) // task-1
	m.Create("B", "", "", nil) // task-2

	inProgress := TaskStatus(StatusInProgress)
	_, err := m.Update("task-1", UpdateOptions{
		Status:       &inProgress,
		AddBlocks:    []string{"task-2"},
		AddBlockedBy: []string{"task-2"},
	})
	if err == nil {
		t.Fatal("expected cross-array cycle rejection")
	}

	x, _ := m.Get("task-1")
	if len(x.Blocks) != 0 {
		t.Errorf("task-1 must be unmodified on error, got blocks=%v", x.Blocks)
	}
	if x.Status != StatusPending {
		t.Errorf("task-1 status must stay pending on error, got %s", x.Status)
	}
	b, _ := m.Get("task-2")
	if len(b.BlockedBy) != 0 {
		t.Errorf("task-2 must be unmodified on error, got blockedBy=%v", b.BlockedBy)
	}
}
