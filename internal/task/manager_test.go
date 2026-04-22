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
