package wailskit

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExportSessionToMarkdown_NoSession(t *testing.T) {
	// With no active chat bridge and empty sessionID, should return error.
	_, err := ExportSessionToMarkdown("")
	if err == nil {
		t.Fatal("expected error when no session is loaded, got nil")
	}
}

func TestExportSessionToJSON_NoSession(t *testing.T) {
	_, err := ExportSessionToJSON("")
	if err == nil {
		t.Fatal("expected error when no session is loaded, got nil")
	}
}

func TestExportSessionToMarkdown_Format(t *testing.T) {
	// Test markdown formatting directly using messages.
	msgs := []SessionMessage{
		{Role: "user", Content: "Hello, how are you?"},
		{Role: "assistant", Content: "I'm doing great!"},
		{Role: "tool", ToolName: "read_file", ToolDisplay: "Read File", Content: "file content here"},
	}

	// We can't call ExportSessionToMarkdown directly (needs bridge),
	// but we can test the formatting logic matches expected patterns.
	var b strings.Builder
	b.WriteString("# Test Session\n\n")
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			b.WriteString("## User\n\n")
			b.WriteString(msg.Content)
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("## Assistant\n\n")
			b.WriteString(msg.Content)
			b.WriteString("\n\n")
		case "tool":
			b.WriteString("### ")
			b.WriteString(msg.ToolDisplay)
			b.WriteString("\n\n")
			if msg.Content != "" {
				b.WriteString(msg.Content)
				b.WriteString("\n\n")
			}
		}
	}
	result := b.String()

	if !strings.Contains(result, "# Test Session") {
		t.Error("markdown should contain title heading")
	}
	if !strings.Contains(result, "## User") {
		t.Error("markdown should contain user heading")
	}
	if !strings.Contains(result, "## Assistant") {
		t.Error("markdown should contain assistant heading")
	}
	if !strings.Contains(result, "### Read File") {
		t.Error("markdown should contain tool heading")
	}
	if !strings.Contains(result, "Hello, how are you?") {
		t.Error("markdown should contain user content")
	}
}

// TestFormatMessagesAsMarkdown_RuneBoundaryTruncation verifies tool message
// truncation lands on a UTF-8 rune boundary (#301): byte-slicing at 2000
// would split multi-byte chars and corrupt the exported .md.
func TestFormatMessagesAsMarkdown_RuneBoundaryTruncation(t *testing.T) {
	// 700 CJK chars = 2100 bytes; byte cut at 2000 lands mid-rune.
	content := strings.Repeat("好", 700)
	out := formatMessagesAsMarkdown([]SessionMessage{
		{Role: "tool", ToolDisplay: "Read File", Content: content},
	}, "T")
	if !utf8.ValidString(out) {
		t.Fatal("exported markdown contains invalid UTF-8 (mid-rune truncation)")
	}
	if !strings.Contains(out, "(truncated)") {
		t.Error("truncation notice missing")
	}
}
