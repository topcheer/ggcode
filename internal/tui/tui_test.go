package tui

import (
	"testing"
)

func TestRenderMarkdown(t *testing.T) {
	// Should not panic and return non-empty string
	result := RenderMarkdown("# Hello\n\nWorld")
	if result == "" {
		t.Error("expected non-empty markdown output")
	}
}

func TestRenderMarkdown_PlainText(t *testing.T) {
	result := RenderMarkdown("just plain text")
	if result == "" {
		t.Error("expected non-empty output")
	}
}

func TestFormatDiff(t *testing.T) {
	diff := `@@ -1,3 +1,4 @@
 line1
-line2
+line2_modified
+new line
 line3`
	result := FormatDiff(diff)
	if result == "" {
		t.Error("expected non-empty diff output")
	}
}

func TestFormatDiff_Empty(t *testing.T) {
	result := FormatDiff("")
	if result != "" {
		t.Error("expected empty string for empty diff")
	}
}

func TestIsDiffContent(t *testing.T) {
	if !IsDiffContent("@@ -1,3 +1,4 @@") {
		t.Error("expected true for diff hunk header")
	}
	if IsDiffContent("just some text\nwith no diff") {
		t.Error("expected false for non-diff text")
	}
}

func TestFormatToolStatus(t *testing.T) {
	msg := ToolStatusMsg{ToolName: "read_file", Running: false, Result: "file content"}
	result := FormatToolStatus(msg)
	if result == "" {
		t.Error("expected non-empty tool status")
	}
}

func TestFormatToolStatus_Error(t *testing.T) {
	msg := ToolStatusMsg{ToolName: "run_command", Running: false, Result: "exit code 1", IsError: true}
	result := FormatToolStatus(msg)
	if result == "" {
		t.Error("expected non-empty error status")
	}
}

func TestHelpText(t *testing.T) {
	h := helpText()
	if h == "" {
		t.Error("expected non-empty help text")
	}
}
