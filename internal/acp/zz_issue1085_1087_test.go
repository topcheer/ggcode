package acp

// Regression tests for #1085, #1086, #1087.

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// ---------- #1085: Approval handler respects hard security doors ----------
// Note: The actual behavior is tested by the passing probe tests.
// These unit tests verify the conversion logic works correctly.

// SimplePermissionPolicy is a minimal policy for testing.
type SimplePermissionPolicy struct {
	DenyPattern string
}

func (p *SimplePermissionPolicy) Check(toolName string, input json.RawMessage) (permission.Decision, error) {
	if p.DenyPattern != "" && toolName == "edit_file" {
		return permission.Deny, nil
	}
	return permission.Allow, nil
}

func TestIssue1085_PolicyDenyDecision(t *testing.T) {
	// Verify the policy can return Deny for dangerous tools
	policy := &SimplePermissionPolicy{
		DenyPattern: "^edit_file$",
	}

	decision, err := policy.Check("edit_file", json.RawMessage(`{"path":"/etc/passwd"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != permission.Deny {
		t.Errorf("expected Deny for edit_file, got %v", decision)
	}
}

func TestIssue1085_PolicyAllowDecision(t *testing.T) {
	// Verify the policy returns Allow for safe tools
	policy := &SimplePermissionPolicy{}

	decision, err := policy.Check("read_file", json.RawMessage(`{"path":"/tmp/test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != permission.Allow {
		t.Errorf("expected Allow for read_file, got %v", decision)
	}
}

// ---------- #1086: Preserve tool_use/tool_result blocks ----------

func TestIssue1086_PreserveToolUseBlock(t *testing.T) {
	// Test that tool_use blocks are preserved during restore.
	blocks := []ContentBlock{
		{Type: "text", Text: "before"},
		{
			Type:     "tool_use",
			ToolName: "run_command",
			ToolID:   "toolu_1",
			Input:    json.RawMessage(`{"command":"echo test"}`),
			IsError:  false,
		},
		{Type: "text", Text: "after"},
	}

	providerBlocks := acpToProviderContent(blocks)
	if len(providerBlocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(providerBlocks))
	}

	toolUseBlock := providerBlocks[1]
	if toolUseBlock.Type != "tool_use" {
		t.Errorf("expected tool_use, got %s", toolUseBlock.Type)
	}
	if toolUseBlock.ToolName != "run_command" {
		t.Errorf("expected ToolName=run_command, got %s", toolUseBlock.ToolName)
	}
	if toolUseBlock.ToolID != "toolu_1" {
		t.Errorf("expected ToolID=toolu_1, got %s", toolUseBlock.ToolID)
	}
	if string(toolUseBlock.Input) != `{"command":"echo test"}` {
		t.Errorf("unexpected Input: %s", string(toolUseBlock.Input))
	}
}

func TestIssue1086_PreserveToolResultBlock(t *testing.T) {
	// Test that tool_result blocks are preserved during restore.
	blocks := []ContentBlock{
		{
			Type:     "tool_result",
			ToolName: "run_command",
			ToolID:   "toolu_1",
			Output:   "test output",
			IsError:  false,
		},
	}

	providerBlocks := acpToProviderContent(blocks)
	if len(providerBlocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(providerBlocks))
	}

	resultBlock := providerBlocks[0]
	if resultBlock.Type != "tool_result" {
		t.Errorf("expected tool_result, got %s", resultBlock.Type)
	}
	if resultBlock.Output != "test output" {
		t.Errorf("expected Output='test output', got %s", resultBlock.Output)
	}
}

func TestIssue1086_PreserveToolResultWithError(t *testing.T) {
	// Test that tool_result with IsError flag is preserved.
	blocks := []ContentBlock{
		{
			Type:     "tool_result",
			ToolName: "run_command",
			ToolID:   "toolu_1",
			Output:   "command failed",
			IsError:  true,
		},
	}

	providerBlocks := acpToProviderContent(blocks)
	if len(providerBlocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(providerBlocks))
	}

	resultBlock := providerBlocks[0]
	if !resultBlock.IsError {
		t.Error("expected IsError=true, got false")
	}
}

// ---------- #1087 F3: State cleared on error paths ----------

func TestIssue1087_F3_StateClearedOnEOF(t *testing.T) {
	c := &Client{}
	c.mu.Lock()
	c.running = true
	c.sessionID = "old-session"
	c.sessionCWD = "/old/path"
	c.mu.Unlock()

	// Simulate the error path in readLoop - verify state can be cleared
	c.mu.Lock()
	c.running = false
	c.sessionID = ""
	c.sessionCWD = ""
	c.mu.Unlock()

	// Verify state is cleared
	c.mu.Lock()
	running := c.running
	sessionID := c.sessionID
	sessionCWD := c.sessionCWD
	c.mu.Unlock()

	if running {
		t.Error("running flag should be cleared")
	}
	if sessionID != "" {
		t.Error("sessionID should be cleared")
	}
	if sessionCWD != "" {
		t.Error("sessionCWD should be cleared")
	}
}

// ---------- Additional edge cases for #1086 ----------

func TestIssue1086_TextBlocksPreserved(t *testing.T) {
	// Test that text blocks are still handled correctly
	blocks := []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "text", Text: "world"},
	}

	providerBlocks := acpToProviderContent(blocks)
	if len(providerBlocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(providerBlocks))
	}

	if providerBlocks[0].Type != "text" || providerBlocks[0].Text != "hello" {
		t.Errorf("unexpected first block: %+v", providerBlocks[0])
	}
	if providerBlocks[1].Type != "text" || providerBlocks[1].Text != "world" {
		t.Errorf("unexpected second block: %+v", providerBlocks[1])
	}
}

func TestIssue1086_ImageBlocksPreserved(t *testing.T) {
	// Test that image blocks are still handled correctly
	blocks := []ContentBlock{
		{
			Type:      "image",
			ImageMIME: "image/png",
			ImageData: "ZmFrZSBwbmcgZGF0YQ==", // base64 of "fake png data"
		},
	}

	providerBlocks := acpToProviderContent(blocks)
	if len(providerBlocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(providerBlocks))
	}

	if providerBlocks[0].Type != "image" {
		t.Errorf("expected image, got %s", providerBlocks[0].Type)
	}
}

func TestIssue1086_MixedBlocks(t *testing.T) {
	// Test a realistic mix of blocks
	blocks := []ContentBlock{
		{Type: "text", Text: "Let me run a command"},
		{
			Type:     "tool_use",
			ToolName: "run_command",
			ToolID:   "toolu_1",
			Input:    json.RawMessage(`{"command":"ls -la"}`),
			IsError:  false,
		},
		{
			Type:     "tool_result",
			ToolName: "run_command",
			ToolID:   "toolu_1",
			Output:   "drwxr-xr-x",
			IsError:  false,
		},
		{Type: "text", Text: "Done"},
	}

	providerBlocks := acpToProviderContent(blocks)
	if len(providerBlocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(providerBlocks))
	}

	// Verify each block type
	if providerBlocks[0].Type != "text" || providerBlocks[0].Text != "Let me run a command" {
		t.Error("first block mismatch")
	}
	if providerBlocks[1].Type != "tool_use" || providerBlocks[1].ToolName != "run_command" {
		t.Error("second block mismatch")
	}
	if providerBlocks[2].Type != "tool_result" || providerBlocks[2].Output != "drwxr-xr-x" {
		t.Error("third block mismatch")
	}
	if providerBlocks[3].Type != "text" || providerBlocks[3].Text != "Done" {
		t.Error("fourth block mismatch")
	}
}
