package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
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
	msg := ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: false, Result: "file content"}
	result := FormatToolStatus(msg)
	if result == "" {
		t.Error("expected non-empty tool status")
	}
}

func TestFormatToolStatus_Error(t *testing.T) {
	msg := ToolStatusMsg{ToolName: "run_command", DisplayName: "Run", Detail: "go test ./...", Running: false, Result: "exit code 1", IsError: true}
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

func TestDescribeToolReadFile(t *testing.T) {
	present := describeTool(LangEnglish, "read_file", `{"path":"docs/guide.md"}`)
	if present.DisplayName != "Read" {
		t.Fatalf("expected friendly display name, got %q", present.DisplayName)
	}
	if present.Detail != "docs/guide.md" {
		t.Fatalf("expected file detail, got %q", present.Detail)
	}
	if present.Activity != "Reading docs/guide.md" {
		t.Fatalf("expected reading activity, got %q", present.Activity)
	}
}

func TestDescribeToolWriteFileUsesWorkspaceRelativePath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "internal", "context"), 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()

	target := filepath.Join(workspace, "internal", "context", "manager.go")
	present := describeTool(LangEnglish, "write_file", `{"file_path":"`+target+`","content":"x"}`)
	if present.Detail != "internal/context/manager.go" {
		t.Fatalf("expected workspace-relative detail, got %q", present.Detail)
	}
	if present.Activity != "Writing internal/context/manager.go" {
		t.Fatalf("expected workspace-relative path in activity, got %q", present.Activity)
	}
}

func TestDescribeToolSearchLocalized(t *testing.T) {
	present := describeTool(LangZhCN, "grep", `{"pattern":"ContextManager","path":"internal/context"}`)
	if present.DisplayName != "搜索" {
		t.Fatalf("expected localized display name, got %q", present.DisplayName)
	}
	if present.Detail != "ContextManager" {
		t.Fatalf("expected search detail, got %q", present.Detail)
	}
	if present.Activity != "搜索 ContextManager" {
		t.Fatalf("expected localized activity, got %q", present.Activity)
	}
}

func TestDescribeToolListDirectoryUsesWorkspaceRelativePath(t *testing.T) {
	present := describeTool(LangEnglish, "list_directory", `{"path":"internal/context"}`)
	if present.Detail != "internal/context" {
		t.Fatalf("expected workspace-relative directory, got %q", present.Detail)
	}
	if present.Activity != "Listing internal/context" {
		t.Fatalf("expected workspace-relative listing activity, got %q", present.Activity)
	}
}

func TestFormatToolStartUsesFriendlyDisplay(t *testing.T) {
	result := FormatToolStart(ToolStatusMsg{
		ToolName:    "read_file",
		DisplayName: "Read",
		Detail:      "README.md",
		Running:     true,
	})
	if strings.Contains(result, "read_file") {
		t.Fatalf("expected raw tool name to be hidden, got %q", result)
	}
	if !strings.Contains(result, "Read README.md") {
		t.Fatalf("expected friendly inline label, got %q", result)
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
	if strings.Contains(h, "/cost") {
		t.Error("expected cost command to be removed from help text")
	}
}

func TestProviderCommandOpensProviderPanel(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())

	cmd := m.handleCommand("/provider")
	if cmd != nil {
		t.Fatal("expected /provider to open inline panel without async command")
	}
	if m.providerPanel == nil {
		t.Fatal("expected provider panel to be open")
	}
}
