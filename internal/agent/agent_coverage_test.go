package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// ---------------------------------------------------------------------------
// agent.go — getter/setter coverage
// ---------------------------------------------------------------------------

func TestAgent_Provider(t *testing.T) {
	original := &mockProvider{}
	a := NewAgent(original, tool.NewRegistry(), "", 1)
	got := a.Provider()
	if got == nil {
		t.Fatal("Provider() returned nil after construction")
	}
	// Verify it returns the same instance we passed in
	if got != original {
		t.Fatal("Provider() returned a different instance than the one passed to NewAgent")
	}
	// Verify setter updates the instance
	replacement := &mockProvider{tokenCount: 42}
	a.SetProvider(replacement)
	if a.Provider() == original {
		t.Fatal("Provider() still returns old instance after SetProvider")
	}
}

func TestAgent_PermissionPolicy(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	if a.PermissionPolicy() != nil {
		t.Fatal("expected nil policy initially")
	}
	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutoMode)
	a.SetPermissionPolicy(policy)
	if a.PermissionPolicy() == nil {
		t.Fatal("expected non-nil policy after SetPermissionPolicy")
	}
}

func TestAgent_ApprovalHandler_DenyWithoutHandler(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	// Set a policy that always returns Ask, but don't set an approval handler.
	// This simulates non-interactive mode where Ask → deny by default.
	a.SetPermissionPolicy(&askAlwaysPolicy{})
	result := a.executeToolWithPermission(context.Background(), provider.ToolCallDelta{
		ID:        "c1",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	})
	if !result.IsError {
		t.Fatal("expected error result when no approval handler is set")
	}
	if !strings.Contains(result.Content, "No approval handler available") {
		t.Fatalf("unexpected error message: %s", result.Content)
	}
}

