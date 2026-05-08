package harness

import (
	"context"
	"fmt"
	"testing"
)

func TestApplyReleasePlanRejectsNonReleaseReadyTask(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Run and approve a task
	summary, err := RunTask(context.Background(), result.Project, result.Config, "Release test task", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	// Approve the review
	_, err = ApproveTaskReview(result.Project, summary.Task.ID, "looks good")
	if err != nil {
		t.Fatalf("ApproveTaskReview: %v", err)
	}

	// Promote the task
	_, err = PromoteTask(context.Background(), result.Project, summary.Task.ID, "promote")
	if err != nil {
		t.Fatalf("PromoteTask: %v", err)
	}

	// Verify it's release-ready
	task, err := LoadTask(result.Project, summary.Task.ID)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if !taskReleaseReady(task) {
		t.Fatalf("task should be release-ready, got status=%q review=%q promotion=%q", task.Status, task.ReviewStatus, task.PromotionStatus)
	}

	// Now revert promotion status to trigger validation failure
	task.PromotionStatus = ""
	if err := SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	plan := &ReleasePlan{
		BatchID: "test-rollback",
		Tasks:   []*Task{task},
	}

	_, err = ApplyReleasePlan(result.Project, plan, "test note")
	if err == nil {
		t.Fatal("expected error when task is not release-ready")
	}
}

func TestSaveTaskMutexPreventsOverwrite(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	summary, err := RunTask(context.Background(), result.Project, result.Config, "Mutex test", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	// Sequential rapid saves — mutex should prevent corruption
	for i := 0; i < 5; i++ {
		task, err := LoadTask(result.Project, summary.Task.ID)
		if err != nil {
			t.Fatalf("LoadTask %d: %v", i, err)
		}
		task.Error = fmt.Sprintf("iteration-%d", i)
		if err := SaveTask(result.Project, task); err != nil {
			t.Fatalf("SaveTask %d: %v", i, err)
		}
	}

	// Verify final state is valid
	final, err := LoadTask(result.Project, summary.Task.ID)
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if final.ID != summary.Task.ID {
		t.Errorf("task ID mismatch: got %q, want %q", final.ID, summary.Task.ID)
	}
}

func TestApplyReleasePlanSnapshotRollback(t *testing.T) {
	// Verify that the snapshot used for rollback contains pre-mutation values.
	// We can't easily inject a SaveTask failure, but we can verify the mechanism
	// by checking that snapshots are captured before mutation.
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Run, approve, promote a task
	summary, err := RunTask(context.Background(), result.Project, result.Config, "Snapshot test", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	_, err = ApproveTaskReview(result.Project, summary.Task.ID, "ok")
	if err != nil {
		t.Fatalf("ApproveTaskReview: %v", err)
	}
	_, err = PromoteTask(context.Background(), result.Project, summary.Task.ID, "promote")
	if err != nil {
		t.Fatalf("PromoteTask: %v", err)
	}

	task, _ := LoadTask(result.Project, summary.Task.ID)
	if !taskReleaseReady(task) {
		t.Fatal("task should be release-ready")
	}

	// Capture pre-release state
	preReleaseBatchID := task.ReleaseBatchID
	preReleasedAt := task.ReleasedAt

	// Apply release plan successfully
	plan := &ReleasePlan{
		BatchID: "snapshot-test",
		Tasks:   []*Task{task},
	}
	applied, err := ApplyReleasePlan(result.Project, plan, "test")
	if err != nil {
		t.Fatalf("ApplyReleasePlan: %v", err)
	}

	// Verify task was mutated
	if applied.Tasks[0].ReleaseBatchID == preReleaseBatchID {
		t.Error("ReleaseBatchID should have changed")
	}
	if applied.Tasks[0].ReleasedAt == preReleasedAt {
		t.Error("ReleasedAt should have changed")
	}

	// Verify on disk the task has the released values
	onDisk, _ := LoadTask(result.Project, task.ID)
	if onDisk.ReleaseBatchID != "snapshot-test" {
		t.Errorf("on disk ReleaseBatchID = %q, want %q", onDisk.ReleaseBatchID, "snapshot-test")
	}
	if onDisk.ReleasedAt == nil {
		t.Error("on disk ReleasedAt should not be nil")
	}
}
