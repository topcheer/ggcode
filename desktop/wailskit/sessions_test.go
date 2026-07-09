//go:build goolm

package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestShouldSkipHistoryTool(t *testing.T) {
	skip := []string{
		"read_command_output", "wait_command", "stop_command",
		"write_command_input", "list_commands",
		"enter_plan_mode",
		"lsp_definition", "lsp_references", "lsp_hover",
	}
	for _, name := range skip {
		if !shouldSkipHistoryTool(name) {
			t.Errorf("expected %s to be skipped", name)
		}
	}

	keep := []string{
		"read_file", "edit_file", "write_file", "run_command",
		"git_status", "grep", "web_fetch", "start_command",
		"exit_plan_mode", // handled specially, not skipped
	}
	for _, name := range keep {
		if shouldSkipHistoryTool(name) {
			t.Errorf("expected %s to NOT be skipped", name)
		}
	}
}

func TestBuildSessionHistory_TextBlocks(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}},
	}
	history := buildSessionHistoryFromMessages(msgs)
	if len(history) != 2 {
		t.Fatalf("expected 2, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Fatalf("unexpected first: %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "world" {
		t.Fatalf("unexpected second: %+v", history[1])
	}
}

func TestBuildSessionHistory_EmptyTextSkipped(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "   "}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "real content"}}},
	}
	history := buildSessionHistoryFromMessages(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 (empty skipped), got %d", len(history))
	}
	if history[0].Content != "real content" {
		t.Fatalf("unexpected content: %q", history[0].Content)
	}
}

func TestBuildSessionHistory_ToolUseToolResultPairing(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "read_file", Input: []byte(`{"path":"/tmp/a.txt"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", Output: "file contents here"},
		}},
	}
	history := buildSessionHistoryFromMessages(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 tool entry, got %d: %+v", len(history), history)
	}
	if history[0].Role != "tool" || history[0].ToolID != "t1" {
		t.Fatalf("unexpected tool entry: %+v", history[0])
	}
	if history[0].Content != "file contents here" {
		t.Fatalf("expected result content, got %q", history[0].Content)
	}
	if history[0].ToolName != "read_file" {
		t.Fatalf("expected tool name read_file, got %q", history[0].ToolName)
	}
}

func TestBuildSessionHistory_ExitPlanModeRendersAsPlan(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "p1", ToolName: "exit_plan_mode", Input: []byte(`{"plan":"Here is my plan\n1. Do thing"}`)},
		}},
	}
	history := buildSessionHistoryFromMessages(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
	if history[0].Role != "assistant" {
		t.Fatalf("expected assistant role, got %s", history[0].Role)
	}
	if history[0].Content != "Here is my plan\n1. Do thing" {
		t.Fatalf("unexpected plan content: %q", history[0].Content)
	}
}

func TestBuildSessionHistory_SkippedToolsOmitted(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "l1", ToolName: "lsp_definition", Input: []byte(`{"path":"a.go"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "l1", Output: "def"},
		}},
	}
	history := buildSessionHistoryFromMessages(msgs)
	if len(history) != 0 {
		t.Fatalf("expected 0 entries (lsp skipped), got %d: %+v", len(history), history)
	}
}

func TestBuildSessionHistory_ToolResultWithError(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "e1", ToolName: "run_command", Input: []byte(`{"command":"ls"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "e1", Output: "command not found", IsError: true},
		}},
	}
	history := buildSessionHistoryFromMessages(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1, got %d", len(history))
	}
	if !history[0].IsError {
		t.Fatal("expected IsError=true")
	}
}

func TestBuildSessionHistory_SystemMessagesSkipped(t *testing.T) {
	msgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "system msg"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}
	history := buildSessionHistoryFromMessages(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 (system skipped), got %d", len(history))
	}
	if history[0].Role != "user" {
		t.Fatalf("expected user, got %s", history[0].Role)
	}
}