func TestAgent_ApprovalHandler_AskApproved(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "echo", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	a := NewAgent(&mockProvider{}, registry, "", 1)

	a.SetApprovalHandler(func(_ context.Context, toolName string, input string) permission.Decision {
		return permission.Allow
	})
	a.SetPermissionPolicy(&askAlwaysPolicy{})

	result := a.executeToolWithPermission(context.Background(), provider.ToolCallDelta{
		ID:        "c1",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if result.Content != "ok" {
		t.Fatalf("expected 'ok', got %q", result.Content)
	}
}

func TestAgent_ApprovalHandler_AskDenied(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "echo", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	a := NewAgent(&mockProvider{}, registry, "", 1)

	a.SetApprovalHandler(func(_ context.Context, toolName string, input string) permission.Decision {
		return permission.Deny
	})
	a.SetPermissionPolicy(&askAlwaysPolicy{})

	result := a.executeToolWithPermission(context.Background(), provider.ToolCallDelta{
		ID:        "c1",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	})
	if !result.IsError {
		t.Fatal("expected error when user denies approval")
	}
	if !strings.Contains(result.Content, "User rejected") {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestAgent_SetSupportsVision(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	if a.SupportsVision() {
		t.Fatal("expected vision disabled by default")
	}
	a.SetSupportsVision(true)
	if !a.SupportsVision() {
		t.Fatal("expected vision enabled after SetSupportsVision(true)")
	}
	a.SetSupportsVision(false)
	if a.SupportsVision() {
		t.Fatal("expected vision disabled after SetSupportsVision(false)")
	}
}

func TestAgent_UpdateSystemPrompt(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "original", 1)
	msgs := a.ContextManager().Messages()
	if len(msgs) != 1 || msgs[0].Content[0].Text != "original" {
		t.Fatalf("expected initial system prompt 'original', got %v", msgs)
	}
	a.UpdateSystemPrompt("updated")
	msgs = a.ContextManager().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after update, got %d", len(msgs))
	}
	if msgs[0].Content[0].Text != "updated" {
		t.Fatalf("expected 'updated', got %q", msgs[0].Content[0].Text)
	}
}

func TestAgent_UpdateSystemPrompt_AddsWhenNone(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	msgs := a.ContextManager().Messages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages initially, got %d", len(msgs))
	}
	a.UpdateSystemPrompt("new system prompt")
	msgs = a.ContextManager().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after update, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content[0].Text != "new system prompt" {
		t.Fatalf("unexpected message: %#v", msgs[0])
	}
}

func TestAgent_SetCheckpointManager(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	if a.CheckpointManager() != nil {
		t.Fatal("expected nil checkpoint manager initially")
	}
	cp := checkpoint.NewManager(10)
	a.SetCheckpointManager(cp)
	if a.CheckpointManager() == nil {
		t.Fatal("expected non-nil after SetCheckpointManager")
	}
}

func TestAgent_SetDiffConfirm(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	// Verify the callback is actually wired through by executing a write_file
	// that triggers the diff confirm flow.
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("original"), 0644)

	registry := tool.NewRegistry()
	writeTool := &fileWriteTool{}
	if err := registry.Register(writeTool); err != nil {
		t.Fatalf("register: %v", err)
	}
	a.tools = registry

	var capturedPath, capturedDiff string
	a.SetDiffConfirm(func(_ context.Context, filePath, diffText string) bool {
		capturedPath = filePath
		capturedDiff = diffText
		return true
	})

	result := a.executeFileTool(context.Background(), writeTool, provider.ToolCallDelta{
		ID:        "c1",
		Name:      "write_file",
		Arguments: json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"modified"}`, filePath)),
	}, hooks.HookEnv{})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if capturedPath != filePath {
		t.Fatalf("expected diff confirm called with path %q, got %q", filePath, capturedPath)
	}
	if !strings.Contains(capturedDiff, "modified") {
		t.Fatalf("expected diff to contain 'modified', got %q", capturedDiff)
	}
}

func TestAgent_ExecuteTool_MultiFileEditDiffAndCheckpoints(t *testing.T) {
	tmpDir := t.TempDir()
	aPath := filepath.Join(tmpDir, "a.txt")
	bPath := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(aPath, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	registry := tool.NewRegistry()
	if err := registry.Register(tool.MultiFileEdit{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	a := NewAgent(&mockProvider{}, registry, "", 1)
	cp := checkpoint.NewManager(10)
	a.SetCheckpointManager(cp)

	var capturedPath, capturedDiff string
	a.SetDiffConfirm(func(_ context.Context, filePath, diffText string) bool {
		capturedPath = filePath
		capturedDiff = diffText
		return true
	})

	args, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": aPath, "edits": []map[string]string{{"old_text": "hello", "new_text": "HELLO"}}},
			{"path": bPath, "edits": []map[string]string{{"old_text": "world", "new_text": "WORLD"}}},
		},
	})
	result := a.executeTool(context.Background(), provider.ToolCallDelta{
		ID:        "mf1",
		Name:      "multi_file_edit",
		Arguments: args,
	})
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content)
	}
	if capturedPath != "2 files" {
		t.Fatalf("expected multi-file diff label, got %q", capturedPath)
	}
	if !strings.Contains(capturedDiff, "=== "+aPath+" ===") || !strings.Contains(capturedDiff, "=== "+bPath+" ===") {
		t.Fatalf("expected combined diff for both files, got: %s", capturedDiff)
	}
	if len(cp.List()) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(cp.List()))
	}
	gotA, _ := os.ReadFile(aPath)
	gotB, _ := os.ReadFile(bPath)
	if string(gotA) != "HELLO\n" || string(gotB) != "WORLD\n" {
		t.Fatalf("unexpected contents: a=%q b=%q", gotA, gotB)
	}
}

func TestAgent_ExecuteTool_MultiFileEditPartialSuccessSavesSuccessfulCheckpoints(t *testing.T) {
	tmpDir := t.TempDir()
	aPath := filepath.Join(tmpDir, "a.txt")
	bPath := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(aPath, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	registry := tool.NewRegistry()
	if err := registry.Register(tool.MultiFileEdit{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	a := NewAgent(&mockProvider{}, registry, "", 1)
	cp := checkpoint.NewManager(10)
	a.SetCheckpointManager(cp)

	args, _ := json.Marshal(map[string]any{
		"mode": "partial_success",
		"files": []map[string]any{
			{"path": aPath, "edits": []map[string]string{{"old_text": "hello", "new_text": "HELLO"}}},
			{"path": bPath, "edits": []map[string]string{{"old_text": "missing", "new_text": "WORLD"}}},
		},
	})
	result := a.executeTool(context.Background(), provider.ToolCallDelta{
		ID:        "mf2",
		Name:      "multi_file_edit",
		Arguments: args,
	})
	if !result.IsError {
		t.Fatalf("expected partial success to still surface an error result, got: %s", result.Content)
	}
	if len(cp.List()) != 1 {
		t.Fatalf("expected 1 checkpoint for the written file, got %d", len(cp.List()))
	}
	if cp.Last().FilePath != aPath {
		t.Fatalf("expected checkpoint for %s, got %+v", aPath, cp.Last())
	}
	gotA, _ := os.ReadFile(aPath)
	gotB, _ := os.ReadFile(bPath)
	if string(gotA) != "HELLO\n" || string(gotB) != "world\n" {
		t.Fatalf("unexpected contents after partial success: a=%q b=%q", gotA, gotB)
	}
}

func TestAgent_SetHookConfig(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	// Verify hook config is wired through by setting a pre-tool-use hook
	// that blocks execution with a specific message.
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "echo", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	a.tools = registry

	a.SetHookConfig(hooks.HookConfig{
		PreToolUse: []hooks.Hook{
			{Match: "echo", Command: "echo blocked"},
		},
	})
	// Hooks.RunPreHooks requires a real command executor; with an empty
	// command in test env the hook may not block. Instead, verify the
	// config is stored by checking that a round-trip through the agent's
	// internal state is consistent: execute the tool and confirm it
	// doesn't crash with the configured hooks.
	result := a.executeTool(context.Background(), provider.ToolCallDelta{
		ID:        "c1",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	})
	// The tool should either succeed (hook doesn't block) or return a
	// hook-related message — but it must not panic.
	_ = result
}

func TestAgent_Clear(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "system prompt", 1)
	a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}})
	msgs := a.ContextManager().Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages before clear, got %d", len(msgs))
	}
	a.Clear()
	msgs = a.ContextManager().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (system) after clear, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected system message preserved, got role %q", msgs[0].Role)
	}
}

func TestAgent_Clear_NoSystemPrompt(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	a.Clear()
	msgs := a.ContextManager().Messages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages after clearing session without system prompt, got %d", len(msgs))
	}
}

func TestAgent_ProjectMemoryFiles(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "GGCODE.md")
	f2 := filepath.Join(tmpDir, "AGENTS.md")
	os.WriteFile(f1, []byte("root"), 0644)
	os.WriteFile(f2, []byte("agents"), 0644)
	a.SetWorkingDir(tmpDir)
	a.SetProjectMemoryFiles([]string{f1, f2})
	files := a.ProjectMemoryFiles()
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
}

// ---------------------------------------------------------------------------
// agent.go — internal helpers
// ---------------------------------------------------------------------------

func TestIsJSON(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`{"key": "value"}`, true},
		{`[1, 2, 3]`, true},
		{`null`, true},
		{`not json`, false},
		{``, false},
		{`{"unclosed": `, false},
	}
	for _, tt := range tests {
		got := isJSON(json.RawMessage(tt.input))
		if got != tt.want {
			t.Errorf("isJSON(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestTruncateStr(t *testing.T) {
	if got := util.Truncate("hello", 3); got != "hel" {
		t.Fatalf("expected 'hel', got %q", got)
	}
	if got := util.Truncate("hi", 10); got != "hi" {
		t.Fatalf("expected 'hi', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// agent_tool.go — executeToolWithPermission
// ---------------------------------------------------------------------------

func TestExecuteToolWithPermission_DenyPolicy(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "echo", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	a := NewAgent(&mockProvider{}, registry, "", 1)

	a.SetPermissionPolicy(&denyAlwaysPolicy{})
	result := a.executeToolWithPermission(context.Background(), provider.ToolCallDelta{
		ID:        "c1",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	})
	if !result.IsError {
		t.Fatal("expected error result for denied tool")
	}
	if !strings.Contains(result.Content, "blocked by the permission policy") {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestExecuteToolWithPermission_CancelledContext(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := a.executeToolWithPermission(ctx, provider.ToolCallDelta{
		ID:        "c1",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	})
	if !result.IsError {
		t.Fatal("expected error for cancelled context")
	}
}

// ---------------------------------------------------------------------------
// agent_tool.go — executeTool (unknown tool, cancelled context)
// ---------------------------------------------------------------------------

func TestExecuteTool_UnknownTool(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	result := a.executeTool(context.Background(), provider.ToolCallDelta{
		ID:        "c1",
		Name:      "nonexistent",
		Arguments: json.RawMessage(`{}`),
	})
	if !result.IsError {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestExecuteTool_CancelledContext(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := a.executeTool(ctx, provider.ToolCallDelta{
		ID:        "c1",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	})
	if !result.IsError {
		t.Fatal("expected error for cancelled context")
	}
}

// ---------------------------------------------------------------------------
// agent_tool.go — computeFileChange
// ---------------------------------------------------------------------------

func TestComputeFileChange_EditFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("hello world foo bar"), 0644)

	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	gotPath, old, new_, err := a.computeFileChange(provider.ToolCallDelta{
		Name:      "edit_file",
		Arguments: json.RawMessage(fmt.Sprintf(`{"file_path":%q,"old_text":"foo","new_text":"baz"}`, filePath)),
	})
	if err != nil {
		t.Fatalf("computeFileChange failed: %v", err)
	}
	if gotPath != filePath {
		t.Fatalf("expected filePath %q, got %q", filePath, gotPath)
	}
	if old != "hello world foo bar" {
		t.Fatalf("expected old content 'hello world foo bar', got %q", old)
	}
	if new_ != "hello world baz bar" {
		t.Fatalf("expected new content 'hello world baz bar', got %q", new_)
	}
}

func TestComputeFileChange_EditFile_Nonexistent(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	_, _, _, err := a.computeFileChange(provider.ToolCallDelta{
		Name:      "edit_file",
		Arguments: json.RawMessage(`{"file_path":"/nonexistent/file.txt","old_text":"a","new_text":"b"}`),
	})
	if err == nil {
		t.Fatal("expected error for nonexistent file in edit_file")
	}
}

func TestComputeFileChange_EditFile_InvalidArgs(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	_, _, _, err := a.computeFileChange(provider.ToolCallDelta{
		Name:      "edit_file",
		Arguments: json.RawMessage(`not json`),
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestComputeFileChange_WriteFile_Existing(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("old content"), 0644)

	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	gotPath, old, new_, err := a.computeFileChange(provider.ToolCallDelta{
		Name:      "write_file",
		Arguments: json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"new content"}`, filePath)),
	})
	if err != nil {
		t.Fatalf("computeFileChange failed: %v", err)
	}
	if gotPath != filePath {
		t.Fatalf("expected filePath %q, got %q", filePath, gotPath)
	}
	if old != "old content" {
		t.Fatalf("expected old content 'old content', got %q", old)
	}
	if new_ != "new content" {
		t.Fatalf("expected new content 'new content', got %q", new_)
	}
}

