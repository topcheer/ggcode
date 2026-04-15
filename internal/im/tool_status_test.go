package im

import (
	"testing"
)

func TestDescribeTool(t *testing.T) {
	tests := []struct {
		name         string
		lang         ToolLanguage
		toolName     string
		rawArgs      string
		wantDisplay  string
		wantActivity string
	}{
		{
			name:         "read_file_zh",
			lang:         ToolLangZhCN,
			toolName:     "read_file",
			rawArgs:      `{"file_path": "/tmp/test.go"}`,
			wantDisplay:  "读",
			wantActivity: "读取 test.go",
		},
		{
			name:         "read_file_en",
			lang:         ToolLangEn,
			toolName:     "read_file",
			rawArgs:      `{"file_path": "/tmp/test.go"}`,
			wantDisplay:  "Read",
			wantActivity: "Reading test.go",
		},
		{
			name:         "write_file_zh",
			lang:         ToolLangZhCN,
			toolName:     "write_file",
			rawArgs:      `{"file_path": "/tmp/chart.html"}`,
			wantDisplay:  "写",
			wantActivity: "写入 chart.html",
		},
		{
			name:         "run_command_zh",
			lang:         ToolLangZhCN,
			toolName:     "run_command",
			rawArgs:      `{"command": "open chart.html"}`,
			wantDisplay:  "执行",
			wantActivity: "执行 open chart.html",
		},
		{
			name:         "glob_zh",
			lang:         ToolLangZhCN,
			toolName:     "glob",
			rawArgs:      `{"pattern": "*.go"}`,
			wantDisplay:  "查找",
			wantActivity: "查找 *.go",
		},
		{
			name:         "grep_zh",
			lang:         ToolLangZhCN,
			toolName:     "grep",
			rawArgs:      `{"pattern": "TODO"}`,
			wantDisplay:  "搜索",
			wantActivity: "搜索 TODO",
		},
		{
			name:         "web_search_zh",
			lang:         ToolLangZhCN,
			toolName:     "web_search",
			rawArgs:      `{"query": "golang testing"}`,
			wantDisplay:  "搜索",
			wantActivity: "搜索 golang testing",
		},
		{
			name:         "unknown_tool",
			lang:         ToolLangZhCN,
			toolName:     "custom_tool",
			rawArgs:      `{}`,
			wantDisplay:  "Custom Tool",
			wantActivity: "运行 Custom Tool",
		},
		{
			name:         "edit_file_create",
			lang:         ToolLangZhCN,
			toolName:     "edit_file",
			rawArgs:      `{"file_path": "/tmp/new.go"}`,
			wantDisplay:  "创建",
			wantActivity: "创建 new.go",
		},
		{
			name:         "edit_file_modify",
			lang:         ToolLangZhCN,
			toolName:     "edit_file",
			rawArgs:      `{"file_path": "/tmp/existing.go", "old_text": "foo"}`,
			wantDisplay:  "编辑",
			wantActivity: "编辑 existing.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeTool(tt.lang, tt.toolName, tt.rawArgs)
			if got.DisplayName != tt.wantDisplay {
				t.Errorf("DisplayName = %q, want %q", got.DisplayName, tt.wantDisplay)
			}
			if got.Activity != tt.wantActivity {
				t.Errorf("Activity = %q, want %q", got.Activity, tt.wantActivity)
			}
		})
	}
}

func TestFormatToolInline(t *testing.T) {
	tests := []struct {
		name   string
		name_  string
		detail string
		want   string
	}{
		{"with_detail", "读", "chart.html", "读 chart.html"},
		{"empty_detail", "读", "", "读"},
		{"trivial_detail", "读", "{}", "读"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatToolInline(tt.name_, tt.detail)
			if got != tt.want {
				t.Errorf("FormatToolInline(%q, %q) = %q, want %q", tt.name_, tt.detail, got, tt.want)
			}
		})
	}
}

func TestFormatIMStatus(t *testing.T) {
	tests := []struct {
		name     string
		lang     ToolLanguage
		activity string
		toolName string
		toolArg  string
		want     string
	}{
		{
			name:     "tool_with_detail_zh",
			lang:     ToolLangZhCN,
			activity: "思考中...",
			toolName: "读",
			toolArg:  "chart.html",
			want:     "正在读 chart.html...",
		},
		{
			name:     "tool_with_detail_en",
			lang:     ToolLangEn,
			activity: "Thinking...",
			toolName: "Read",
			toolArg:  "chart.html",
			want:     "Working on Read chart.html...",
		},
		{
			name:     "thinking_no_tool_zh",
			lang:     ToolLangZhCN,
			activity: "思考中...",
			toolName: "",
			toolArg:  "",
			want:     "", // TUI returns empty when no tool summary and activity is thinking/writing
		},
		{
			name:     "activity_only_zh",
			lang:     ToolLangZhCN,
			activity: "读取文件",
			toolName: "",
			toolArg:  "",
			want:     "正在读取文件...",
		},
		{
			name:     "empty",
			lang:     ToolLangZhCN,
			activity: "",
			toolName: "",
			toolArg:  "",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatIMStatus(tt.lang, tt.activity, tt.toolName, tt.toolArg)
			if got != tt.want {
				t.Errorf("FormatIMStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalizeIMProgress(t *testing.T) {
	tests := []struct {
		lang ToolLanguage
		text string
		want string
	}{
		{ToolLangZhCN, "思考中...", "我先想一下..."},
		{ToolLangZhCN, "输出中...", "我整理一下结果..."},
		{ToolLangZhCN, "读取文件", "正在读取文件..."},
		{ToolLangZhCN, "正在执行", "正在执行"},
		{ToolLangZhCN, "我需要思考", "我需要思考"},
		{ToolLangEn, "Thinking...", "Let me think..."},
		{ToolLangEn, "Writing...", "I'm drafting the answer..."},
		{ToolLangEn, "Reading file", "Working on Reading file..."},
		{ToolLangEn, "I'm doing stuff", "I'm doing stuff"},
		{ToolLangEn, "Let me check", "Let me check"},
		{ToolLangZhCN, "", ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.lang)+"_"+tt.text, func(t *testing.T) {
			got := LocalizeIMProgress(tt.lang, tt.text)
			if got != tt.want {
				t.Errorf("LocalizeIMProgress(%s, %q) = %q, want %q", tt.lang, tt.text, got, tt.want)
			}
		})
	}
}

func TestDescribeToolPipeline(t *testing.T) {
	// Integration test: DescribeTool → FormatIMStatus mirrors the TUI pipeline
	// for a common scenario: write_file chart.html
	present := DescribeTool(ToolLangZhCN, "write_file", `{"file_path": "/tmp/chart.html", "content": "<html>"}`)
	status := FormatIMStatus(ToolLangZhCN, present.Activity, present.DisplayName, present.Detail)

	if status == "" {
		t.Error("expected non-empty status for write_file")
	}
	t.Logf("write_file → status: %q (display=%q detail=%q activity=%q)", status, present.DisplayName, present.Detail, present.Activity)

	// Expected: "正在写入 chart.html..." (localizeIMProgress("写入 chart.html"))
	if status != "正在写入 chart.html..." {
		t.Errorf("expected '正在写入 chart.html...', got %q", status)
	}
}
