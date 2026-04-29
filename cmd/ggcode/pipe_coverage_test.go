package main

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestFilepathBase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "project"},
		{"/home/user/myproject", "myproject"},
		{"/", "/"},
		{"  /home/user/proj  ", "proj"},
	}
	for _, tt := range tests {
		got := filepathBase(tt.input)
		if got != tt.expected {
			t.Errorf("filepathBase(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFileDescriptorIsTerminal_Nil(t *testing.T) {
	if fileDescriptorIsTerminal(nil) {
		t.Error("expected false for nil")
	}
}

func TestIsInteractiveTerminal_Nil(t *testing.T) {
	if isInteractiveTerminal(nil, nil) {
		t.Error("expected false for nil readers/writers")
	}
}

func TestPipeArgString(t *testing.T) {
	if pipeArgString("hello") != "hello" {
		t.Error("expected string passthrough")
	}
	if pipeArgString(42) != "" {
		t.Error("expected empty for non-string")
	}
	if pipeArgString(nil) != "" {
		t.Error("expected empty for nil")
	}
}

func TestFormatPipeProgressEvent_ToolDone(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{Name: "edit_file"},
	})
	if got == "" {
		t.Error("expected non-empty for tool done")
	}
}

func TestFormatPipeProgressEvent_ToolDoneNoName(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{Name: ""},
	})
	if got != "" {
		t.Errorf("expected empty for no name, got %q", got)
	}
}

func TestFormatPipeProgressEvent_ToolResult(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type:   provider.StreamEventToolResult,
		Result: "file updated successfully",
	})
	if got == "" {
		t.Error("expected non-empty for tool result")
	}
}

func TestFormatPipeProgressEvent_ToolResultEmpty(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type:   provider.StreamEventToolResult,
		Result: "",
	})
	if got != "" {
		t.Errorf("expected empty for empty result, got %q", got)
	}
}

func TestFormatPipeProgressEvent_ToolResultError(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type:    provider.StreamEventToolResult,
		Result:  "",
		IsError: true,
	})
	if got != "tool result: error" {
		t.Errorf("expected 'tool result: error', got %q", got)
	}
}

func TestFormatPipeProgressEvent_ToolResultMultiline(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type:   provider.StreamEventToolResult,
		Result: "first line\nsecond line",
	})
	if got == "" {
		t.Error("expected non-empty")
	}
	// Should only contain first line
	if len(got) > 200 {
		t.Errorf("result too long: %q", got)
	}
}

func TestFormatPipeProgressEvent_Default(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
	})
	if got != "" {
		t.Errorf("expected empty for default event, got %q", got)
	}
}

func TestBuildPipePrompt_NoStdin(t *testing.T) {
	prompt, blocks := buildPipePrompt("hello", nil)
	if prompt != "hello" {
		t.Errorf("expected 'hello', got %q", prompt)
	}
	if blocks != nil {
		t.Error("expected nil blocks")
	}
}

func TestBuildPipePrompt_TextStdin(t *testing.T) {
	prompt, blocks := buildPipePrompt("summarize", []byte("some text content"))
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
	if blocks != nil {
		t.Error("expected nil blocks for text stdin")
	}
}
