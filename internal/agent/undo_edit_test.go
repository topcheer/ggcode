package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestUndoEdit_NoCheckpoints verifies that undo with no checkpoints returns an error.
func TestUndoEdit_NoCheckpoints(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.SetCheckpointManager(checkpoint.NewManager(10))

	result := a.executeUndoEdit(context.Background(), provider.ToolCallDelta{
		Name:      "undo_edit",
		Arguments: json.RawMessage(`{"action":"undo","description":"test"}`),
	})
	if !result.IsError {
		t.Fatal("expected error when no checkpoints exist")
	}
	if !strings.Contains(result.Content, "Nothing to undo") {
		t.Fatalf("unexpected message: %s", result.Content)
	}
}

// TestUndoEdit_NilCheckpointManager verifies graceful handling when checkpoint manager is nil.
func TestUndoEdit_NilCheckpointManager(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	// Do NOT set a checkpoint manager

	result := a.executeUndoEdit(context.Background(), provider.ToolCallDelta{
		Name:      "undo_edit",
		Arguments: json.RawMessage(`{"action":"undo","description":"test"}`),
	})
	if !result.IsError {
		t.Fatal("expected error when checkpoint manager is nil")
	}
	if !strings.Contains(result.Content, "not initialized") {
		t.Fatalf("unexpected message: %s", result.Content)
	}
}

// TestUndoEdit_UndoAndList verifies the full undo + list lifecycle.
func TestUndoEdit_UndoAndList(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")

	// Create a file, then simulate an edit via checkpoint
	originalContent := "package main\n\nfunc old() {}\n"
	if err := os.WriteFile(filePath, []byte(originalContent), 0644); err != nil {
		t.Fatal(err)
	}
	newContent := "package main\n\nfunc new() {}\n"

	a := NewAgent(&mockProvider{}, tool.NewRegistry(), dir, 1)
	cpMgr := checkpoint.NewManager(10)
	a.SetCheckpointManager(cpMgr)

	// Simulate an edit by saving a checkpoint
	cpMgr.Save(filePath, originalContent, newContent, "edit_file")

	// Write the new content to disk (simulating what edit_file would do)
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		t.Fatal(err)
	}

	// List should show 1 checkpoint
	listResult := a.executeUndoEdit(context.Background(), provider.ToolCallDelta{
		Name:      "undo_edit",
		Arguments: json.RawMessage(`{"action":"list","description":"test"}`),
	})
	if listResult.IsError {
		t.Fatalf("list failed: %s", listResult.Content)
	}
	if !strings.Contains(listResult.Content, "test.go") {
		t.Fatalf("list should contain file path: %s", listResult.Content)
	}
	if !strings.Contains(listResult.Content, "edit_file") {
		t.Fatalf("list should contain tool call: %s", listResult.Content)
	}

	// Undo should revert the file
	undoResult := a.executeUndoEdit(context.Background(), provider.ToolCallDelta{
		Name:      "undo_edit",
		Arguments: json.RawMessage(`{"action":"undo","description":"test"}`),
	})
	if undoResult.IsError {
		t.Fatalf("undo failed: %s", undoResult.Content)
	}
	if !strings.Contains(undoResult.Content, "Reverted") {
		t.Fatalf("undo should mention revert: %s", undoResult.Content)
	}

	// Verify the file was actually reverted on disk
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != originalContent {
		t.Fatalf("file not reverted: got %q, want %q", string(data), originalContent)
	}
}

// TestUndoEdit_Revert verifies reverting to a specific checkpoint by ID.
func TestUndoEdit_Revert(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "revert_test.go")

	a := NewAgent(&mockProvider{}, tool.NewRegistry(), dir, 1)
	cpMgr := checkpoint.NewManager(10)
	a.SetCheckpointManager(cpMgr)

	// Save multiple checkpoints
	v1 := "package main\n\nfunc v1() {}\n"
	v2 := "package main\n\nfunc v2() {}\n"
	v3 := "package main\n\nfunc v3() {}\n"

	cp1 := cpMgr.Save(filePath, v1, v2, "edit_file")
	if err := os.WriteFile(filePath, []byte(v2), 0644); err != nil {
		t.Fatal(err)
	}
	cpMgr.Save(filePath, v2, v3, "edit_file")
	if err := os.WriteFile(filePath, []byte(v3), 0644); err != nil {
		t.Fatal(err)
	}

	// Revert to the first checkpoint
	revertResult := a.executeUndoEdit(context.Background(), provider.ToolCallDelta{
		Name:      "undo_edit",
		Arguments: json.RawMessage(`{"action":"revert","checkpoint_id":"` + cp1.ID + `","description":"test"}`),
	})
	if revertResult.IsError {
		t.Fatalf("revert failed: %s", revertResult.Content)
	}
	if !strings.Contains(revertResult.Content, "Reverted") {
		t.Fatalf("revert should mention revert: %s", revertResult.Content)
	}

	// File should be back to v1
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != v1 {
		t.Fatalf("file not reverted to v1: got %q", string(data))
	}
}

// TestUndoEdit_RevertMissingID verifies error when checkpoint_id is missing.
func TestUndoEdit_RevertMissingID(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.SetCheckpointManager(checkpoint.NewManager(10))

	result := a.executeUndoEdit(context.Background(), provider.ToolCallDelta{
		Name:      "undo_edit",
		Arguments: json.RawMessage(`{"action":"revert","description":"test"}`),
	})
	if !result.IsError {
		t.Fatal("expected error when checkpoint_id is missing")
	}
	if !strings.Contains(result.Content, "checkpoint_id is required") {
		t.Fatalf("unexpected message: %s", result.Content)
	}
}

// TestUndoEdit_InvalidAction verifies error for unknown actions.
func TestUndoEdit_InvalidAction(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.SetCheckpointManager(checkpoint.NewManager(10))

	result := a.executeUndoEdit(context.Background(), provider.ToolCallDelta{
		Name:      "undo_edit",
		Arguments: json.RawMessage(`{"action":"frobnicate","description":"test"}`),
	})
	if !result.IsError {
		t.Fatal("expected error for invalid action")
	}
	if !strings.Contains(result.Content, "Unknown action") {
		t.Fatalf("unexpected message: %s", result.Content)
	}
}

// TestUndoEdit_DefaultAction verifies that empty action defaults to "undo".
func TestUndoEdit_DefaultAction(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.SetCheckpointManager(checkpoint.NewManager(10))

	result := a.executeUndoEdit(context.Background(), provider.ToolCallDelta{
		Name:      "undo_edit",
		Arguments: json.RawMessage(`{"description":"test"}`),
	})
	if !result.IsError {
		t.Fatal("expected error (no checkpoints) when action is omitted")
	}
	// Should mention "Nothing to undo" which proves it defaulted to undo
	if !strings.Contains(result.Content, "Nothing to undo") {
		t.Fatalf("should default to undo action: %s", result.Content)
	}
}

// TestUndoEdit_StandaloneExecute verifies the tool's Execute method returns an error
// when called outside the agent runtime (no checkpoint manager available).
func TestUndoEdit_StandaloneExecute(t *testing.T) {
	t2 := tool.UndoEditTool{}
	result, err := t2.Execute(context.Background(), json.RawMessage(`{"action":"undo"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result from standalone execution")
	}
}
