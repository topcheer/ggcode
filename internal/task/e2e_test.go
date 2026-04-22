package task

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// E2E: task full lifecycle — create → update → stop → complete
// ---------------------------------------------------------------------------

func TestE2E_TaskFullLifecycle(t *testing.T) {
	m := NewManager()

	// Create.
	tk := m.Create("Design API", "Create API specification", "Designing API", map[string]string{"type": "design"})
	if tk.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if tk.Status != StatusPending {
		t.Fatalf("expected pending, got %s", tk.Status)
	}

	// Update to in_progress.
	inProg := StatusInProgress
	tk, err := m.Update(tk.ID, UpdateOptions{Status: &inProg, Owner: ptrStr("alice")})
	if err != nil {
		t.Fatalf("update to in_progress: %v", err)
	}
	if tk.Status != StatusInProgress || tk.Owner != "alice" {
		t.Fatalf("expected in_progress/alice, got %s/%s", tk.Status, tk.Owner)
	}

	// Stop (reset back to pending).
	pending := StatusPending
	tk, err = m.Update(tk.ID, UpdateOptions{Status: &pending})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if tk.Status != StatusPending {
		t.Fatalf("expected pending after stop, got %s", tk.Status)
	}

	// Re-claim and complete.
	tk, err = m.Update(tk.ID, UpdateOptions{Status: &inProg, Owner: ptrStr("bob")})
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	completed := StatusCompleted
	tk, err = m.Update(tk.ID, UpdateOptions{Status: &completed})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if tk.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s", tk.Status)
	}

	// Verify description and subject survive the lifecycle.
	got, ok := m.Get(tk.ID)
	if !ok {
		t.Fatal("task should still exist")
	}
	if got.Subject != "Design API" || got.Description != "Create API specification" {
		t.Errorf("subject/description lost: %q / %q", got.Subject, got.Description)
	}
}

// ---------------------------------------------------------------------------
// E2E: task dependency (blockedBy) chain resolution
// ---------------------------------------------------------------------------

func TestE2E_DependencyChain(t *testing.T) {
	m := NewManager()

	// Create A → B → C dependency chain.
	taskA := m.Create("Task A", "First task", "", nil)
	taskB := m.Create("Task B", "Second task", "", nil)
	taskC := m.Create("Task C", "Third task", "", nil)

	// A blocks B.
	_, err := m.Update(taskB.ID, UpdateOptions{AddBlockedBy: []string{taskA.ID}})
	if err != nil {
		t.Fatalf("B blocked by A: %v", err)
	}

	// B blocks C.
	_, err = m.Update(taskC.ID, UpdateOptions{AddBlockedBy: []string{taskB.ID}})
	if err != nil {
		t.Fatalf("C blocked by B: %v", err)
	}

	// Verify forward links too.
	gotA, _ := m.Get(taskA.ID)
	if len(gotA.Blocks) != 1 || gotA.Blocks[0] != taskB.ID {
		t.Errorf("expected A to block B, got %v", gotA.Blocks)
	}
	gotB, _ := m.Get(taskB.ID)
	if len(gotB.Blocks) != 1 || gotB.Blocks[0] != taskC.ID {
		t.Errorf("expected B to block C, got %v", gotB.Blocks)
	}

	// Resolve: complete A.
	completed := StatusCompleted
	_, err = m.Update(taskA.ID, UpdateOptions{Status: &completed})
	if err != nil {
		t.Fatalf("complete A: %v", err)
	}
	// B is still blockedBy A (the link stays), but A is completed — app logic
	// would check if all blockers are completed. Verify the link still exists.
	gotB, _ = m.Get(taskB.ID)
	if len(gotB.BlockedBy) != 1 || gotB.BlockedBy[0] != taskA.ID {
		t.Error("blockedBy link should persist after completion")
	}
}

// ---------------------------------------------------------------------------
// E2E: diamond dependency (A blocks B and C; B and C block D)
// ---------------------------------------------------------------------------

