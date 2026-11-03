package acp

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

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
	policy := &SimplePermissionPolicy{}

	decision, err := policy.Check("read_file", json.RawMessage(`{"path":"/tmp/test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != permission.Allow {
		t.Errorf("expected Allow for read_file, got %v", decision)
	}
}

func TestIssue1086_PreserveToolUseBlock(t *testing.T) {
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

func TestIssue1087_F3_StateClearedOnEOF(t *testing.T) {
	c := &Client{}
	c.mu.Lock()
	c.running = true
	c.sessionID = "old-session"
	c.sessionCWD = "/old/path"
	c.mu.Unlock()

	c.mu.Lock()
	c.running = false
	c.sessionID = ""
	c.sessionCWD = ""
	c.mu.Unlock()

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

func TestIssue1086_TextBlocksPreserved(t *testing.T) {
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
	blocks := []ContentBlock{
		{
			Type:      "image",
			ImageMIME: "image/png",
			ImageData: "ZmFrZSBwbmcgZGF0YQ==",
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