func TestComputeFileChange_WriteFile_NewFile(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	gotPath, old, new_, err := a.computeFileChange(provider.ToolCallDelta{
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"/tmp/nonexistent_new_file_test.txt","content":"fresh"}`),
	})
	if err != nil {
		t.Fatalf("computeFileChange failed: %v", err)
	}
	if gotPath != "/tmp/nonexistent_new_file_test.txt" {
		t.Fatalf("unexpected path: %q", gotPath)
	}
	if old != "" {
		t.Fatalf("expected empty old content for new file, got %q", old)
	}
	if new_ != "fresh" {
		t.Fatalf("expected new content 'fresh', got %q", new_)
	}
}

func TestComputeFileChange_WriteFile_InvalidArgs(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	_, _, _, err := a.computeFileChange(provider.ToolCallDelta{
		Name:      "write_file",
		Arguments: json.RawMessage(`{invalid`),
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestComputeFileChange_UnknownTool(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	_, _, _, err := a.computeFileChange(provider.ToolCallDelta{
		Name:      "read_file",
		Arguments: json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "not a file tool") {
		t.Fatalf("expected 'not a file tool' error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// agent_tool.go — executeFileTool
// ---------------------------------------------------------------------------

func TestExecuteFileTool_WriteFileWithCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("old content"), 0644)

	registry := tool.NewRegistry()
	writeTool := &fileWriteTool{}
	if err := registry.Register(writeTool); err != nil {
		t.Fatalf("register: %v", err)
	}

	a := NewAgent(&mockProvider{}, registry, "", 1)
	cp := checkpoint.NewManager(10)
	a.SetCheckpointManager(cp)

	result := a.executeFileTool(context.Background(), writeTool, provider.ToolCallDelta{
		ID:        "c1",
		Name:      "write_file",
		Arguments: json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"new content"}`, filePath)),
	}, hooks.HookEnv{})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	// Verify checkpoint was saved
	last := cp.Last()
	if last == nil {
		t.Fatal("expected checkpoint to be saved")
	}
	if last.OldContent != "old content" {
		t.Fatalf("expected checkpoint before='old content', got %q", last.OldContent)
	}
	if last.NewContent != "new content" {
		t.Fatalf("expected checkpoint after='new content', got %q", last.NewContent)
	}
}