func TestE2E_DiamondDependency(t *testing.T) {
	m := NewManager()

	A := m.Create("A", "", "", nil)
	B := m.Create("B", "", "", nil)
	C := m.Create("C", "", "", nil)
	D := m.Create("D", "", "", nil)

	// A blocks B and C.
	_, err := m.Update(B.ID, UpdateOptions{AddBlockedBy: []string{A.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Update(C.ID, UpdateOptions{AddBlockedBy: []string{A.ID}})
	if err != nil {
		t.Fatal(err)
	}

	// B and C block D.
	_, err = m.Update(D.ID, UpdateOptions{AddBlockedBy: []string{B.ID, C.ID}})
	if err != nil {
		t.Fatal(err)
	}

	// Verify D has two blockers.
	gotD, _ := m.Get(D.ID)
	if len(gotD.BlockedBy) != 2 {
		t.Errorf("expected 2 blockers for D, got %d", len(gotD.BlockedBy))
	}

	// Verify A blocks B and C.
	gotA, _ := m.Get(A.ID)
	if len(gotA.Blocks) != 2 {
		t.Errorf("expected A to block 2 tasks, got %d", len(gotA.Blocks))
	}
}

// ---------------------------------------------------------------------------
// E2E: concurrent task operations
// ---------------------------------------------------------------------------

func TestE2E_ConcurrentTaskOperations(t *testing.T) {
	m := NewManager()

	const numTasks = 100
	var wg sync.WaitGroup
	ids := make(chan string, numTasks)

	// Concurrent creates.
	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tk := m.Create(fmt.Sprintf("task-%d", idx), "", "", nil)
			ids <- tk.ID
		}(i)
	}
	wg.Wait()
	close(ids)

	taskIDs := make(map[string]bool)
	for id := range ids {
		if taskIDs[id] {
			t.Errorf("duplicate task ID: %s", id)
		}
		taskIDs[id] = true
	}

	if len(taskIDs) != numTasks {
		t.Errorf("expected %d unique tasks, got %d", numTasks, len(taskIDs))
	}

	// Concurrent updates — each task gets claimed.
	inProg := StatusInProgress
	var updateWg sync.WaitGroup
	for id := range taskIDs {
		updateWg.Add(1)
		go func(tid string) {
			defer updateWg.Done()
			_, err := m.Update(tid, UpdateOptions{
				Status: &inProg,
				Owner:  ptrStr("worker-" + tid),
			})
			if err != nil {
				t.Errorf("update %s: %v", tid, err)
			}
		}(id)
	}
	updateWg.Wait()

	// All tasks should be in_progress.
	list := m.List()
	for _, tk := range list {
		if tk.Status != StatusInProgress {
			t.Errorf("task %s: expected in_progress, got %s", tk.ID, tk.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: concurrent claim race on a single task
// ---------------------------------------------------------------------------

func TestE2E_ConcurrentClaimRace(t *testing.T) {
	m := NewManager()
	tk := m.Create("Hot task", "Everyone wants this", "", nil)

	pending := StatusPending
	inProg := StatusInProgress

	var winner atomic.Value
	var losers atomic.Int32

	const numClaimants = 10
	var wg sync.WaitGroup

	for i := 0; i < numClaimants; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			owner := fmt.Sprintf("claimant-%d", idx)
			_, err := m.Update(tk.ID, UpdateOptions{
				ExpectedStatus: &pending,
				Status:         &inProg,
				Owner:          &owner,
			})
			if err != nil {
				losers.Add(1)
			} else {
				winner.Store(owner)
			}
		}(i)
	}
	wg.Wait()

	w := winner.Load()
	if w == nil {
		t.Fatal("expected exactly one winner")
	}
	if int(losers.Load()) != numClaimants-1 {
		t.Errorf("expected %d losers, got %d", numClaimants-1, losers.Load())
	}

	got, _ := m.Get(tk.ID)
	if got.Owner != w.(string) {
		t.Errorf("owner mismatch: expected %s, got %s", w, got.Owner)
	}
}

// ---------------------------------------------------------------------------
// E2E: task metadata operations
// ---------------------------------------------------------------------------

func TestE2E_MetadataOperations(t *testing.T) {
	m := NewManager()

	tk := m.Create("Meta task", "", "", map[string]string{
		"priority": "high",
		"team":     "backend",
	})

	// Merge additional metadata.
	_, err := m.Update(tk.ID, UpdateOptions{Metadata: map[string]string{
		"assignee": "alice",
		"priority": "critical", // overwrite
	}})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := m.Get(tk.ID)
	if got.Metadata["priority"] != "critical" {
		t.Errorf("expected priority=critical, got %s", got.Metadata["priority"])
	}
	if got.Metadata["team"] != "backend" {
		t.Errorf("expected team=backend, got %s", got.Metadata["team"])
	}
	if got.Metadata["assignee"] != "alice" {
		t.Errorf("expected assignee=alice, got %s", got.Metadata["assignee"])
	}

	// Nil metadata on update should not clear existing.
	_, err = m.Update(tk.ID, UpdateOptions{Metadata: nil})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = m.Get(tk.ID)
	if len(got.Metadata) != 3 {
		t.Errorf("expected 3 metadata entries, got %d", len(got.Metadata))
	}
}

// ---------------------------------------------------------------------------
// E2E: task delete with dependency cleanup
// ---------------------------------------------------------------------------

func TestE2E_DeleteCleansDependencies(t *testing.T) {
	m := NewManager()

	A := m.Create("A", "", "", nil)
	B := m.Create("B", "", "", nil)
	C := m.Create("C", "", "", nil)

	// A blocks B, B blocks C.
	m.Update(B.ID, UpdateOptions{AddBlockedBy: []string{A.ID}})
	m.Update(C.ID, UpdateOptions{AddBlockedBy: []string{B.ID}})

	// Delete B — should remove B from A.Blocks and C.BlockedBy.
	if !m.Delete(B.ID) {
		t.Fatal("delete B failed")
	}

	gotA, _ := m.Get(A.ID)
	if len(gotA.Blocks) != 0 {
		t.Errorf("expected A.Blocks empty after B deleted, got %v", gotA.Blocks)
	}
	gotC, _ := m.Get(C.ID)
	if len(gotC.BlockedBy) != 0 {
		t.Errorf("expected C.BlockedBy empty after B deleted, got %v", gotC.BlockedBy)
	}

	// B should be gone.
	_, ok := m.Get(B.ID)
	if ok {
		t.Error("B should be deleted")
	}
}

// ---------------------------------------------------------------------------
// E2E: ActiveForm field
// ---------------------------------------------------------------------------

func TestE2E_ActiveForm(t *testing.T) {
	m := NewManager()

	tk := m.Create("Build feature", "Full description", "Building feature", nil)
	if tk.ActiveForm != "Building feature" {
		t.Errorf("expected 'Building feature', got %q", tk.ActiveForm)
	}

	newForm := "Compiling code"
	updated, err := m.Update(tk.ID, UpdateOptions{ActiveForm: &newForm})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ActiveForm != "Compiling code" {
		t.Errorf("expected 'Compiling code', got %q", updated.ActiveForm)
	}
}

// ---------------------------------------------------------------------------
// E2E: ExpectedStatus conditional update
// ---------------------------------------------------------------------------

func TestE2E_ConditionalUpdate(t *testing.T) {
	m := NewManager()
	tk := m.Create("Conditional", "", "", nil)

	pending := StatusPending
	inProg := StatusInProgress

	// Success: current is pending, expected is pending.
	_, err := m.Update(tk.ID, UpdateOptions{ExpectedStatus: &pending, Status: &inProg})
	if err != nil {
		t.Fatalf("first conditional update should succeed: %v", err)
	}

	// Failure: current is in_progress, expected is pending.
	_, err = m.Update(tk.ID, UpdateOptions{ExpectedStatus: &pending, Status: &inProg})
	if err == nil {
		t.Error("expected error when status doesn't match ExpectedStatus")
	}
}

// ---------------------------------------------------------------------------
// E2E: task timestamps
// ---------------------------------------------------------------------------

func TestE2E_Timestamps(t *testing.T) {
	m := NewManager()

	beforeCreate := time.Now()
	tk := m.Create("Timestamps", "", "", nil)
	afterCreate := time.Now()

	if tk.CreatedAt.Before(beforeCreate) || tk.CreatedAt.After(afterCreate) {
		t.Errorf("CreatedAt out of range: %v", tk.CreatedAt)
	}
	if tk.UpdatedAt.Before(beforeCreate) || tk.UpdatedAt.After(afterCreate) {
		t.Errorf("UpdatedAt out of range: %v", tk.UpdatedAt)
	}

	time.Sleep(10 * time.Millisecond)

	inProg := StatusInProgress
	updated, err := m.Update(tk.ID, UpdateOptions{Status: &inProg})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.UpdatedAt.After(tk.CreatedAt) {
		t.Error("UpdatedAt should advance after update")
	}
}

// ---------------------------------------------------------------------------
// E2E: snapshot isolation — mutations to returned snapshot don't affect manager
// ---------------------------------------------------------------------------

func TestE2E_SnapshotIsolation(t *testing.T) {
	m := NewManager()
	tk := m.Create("Isolated", "", "", map[string]string{"key": "val"})

	snap, _ := m.Get(tk.ID)
	snap.Subject = "MUTATED"
	snap.Metadata["key"] = "OVERWRITTEN"

	original, _ := m.Get(tk.ID)
	if original.Subject == "MUTATED" {
		t.Error("snapshot mutation leaked into manager")
	}
	if original.Metadata["key"] == "OVERWRITTEN" {
		t.Error("metadata mutation leaked into manager")
	}
}

// ---------------------------------------------------------------------------
// helper
// ---------------------------------------------------------------------------

func ptrStr(s string) *string { return &s }
