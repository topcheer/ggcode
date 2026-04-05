package tui

import (
	"strings"
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

func TestFormatToolStatus_HidesReadFileBody(t *testing.T) {
	msg := ToolStatusMsg{
		ToolName: "read_file",
		Running:  false,
		Result:   "package main\n\nfunc main() {}\n",
	}
	result := FormatToolStatus(msg)
	if strings.Contains(result, "package main") {
		t.Error("expected read_file body to be hidden from TUI output")
	}
	if !strings.Contains(result, "lines of content") {
		t.Error("expected read_file summary in TUI output")
	}
}

func TestFormatToolStatus_RunCommandErrorShowsOnlyExitStatus(t *testing.T) {
	msg := ToolStatusMsg{
		ToolName: "run_command",
		Running:  false,
		Result:   "STDERR:\nboom\nCommand failed: exit status 2",
		IsError:  true,
	}
	result := FormatToolStatus(msg)
	if strings.Contains(result, "boom") {
		t.Error("expected stderr body to be hidden from TUI output")
	}
	if !strings.Contains(result, "exit status 2") {
		t.Error("expected exit status summary in TUI output")
	}
}

func TestHelpText(t *testing.T) {
	h := newTestModel().helpText()
	if h == "" {
		t.Error("expected non-empty help text")
	}
	if !strings.Contains(h, "/help, /?") {
		t.Error("expected /? alias in help text")
	}
}
