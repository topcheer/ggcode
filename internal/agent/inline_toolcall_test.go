package agent

import (
	"testing"
)

func TestHasInlineToolCall(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "empty",
			text: "",
			want: false,
		},
		{
			name: "plain text",
			text: "I'll read the file for you.",
			want: false,
		},
		{
			name: "XML tool_call tag",
			text: `<tool_call>{"name":"read_file","arguments":{"path":"/tmp/test.go"}}</tool_call>`,
			want: true,
		},
		{
			name: "code block with JSON tool call",
			text: "Let me read the file:\n```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"/tmp/test.go\"}}\n```\nThen I'll fix the bug.",
			want: true,
		},
		{
			name: "plain JSON tool call",
			text: `{"name":"run_command","arguments":{"command":"ls -la"}}`,
			want: true,
		},
		{
			name: "tool/input format",
			text: `{"tool":"edit_file","input":{"path":"/tmp/test.go","old":"foo","new":"bar"}}`,
			want: true,
		},
		{
			name: "parameters instead of arguments",
			text: `{"name":"write_file","parameters":{"path":"/tmp/test.go","content":"hello"}}`,
			want: true,
		},
		{
			name: "JSON without tool fields",
			text: `{"key":"value","other":123}`,
			want: false,
		},
		{
			name: "name field but no arguments",
			text: `{"name":"read_file"}`,
			want: false,
		},
		{
			name: "tool call in reasoning text",
			text: "I need to check the file first.\n\nI'll call read_file:\n" + `{ "name": "read_file", "arguments": { "path": "/src/main.go" } }` + "\n\nAfter that I'll make the fix.",
			want: true,
		},
		{
			name: "nested JSON with tool call",
			text: `Here's my plan: {"action":"execute","tool_call":{"name":"grep","arguments":{"pattern":"TODO","path":"."}}}`,
			want: true,
		},
		{
			name: "short text with name keyword",
			text: "What is your name?",
			want: false,
		},
		{
			name: "multiple objects, one is a tool call",
			text: `{"key":"value"} and then {"name":"read_file","arguments":{"path":"/tmp"}}`,
			want: true,
		},
		{
			name: "incomplete JSON",
			text: `{"name":"read_file","arguments":`,
			want: false,
		},
		{
			name: "escaped quotes in string",
			text: `{"name":"run_command","arguments":{"command":"echo \"hello\""}}`,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasInlineToolCall(tt.text); got != tt.want {
				t.Errorf("hasInlineToolCall() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasInlineToolCall_Performance(t *testing.T) {
	// Ensure the function doesn't cause performance issues on large text inputs
	// that contain many JSON-like patterns but no actual tool calls.
	large := ""
	for i := 0; i < 1000; i++ {
		large += `{"key":"value","number":` + itoa(i) + `},`
	}
	large = `{"data":[` + large + `]}`
	if hasInlineToolCall(large) {
		t.Error("expected false for large non-tool-call JSON")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
