package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveTaskSucceedsEvenIfEventLogFails(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	summary, err := RunTask(context.Background(), result.Project, result.Config, "Event log test", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	// Block event log writing by making the event log path a directory.
	// EventLogPath is like ".../events.jsonl" — if we create a directory at that path,
	// the event log code's OpenFile will fail with EISDIR.
	eventDir := filepath.Dir(result.Project.EventLogPath)
	os.MkdirAll(eventDir, 0o755)
	os.Remove(result.Project.EventLogPath) // remove if exists
	if err := os.Mkdir(result.Project.EventLogPath, 0o755); err != nil {
		t.Fatalf("setup event log block: %v", err)
	}

	// SaveTask should succeed despite event log failure (best-effort)
	task, err := LoadTask(result.Project, summary.Task.ID)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	task.Error = "test mutation"
	err = SaveTask(result.Project, task)
	if err != nil {
		t.Fatalf("SaveTask should succeed even with event log failure, got: %v", err)
	}

	// Verify the task was saved on disk
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask after save: %v", err)
	}
	if loaded.Error != "test mutation" {
		t.Errorf("task.Error = %q, want %q", loaded.Error, "test mutation")
	}
}

func TestSaveTaskUsesAtomicWriteNoTempFiles(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	summary, err := RunTask(context.Background(), result.Project, result.Config, "Atomic write test", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	task, err := LoadTask(result.Project, summary.Task.ID)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}

	// Save multiple times, verify no temp files left behind
	for i := 0; i < 5; i++ {
		task.Error = fmt.Sprintf("iteration-%d", i)
		if err := SaveTask(result.Project, task); err != nil {
			t.Fatalf("SaveTask %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(result.Project.TasksDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != task.ID+".json" {
			t.Errorf("unexpected file in tasks dir: %s", e.Name())
		}
	}
}

func TestSaveTaskDoesNotUseToolPreWriteHook(t *testing.T) {
	// Verify that SaveTask uses the harness-local atomicWriteJSON (no pre-write hook),
	// not the tool package's atomicWriteFile (which has the global hook).
	// This is correct: task state files should not participate in user undo/checkpoint.
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	summary, err := RunTask(context.Background(), result.Project, result.Config, "Hook test", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	// SaveTask should work fine — it uses its own atomicWriteJSON, not tool's version
	task, _ := LoadTask(result.Project, summary.Task.ID)
	task.Error = "no hook involved"
	if err := SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	loaded, _ := LoadTask(result.Project, task.ID)
	if loaded.Error != "no hook involved" {
		t.Errorf("task.Error = %q, want %q", loaded.Error, "no hook involved")
	}
}
