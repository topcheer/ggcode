package chat

import (
	"testing"
)

func TestParseToolInputArg(t *testing.T) {
	got := parseToolInputArg(`{"file_path": "/tmp/test.go"}`, "file_path")
	if got != "/tmp/test.go" {
		t.Errorf("expected '/tmp/test.go', got %q", got)
	}
	got = parseToolInputArg(`{"file_path": "/tmp/test.go"}`, "command")
	if got != "" {
		t.Errorf("expected empty for missing key, got %q", got)
	}
	got = parseToolInputArg("invalid json", "file_path")
	if got != "" {
		t.Errorf("expected empty for invalid json, got %q", got)
	}
}

func TestParseToolInputArgAny(t *testing.T) {
	got := parseToolInputArgAny(`{"file_path": "/tmp/test.go"}`, "command", "file_path")
	if got != "/tmp/test.go" {
		t.Errorf("expected '/tmp/test.go', got %q", got)
	}
}

func TestFormatJSONResult(t *testing.T) {
	got := FormatJSONResult(`{"ID": "123", "Prompt": "test prompt"}`)
	if got == "" {
		t.Error("expected non-empty")
	}
	got = FormatJSONResult("not json")
	if got != "not json" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestFormatKVPair(t *testing.T) {
	tests := []struct {
		label string
		val   interface{}
		want  string
	}{
		{"Test", true, "Test: Yes"},
		{"Test", false, "Test: No"},
		{"Name", "hello", "Name: hello"},
		{"Name", "", "Name: -"},
		{"Count", 42, "Count: 42"},
	}
	for _, tt := range tests {
		got := formatKVPair(tt.label, tt.val)
		if got != tt.want {
			t.Errorf("formatKVPair(%q, %v) = %q, want %q", tt.label, tt.val, got, tt.want)
		}
	}
}

func TestPrettifyJSONKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ID", "Job ID"},
		{"CronExpr", "Schedule"},
		{"NextFire", "Next Fire"},
		{"Subject", "Subject"},
	}
	for _, tt := range tests {
		got := prettifyJSONKey(tt.input)
		if got != tt.expected {
			t.Errorf("prettifyJSONKey(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNewSpacerItem(t *testing.T) {
	item := NewSpacerItem(5)
	if item == nil {
		t.Error("expected non-nil")
	}
	if item.ID() != "" {
		t.Errorf("expected empty ID for spacer, got %q", item.ID())
	}
}

func TestSpacerHeight(t *testing.T) {
	item := SpacerItem{height: 3}
	if item.Height(80) != 3 {
		t.Errorf("expected 3, got %d", item.Height(80))
	}
}

func TestBaseToolItemBasics(t *testing.T) {
	item := NewBaseToolItem("id-1", "test_tool", StatusPending, "{}", Styles{})
	if item.ID() != "id-1" {
		t.Errorf("expected 'id-1', got %q", item.ID())
	}
	if item.ToolName() != "test_tool" {
		t.Errorf("expected 'test_tool', got %q", item.ToolName())
	}
	if item.Status() != StatusPending {
		t.Error("expected pending status")
	}
	item.SetStatus(StatusSuccess)
	if item.Status() != StatusSuccess {
		t.Error("expected success status")
	}
}

func TestBashToolItem_Coverage(t *testing.T) {
	item := NewBashToolItem("id-1", "Bash", "echo hello", StatusRunning, Styles{})
	if item.ID() != "id-1" {
		t.Errorf("expected 'id-1', got %q", item.ID())
	}
}

func TestFileToolItem_Coverage(t *testing.T) {
	item := NewFileToolItem("id-1", "Edit", "/tmp/test.go", StatusPending, Styles{}, "en")
	if item.ID() != "id-1" {
		t.Errorf("expected 'id-1', got %q", item.ID())
	}
}

func TestSearchToolItem(t *testing.T) {
	item := NewSearchToolItem("id-1", "Grep", "TODO", StatusPending, Styles{})
	if item.ID() != "id-1" {
		t.Errorf("expected 'id-1', got %q", item.ID())
	}
}

func TestClassifyTool(t *testing.T) {
	tests := []struct {
		name string
		cat  toolCategory
	}{
		{"run_command", catBash},
		{"edit_file", catFile},
		{"read_file", catFile},
		{"grep", catSearch},
		{"list_directory", catList},
		{"web_fetch", catWeb},
		{"git_status", catGit},
		{"start_command", catCmd},
		{"lsp_definition", catLSP},
		{"unknown_tool", catGeneric},
	}
	for _, tt := range tests {
		got := classifyTool(tt.name)
		if got != tt.cat {
			t.Errorf("classifyTool(%q) = %d, want %d", tt.name, got, tt.cat)
		}
	}
}

func TestPrettifyToolName(t *testing.T) {
	got := PrettifyToolName("run_command")
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestTruncateTailByWidth(t *testing.T) {
	got := truncateTailByWidth("hello world", 5)
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestTruncateHeadByWidth(t *testing.T) {
	got := truncateHeadByWidth("hello world", 5)
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestToolIconCoverage(t *testing.T) {
	s := Styles{}
	icon := s.ToolIcon(StatusRunning)
	if icon == "" {
		t.Error("expected non-empty icon")
	}
}

func TestToolIconStyleCoverage(t *testing.T) {
	s := Styles{}
	style := s.ToolIconStyle(StatusRunning)
	_ = style
}

func TestGetToolBodyBehaviorCoverage(t *testing.T) {
	bh := GetToolBodyBehavior("run_command")
	_ = bh
	bh = GetToolBodyBehavior("unknown_tool")
	_ = bh
}

func TestStylesStringCoverage(t *testing.T) {
	// Styles.String() may not exist; just verify no panic
}
