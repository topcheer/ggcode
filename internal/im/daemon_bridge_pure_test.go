package im

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIsDaemonSkippedTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"spawn_agent", true},
		{"wait_agent", true},
		{"list_agents", true},
		{"read_file", false},
		{"run_command", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDaemonSkippedTool(tt.name); got != tt.want {
				t.Errorf("isDaemonSkippedTool(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestContentToText(t *testing.T) {
	tests := []struct {
		name   string
		blocks []provider.ContentBlock
		want   string
	}{
		{
			name:   "empty",
			blocks: nil,
			want:   "",
		},
		{
			name:   "single block",
			blocks: []provider.ContentBlock{{Type: "text", Text: "hello"}},
			want:   "hello",
		},
		{
			name:   "multiple blocks joined",
			blocks: []provider.ContentBlock{{Type: "text", Text: "hello "}, {Type: "text", Text: "world"}},
			want:   "hello world",
		},
		{
			name:   "skips empty blocks",
			blocks: []provider.ContentBlock{{Type: "text", Text: "  "}, {Type: "text", Text: "real"}},
			want:   "real",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contentToText(tt.blocks)
			if got != tt.want {
				t.Errorf("contentToText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatToolSummary(t *testing.T) {
	// No tools
	if got := formatToolSummary("en", nil, 0, 0, 0); got != "" {
		t.Errorf("empty: %q", got)
	}

	// Single tool, no failures
	tools := []ToolResultInfo{{ToolName: "read_file"}}
	got := formatToolSummary("en", tools, 1, 1, 0)
	if !strings.Contains(got, "1 tool calls") {
		t.Errorf("en header: %q", got)
	}
	if !strings.Contains(got, "Read") {
		t.Errorf("tool name: %q", got)
	}

	// With failures
	got2 := formatToolSummary("en", tools, 3, 2, 1)
	if !strings.Contains(got2, "2 ok, 1 failed") {
		t.Errorf("with failures: %q", got2)
	}

	// Chinese
	got3 := formatToolSummary("zh-CN", tools, 1, 1, 0)
	if !strings.Contains(got3, "1 个工具调用") {
		t.Errorf("zh header: %q", got3)
	}

	// Multiple same tool -> count suffix
	tools2 := []ToolResultInfo{{ToolName: "run_command"}, {ToolName: "run_command"}, {ToolName: "read_file"}}
	got4 := formatToolSummary("en", tools2, 3, 3, 0)
	if !strings.Contains(got4, "Run ×2") {
		t.Errorf("multi count: %q", got4)
	}
}

func TestFormatToolSummaryChinese(t *testing.T) {
	tools := []ToolResultInfo{{ToolName: "read_file"}, {ToolName: "run_command"}}
	got := formatToolSummary("zh-CN", tools, 2, 2, 0)
	if !strings.Contains(got, "2 个工具调用") {
		t.Errorf("zh-CN header: %q", got)
	}
	if !strings.Contains(got, "Read") {
		t.Errorf("tool name: %q", got)
	}

	// With failures in Chinese
	got2 := formatToolSummary("zh-CN", tools, 3, 1, 2)
	if !strings.Contains(got2, "1 成功，2 失败") {
		t.Errorf("zh failures: %q", got2)
	}
}

func TestFormatIMToolDisplayName(t *testing.T) {
	// Known tool - returns short display name
	got := formatIMToolDisplayName("read_file")
	if got == "" {
		t.Error("should have a display name")
	}
	// Unknown tool - returns prettified name
	got2 := formatIMToolDisplayName("custom_tool_xyz")
	if got2 != "Custom Tool Xyz" {
		t.Errorf("unknown: %q", got2)
	}
}

func TestLocalizedThinking(t *testing.T) {
	if got := localizedThinking(ToolLangEn); got != "Thinking..." {
		t.Errorf("en: %q", got)
	}
	if got := localizedThinking(ToolLangZhCN); got != "思考中..." {
		t.Errorf("zh: %q", got)
	}
}

func TestLocalizedWriting(t *testing.T) {
	if got := localizedWriting(ToolLangEn); got != "Writing..." {
		t.Errorf("en: %q", got)
	}
	if got := localizedWriting(ToolLangZhCN); got != "输出中..." {
		t.Errorf("zh: %q", got)
	}
}

func TestLocalizedToolLabel(t *testing.T) {
	tests := []struct {
		lang   ToolLanguage
		action string
		want   string
	}{
		{ToolLangEn, "read", "Read"},
		{ToolLangEn, "edit", "Edit"},
		{ToolLangEn, "write", "Write"},
		{ToolLangEn, "search", "Search"},
		{ToolLangZhCN, "read", "读"},
		{ToolLangZhCN, "edit", "编辑"},
		{ToolLangZhCN, "write", "写"},
		{ToolLangZhCN, "search", "搜索"},
		{ToolLangEn, "unknown_action", "Unknown Action"},
	}
	for _, tt := range tests {
		t.Run(string(tt.lang)+"_"+tt.action, func(t *testing.T) {
			got := localizedToolLabel(tt.lang, tt.action)
			if got != tt.want {
				t.Errorf("localizedToolLabel(%q, %q) = %q, want %q", tt.lang, tt.action, got, tt.want)
			}
		})
	}
}

func TestLocalizedToolActivity(t *testing.T) {
	// Without target
	got := localizedToolActivity(ToolLangEn, "read", "")
	if got != "Reading file" {
		t.Errorf("en read no target: %q", got)
	}
	got2 := localizedToolActivity(ToolLangZhCN, "read", "")
	if got2 != "读取文件" {
		t.Errorf("zh read no target: %q", got2)
	}

	// With target
	got3 := localizedToolActivity(ToolLangEn, "read", "main.go")
	if !strings.Contains(got3, "main.go") {
		t.Errorf("en read with target: %q", got3)
	}

	// Unknown action
	got4 := localizedToolActivity(ToolLangEn, "unknown", "")
	if got4 == "" {
		t.Error("unknown action should not be empty")
	}
}

func TestIsTrivialToolDetail(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", true},
		{"{}", true},
		{"[]", true},
		{"null", true},
		{"  {}  ", true},
		{"hello", false},
		{"{key: val}", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := isTrivialToolDetail(tt.value); got != tt.want {
				t.Errorf("isTrivialToolDetail(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestCompactSingleLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello\nworld", "hello world"},
		{"hello\r\nworld", "hello world"},
		{"hello\t\tworld", "hello world"},
		{"  hello   world  ", "hello world"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := compactSingleLine(tt.input); got != tt.want {
				t.Errorf("compactSingleLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     string
		want     string
	}{
		{
			name:     "read_file",
			toolName: "read_file",
			args:     `{"file_path":"/tmp/main.go"}`,
			want:     "/tmp/main.go",
		},
		{
			name:     "edit_file path",
			toolName: "edit_file",
			args:     `{"path":"/tmp/edit.go"}`,
			want:     "/tmp/edit.go",
		},
		{
			name:     "write_file",
			toolName: "write_file",
			args:     `{"file_path":"/tmp/new.go"}`,
			want:     "/tmp/new.go",
		},
		{
			name:     "glob",
			toolName: "glob",
			args:     `{"pattern":"**/*.go"}`,
			want:     "**/*.go",
		},
		{
			name:     "search_files",
			toolName: "search_files",
			args:     `{"pattern":"TODO","directory":"/tmp"}`,
			want:     "/tmp",
		},
		{
			name:     "list_directory",
			toolName: "list_directory",
			args:     `{"path":"/tmp"}`,
			want:     "/tmp",
		},
		{
			name:     "invalid json",
			toolName: "read_file",
			args:     "not json",
			want:     "",
		},
		{
			name:     "unknown tool",
			toolName: "custom_tool",
			args:     `{"path":"/tmp/custom"}`,
			want:     "/tmp/custom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFilePath(tt.toolName, tt.args)
			if got != tt.want {
				t.Errorf("extractFilePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDisplayToolFileTarget(t *testing.T) {
	// Empty input
	if got := displayToolFileTarget(""); got != "" {
		t.Errorf("empty: %q", got)
	}
	// Just slashes
	if got := displayToolFileTarget("///"); got != "" {
		t.Errorf("slashes: %q", got)
	}
	// Relative path
	if got := displayToolFileTarget("main.go"); got != "main.go" {
		t.Errorf("relative: %q", got)
	}
}

func TestNormalizeDisplayPath(t *testing.T) {
	got := normalizeDisplayPath("/very/long/path/to/some/file.go")
	if got == "" {
		t.Error("should return non-empty path")
	}
	// Should contain the filename at least
	if !strings.Contains(got, "file.go") {
		t.Errorf("should contain filename: %q", got)
	}
}
