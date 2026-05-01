package harness

import (
	"context"
	"testing"
)

// TestPromoteTask_OnlyTargetsSpecificTask verifies that PromoteTask promotes
// ONLY the specified task, not all approved tasks. This is a regression test
// for the batch-promote bug where the CTA Enter handler used
// PromoteApprovedTasks instead of PromoteTask.
func TestPromoteTask_OnlyTargetsSpecificTask(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	project := result.Project

	// Create 3 approved, completed, verified tasks
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		task := &Task{
			ID:                 id,
			Goal:               "test goal for " + id,
			Status:             TaskCompleted,
			VerificationStatus: VerificationPassed,
			ReviewStatus:       ReviewApproved,
		}
		if err := SaveTask(project, task); err != nil {
			t.Fatalf("SaveTask %s error = %v", id, err)
		}
	}

	// Promote only task-2
	promoted, err := PromoteTask(context.Background(), project, "task-2", "test note")
	if err != nil {
		t.Fatalf("PromoteTask() error = %v", err)
	}
	if promoted == nil {
		t.Fatal("PromoteTask() returned nil task")
	}
	if promoted.ID != "task-2" {
		t.Errorf("promoted task ID = %q, want %q", promoted.ID, "task-2")
	}
	if promoted.PromotionStatus != PromotionApplied {
		t.Errorf("promoted task promotion = %q, want %q", promoted.PromotionStatus, PromotionApplied)
	}

	// Verify task-1 is still NOT promoted
	task1, err := LoadTask(project, "task-1")
	if err != nil {
		t.Fatalf("LoadTask task-1 error = %v", err)
	}
	if task1.PromotionStatus == PromotionApplied {
		t.Error("task-1 should NOT be promoted — only task-2 was targeted")
	}

	// Verify task-3 is still NOT promoted
	task3, err := LoadTask(project, "task-3")
	if err != nil {
		t.Fatalf("LoadTask task-3 error = %v", err)
	}
	if task3.PromotionStatus == PromotionApplied {
		t.Error("task-3 should NOT be promoted — only task-2 was targeted")
	}
}