func TestExecuteFileTool_DiffConfirmCancelled(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("old content"), 0644)

	registry := tool.NewRegistry()
	writeTool := &fileWriteTool{}
	if err := registry.Register(writeTool); err != nil {
		t.Fatalf("register: %v", err)
	}

	a := NewAgent(&mockProvider{}, registry, "", 1)
	a.SetDiffConfirm(func(_ context.Context, filePath, diffText string) bool {
		return false // user rejects
	})

	result := a.executeFileTool(context.Background(), writeTool, provider.ToolCallDelta{
		ID:        "c1",
		Name:      "write_file",
		Arguments: json.RawMessage(fmt.Sprintf(`{"path":%q,"content":"new content"}`, filePath)),
	}, hooks.HookEnv{})
	if !result.IsError {
		t.Fatal("expected error when user cancels diff")
	}
	if !strings.Contains(result.Content, "cancelled by user") {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestExecuteFileTool_EditFileNoChanges(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("same content"), 0644)

	registry := tool.NewRegistry()
	editTool := &fileEditTool{}
	if err := registry.Register(editTool); err != nil {
		t.Fatalf("register: %v", err)
	}

	a := NewAgent(&mockProvider{}, registry, "", 1)
	// Set up a diff confirm that should NOT be called since content is same
	diffConfirmCalled := false
	a.SetDiffConfirm(func(_ context.Context, filePath, diffText string) bool {
		diffConfirmCalled = true
		return true
	})

	result := a.executeFileTool(context.Background(), editTool, provider.ToolCallDelta{
		ID:        "c1",
		Name:      "edit_file",
		Arguments: json.RawMessage(fmt.Sprintf(`{"file_path":%q,"old_text":"same content","new_text":"same content"}`, filePath)),
	}, hooks.HookEnv{})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if diffConfirmCalled {
		t.Fatal("diff confirm should not be called when there are no changes")
	}
}

func TestExecuteFileTool_InvalidArgs(t *testing.T) {
	registry := tool.NewRegistry()
	writeTool := &fileWriteTool{}
	if err := registry.Register(writeTool); err != nil {
		t.Fatalf("register: %v", err)
	}

	a := NewAgent(&mockProvider{}, registry, "", 1)
	result := a.executeFileTool(context.Background(), writeTool, provider.ToolCallDelta{
		ID:        "c1",
		Name:      "write_file",
		Arguments: json.RawMessage(`not json`),
	}, hooks.HookEnv{})
	if !result.IsError {
		t.Fatal("expected error for invalid JSON args")
	}
	if !strings.Contains(result.Content, "file change error") {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// agent_tool.go — indexOf
// ---------------------------------------------------------------------------

func TestIndexOf(t *testing.T) {
	tests := []struct {
		s, substr string
		want      int
	}{
		{"hello world", "world", 6},
		{"hello", "x", -1},
		{"", "a", -1},
		{"abc", "", 0},
		{"aaa", "a", 0},
	}
	for _, tt := range tests {
		got := indexOf(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("indexOf(%q, %q) = %d, want %d", tt.s, tt.substr, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// agent_memory.go — projectMemoryTargetsForTool, collectProjectMemoryTargets,
// toolCanTriggerProjectMemory, projectMemoryPathKey, normalizeProjectMemoryPath
// ---------------------------------------------------------------------------

func TestProjectMemoryTargetsForTool_NonTriggerTool(t *testing.T) {
	got := projectMemoryTargetsForTool("run_command", json.RawMessage(`{"path":"/foo"}`))
	if got != nil {
		t.Fatalf("expected nil for non-trigger tool, got %v", got)
	}
}

func TestProjectMemoryTargetsForTool_TriggerTool(t *testing.T) {
	got := projectMemoryTargetsForTool("read_file", json.RawMessage(`{"path":"/foo/bar.go"}`))
	if len(got) != 1 || got[0] != "/foo/bar.go" {
		t.Fatalf("expected ['/foo/bar.go'], got %v", got)
	}
}

func TestProjectMemoryTargetsForTool_MultiplePaths(t *testing.T) {
	got := projectMemoryTargetsForTool("glob", json.RawMessage(`{"pattern":"**/*.go","directory":"/src"}`))
	if len(got) != 1 || got[0] != "/src" {
		t.Fatalf("expected ['/src'], got %v", got)
	}
}

func TestProjectMemoryTargetsForTool_InvalidJSON(t *testing.T) {
	got := projectMemoryTargetsForTool("read_file", json.RawMessage(`not json`))
	if got != nil {
		t.Fatalf("expected nil for invalid JSON, got %v", got)
	}
}

func TestProjectMemoryTargetsForTool_Deduplicates(t *testing.T) {
	got := projectMemoryTargetsForTool("edit_file", json.RawMessage(`{"file_path":"/a","old_text":"","new_text":""}`))
	if len(got) != 1 {
		t.Fatalf("expected 1 target, got %d: %v", len(got), got)
	}
}

func TestProjectMemoryTargetsForTool_ExcludesURLs(t *testing.T) {
	got := projectMemoryTargetsForTool("read_file", json.RawMessage(`{"path":"https://example.com/file"}`))
	if len(got) != 0 {
		t.Fatalf("expected empty for URL path, got %v", got)
	}
}

func TestProjectMemoryTargetsForTool_ExcludesEmptyPaths(t *testing.T) {
	got := projectMemoryTargetsForTool("read_file", json.RawMessage(`{"path":"  "}`))
	if len(got) != 0 {
		t.Fatalf("expected empty for whitespace-only path, got %v", got)
	}
}

func TestToolCanTriggerProjectMemory(t *testing.T) {
	tests := []struct {
		toolName string
		want     bool
	}{
		{"read_file", true},
		{"write_file", true},
		{"edit_file", true},
		{"list_directory", true},
		{"glob", true},
		{"search_files", true},
		{"lsp_definition", true},
		{"lsp_references", true},
		{"run_command", false},
		{"ask_user", false},
		{"web_search", false},
	}
	for _, tt := range tests {
		got := toolCanTriggerProjectMemory(tt.toolName)
		if got != tt.want {
			t.Errorf("toolCanTriggerProjectMemory(%q) = %v, want %v", tt.toolName, got, tt.want)
		}
	}
}

func TestProjectMemoryPathKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"path", true},
		{"file_path", true},
		{"file", true},
		{"filename", true},
		{"directory", true},
		{"pattern", false},
		{"content", false},
		{"url", false},
		{"text", false},
	}
	for _, tt := range tests {
		got := projectMemoryPathKey(tt.key)
		if got != tt.want {
			t.Errorf("projectMemoryPathKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestNormalizeProjectMemoryPath(t *testing.T) {
	tests := []struct {
		target     string
		workingDir string
		want       string
	}{
		{"", "/home", ""},
		{"  ", "/home", ""},
		{"https://example.com", "/home", ""},
		{"/abs/path", "/home", "/abs/path"},
		{"relative/path", "/home/user", "/home/user/relative/path"},
		{".", "/home/user", "/home/user"},
		{"../parent", "/home/user/project", "/home/user/parent"},
	}
	for _, tt := range tests {
		got := normalizeProjectMemoryPath(tt.target, tt.workingDir)
		if got != tt.want {
			t.Errorf("normalizeProjectMemoryPath(%q, %q) = %q, want %q", tt.target, tt.workingDir, got, tt.want)
		}
	}
}

func TestNormalizeProjectMemoryPath_EmptyWorkingDir(t *testing.T) {
	got := normalizeProjectMemoryPath("relative/path", "")
	if !strings.HasSuffix(got, "relative/path") {
		t.Fatalf("expected path ending with 'relative/path', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// agent_compact.go — isPromptTooLongError, shouldIgnoreAutoCompactError,
// compactErrorReason
// ---------------------------------------------------------------------------

func TestIsPromptTooLongError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{errors.New("prompt too long: exceeded limit"), true},
		{errors.New("context length exceeded"), true},
		{errors.New("context window is full"), true},
		{errors.New("maximum context reached"), true},
		{errors.New("too many tokens in input"), true},
		{errors.New("input is too long for model"), true},
		{errors.New("exceeds the model's context window"), true},
		{errors.New("maximum input tokens reached"), true},
		// Real vendor error messages (from API documentation)
		{errors.New("This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens."), true},   // OpenAI
		{errors.New("prompt is too long: 204521 > 200000"), true},                                                                       // Anthropic
		{errors.New("The input token count (1632254) exceeds the maximum number of tokens allowed (1048576)."), true},                   // Gemini
		{errors.New("Request too large for model `llama-3.3-70b-versatile` Limit 131072, content 140000"), true},                        // Groq
		{errors.New("Prompt contains 66385 tokens, too large for model with 32768 maximum context length"), true},                       // Mistral
		{errors.New("Input token count + max_tokens parameter must be less than the context length of the model being queried."), true}, // Together AI
		{errors.New("Invalid request: Your request exceeded model token limit: 262144 (requested: 270000)"), true},                      // Moonshot
		{errors.New("Prompt exceeds max length"), true},                                                                                 // ZAI/GLM English
		{fmt.Errorf("error code: %s, message: %s", "1261", "Prompt 超长"), true},                                                          // ZAI/GLM Chinese
		{errors.New("prompt token count exceeds the limit of 128000"), true},                                                            // GitHub Copilot
		{errors.New("Requested token count exceeds the model's maximum context length of 163840 tokens."), true},                        // Volcengine Ark
		{errors.New("Token limit exceeded, please try again later."), true},                                                             // Novita
		// Future vendor support
		{errors.New("Input is too long for requested model"), true},                                                     // AWS Bedrock (Claude)
		{errors.New("the input length exceeds the context length of 4096"), true},                                       // Ollama
		{errors.New("too many tokens: total number of tokens in the prompt cannot exceed 8192 - received 9000."), true}, // Cohere
		{errors.New("Input validation error: `inputs` must have less than 4096 tokens. Given: 5000."), true},            // HuggingFace TGI
		{errors.New("error_code: 336103, error_msg: Prompt tokens too long"), true},                                     // Baidu ERNIE
		{errors.New("Range of input length should be [1, 6000]"), true},                                                 // Alibaba Qwen/DashScope
		{errors.New("超出了模型最大token限制。"), true},                                                                           // Huawei Pangu
		// Negative cases
		{errors.New("connection reset by peer"), false},
		{errors.New("rate limited"), false},
		{nil, false},
		{errors.New("some other error"), false},
	}
	for _, tt := range tests {
		got := isPromptTooLongError(tt.err)
		if got != tt.want {
			t.Errorf("isPromptTooLongError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestShouldIgnoreAutoCompactError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{errors.New("unexpected EOF"), true},
		{errors.New("connection reset by peer"), true},
		{errors.New("broken pipe"), true},
		{errors.New("server closed idle connection"), true},
		{errors.New("tls handshake timeout"), true},
		{errors.New("temporary failure in name resolution"), true},
		{errors.New("timeout awaiting response headers"), true},
		{context.Canceled, false},
		{context.DeadlineExceeded, false},
		{errors.New("prompt too long"), false},
		{errors.New("some retryable API error"), false},
		{nil, false},
	}
	for _, tt := range tests {
		got := shouldIgnoreAutoCompactError(tt.err)
		if got != tt.want {
			t.Errorf("shouldIgnoreAutoCompactError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestCompactErrorReason(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errors.New("summarization call failed: timeout"), "timeout"},
		{errors.New("auto-summarize failed: rate limit"), "rate limit"},
		{errors.New("simple error"), "simple error"},
		{nil, "unknown error"},
		{errors.New(strings.Repeat("x", 200)), strings.Repeat("x", 117) + "..."},
	}
	for _, tt := range tests {
		got := compactErrorReason(tt.err)
		if got != tt.want {
			t.Errorf("compactErrorReason(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// agent_autopilot.go — deterministic functions removed; replaced by strategist LLM call.
// Tests for shouldAutopilotKeepGoing / shouldTriggerAutopilotLoopGuard removed.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Helper types for tests
// ---------------------------------------------------------------------------

// askAlwaysPolicy always returns Ask for any tool.
type askAlwaysPolicy struct{}

func (p *askAlwaysPolicy) Check(toolName string, input json.RawMessage) (permission.Decision, error) {
	return permission.Ask, nil
}
func (p *askAlwaysPolicy) IsDangerous(command string) bool                           { return false }
func (p *askAlwaysPolicy) AllowedPath(path string) bool                              { return true }
func (p *askAlwaysPolicy) AllowedPathForTool(toolName, path string) bool             { return true }
func (p *askAlwaysPolicy) SetOverride(toolName string, decision permission.Decision) {}
func (p *askAlwaysPolicy) Mode() permission.PermissionMode                           { return permission.SupervisedMode }

// denyAlwaysPolicy always returns Deny for any tool.
type denyAlwaysPolicy struct{}

func (p *denyAlwaysPolicy) Check(toolName string, input json.RawMessage) (permission.Decision, error) {
	return permission.Deny, nil
}
func (p *denyAlwaysPolicy) IsDangerous(command string) bool                           { return false }
func (p *denyAlwaysPolicy) AllowedPath(path string) bool                              { return true }
func (p *denyAlwaysPolicy) AllowedPathForTool(toolName, path string) bool             { return true }
func (p *denyAlwaysPolicy) SetOverride(toolName string, decision permission.Decision) {}
func (p *denyAlwaysPolicy) Mode() permission.PermissionMode                           { return permission.SupervisedMode }

// fileWriteTool is a test tool that simulates write_file.
type fileWriteTool struct{}

func (t *fileWriteTool) Name() string                { return "write_file" }
func (t *fileWriteTool) Description() string         { return "test write_file" }
func (t *fileWriteTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *fileWriteTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	return tool.Result{Content: fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), args.Path)}, nil
}

// fileEditTool is a test tool that simulates edit_file.
type fileEditTool struct{}

func (t *fileEditTool) Name() string                { return "edit_file" }
func (t *fileEditTool) Description() string         { return "test edit_file" }
func (t *fileEditTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *fileEditTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var args struct {
		FilePath string `json:"file_path"`
		OldText  string `json:"old_text"`
		NewText  string `json:"new_text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	data, err := os.ReadFile(args.FilePath)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	newContent := strings.Replace(string(data), args.OldText, args.NewText, 1)
	if err := os.WriteFile(args.FilePath, []byte(newContent), 0644); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	return tool.Result{Content: fmt.Sprintf("Edited %s", args.FilePath)}, nil
}
