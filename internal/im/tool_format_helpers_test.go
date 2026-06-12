package im

import (
	"strings"
	"testing"
)

func TestFormatIMStopCommandResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "not found"},
			want: "停止命令",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "命令已停止",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "signal killed"},
			want: "signal killed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMStopCommandResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMStopCommandResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMReadCmdOutputResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "job not found"},
			want: "读取输出",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "无新输出",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "hello world"},
			want: "hello world",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMReadCmdOutputResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMReadCmdOutputResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMWaitCommandResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "timeout"},
			want: "等待命令",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "命令完成",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "build complete"},
			want: "build complete",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMWaitCommandResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMWaitCommandResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMWriteCmdInputResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "closed"},
			want: "输入发送",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "输入已发送",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "accepted"},
			want: "accepted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMWriteCmdInputResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMWriteCmdInputResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMListCommandsResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "fail"},
			want: "活动命令",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "无活动命令",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "job-1 running"},
			want: "job-1 running",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMListCommandsResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMListCommandsResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMWaitAgentResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "agent crashed"},
			want: "子任务",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "子任务完成",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "result: done"},
			want: "result: done",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMWaitAgentResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMWaitAgentResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMListAgentsResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "fail"},
			want: "子任务列表",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "无活动子任务",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "agent-1 running"},
			want: "agent-1 running",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMListAgentsResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMListAgentsResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMMCPCapabilitiesResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "no servers"},
			want: "MCP",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "MCP",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "server1: running"},
			want: "server1: running",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMMCPCapabilitiesResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMMCPCapabilitiesResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMMCPPromptResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "not found"},
			want: "MCP Prompt",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "MCP Prompt",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "prompt text here"},
			want: "prompt text here",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMMCPPromptResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMMCPPromptResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMMCPResourceResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "denied"},
			want: "资源读取",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "资源内容",
		},
		{
			name: "success with output",
			tr:   &ToolResultInfo{IsError: false, Result: "file content"},
			want: "file content",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMMCPResourceResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMMCPResourceResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMCronCreateResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "invalid"},
			want: "Cron",
		},
		{
			name: "valid JSON",
			tr:   &ToolResultInfo{IsError: false, Result: `{"ID":"c1","CronExpr":"*/5 * * * *","Prompt":"hello world","Recurring":true}`},
			want: "*/5 * * * *",
		},
		{
			name: "invalid JSON success",
			tr:   &ToolResultInfo{IsError: false, Result: "ok"},
			want: "Cron job created",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMCronCreateResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMCronCreateResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMCronDeleteResult(t *testing.T) {
	if got := formatIMCronDeleteResult(&ToolResultInfo{IsError: true, Result: "not found"}); !strings.Contains(got, "Cron") {
		t.Errorf("error case: %q", got)
	}
	if got := formatIMCronDeleteResult(&ToolResultInfo{IsError: false}); !strings.Contains(got, "Cron job deleted") {
		t.Errorf("success case: %q", got)
	}
}

func TestFormatIMCronListResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "fail"},
			want: "Cron",
		},
		{
			name: "empty success",
			tr:   &ToolResultInfo{IsError: false, Result: ""},
			want: "No scheduled cron jobs",
		},
		{
			name: "no jobs message",
			tr:   &ToolResultInfo{IsError: false, Result: "No scheduled jobs found"},
			want: "No scheduled cron jobs",
		},
		{
			name: "jobs listed",
			tr:   &ToolResultInfo{IsError: false, Result: "cron-1 */5 * * * * hello"},
			want: "cron-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMCronListResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMCronListResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMTeammateResultsResult(t *testing.T) {
	if got := formatIMTeammateResultsResult(&ToolResultInfo{IsError: true, Result: "fail"}); !strings.Contains(got, "fail") {
		t.Errorf("error case: %q", got)
	}
	if got := formatIMTeammateResultsResult(&ToolResultInfo{IsError: false, Result: ""}); got == "" {
		t.Errorf("empty case should not be empty: %q", got)
	}
	if got := formatIMTeammateResultsResult(&ToolResultInfo{IsError: false, Result: "result text"}); !strings.Contains(got, "result text") {
		t.Errorf("success case: %q", got)
	}
}

func TestFormatIMWorktreeResult(t *testing.T) {
	if got := formatIMWorktreeResult("🌲", &ToolResultInfo{IsError: true, Result: "fail"}); !strings.Contains(got, "Worktree") {
		t.Errorf("error case: %q", got)
	}
	if got := formatIMWorktreeResult("🌲", &ToolResultInfo{IsError: false, Result: ""}); !strings.Contains(got, "Worktree") {
		t.Errorf("empty case: %q", got)
	}
	if got := formatIMWorktreeResult("🌲", &ToolResultInfo{IsError: false, Result: "/tmp/worktree-1"}); !strings.Contains(got, "/tmp/worktree-1") {
		t.Errorf("success case: %q", got)
	}
}

func TestFormatIMErrorResult(t *testing.T) {
	tr := &ToolResultInfo{ToolName: "read_file", Result: "permission denied"}
	got := formatIMErrorResult(tr)
	if !strings.Contains(got, "Read File") || !strings.Contains(got, "permission denied") {
		t.Errorf("formatIMErrorResult() = %q", got)
	}

	tr2 := &ToolResultInfo{ToolName: "run_command", Result: ""}
	got2 := formatIMErrorResult(tr2)
	if !strings.Contains(got2, "Run Command") {
		t.Errorf("formatIMErrorResult empty output: %q", got2)
	}
}

func TestFormatIMWebFetchResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "timeout", Args: `{"url":"https://example.com"}`},
			want: "example.com",
		},
		{
			name: "success with url",
			tr:   &ToolResultInfo{IsError: false, Args: `{"url":"https://example.com"}`},
			want: "example.com",
		},
		{
			name: "success no url",
			tr:   &ToolResultInfo{IsError: false, Detail: "https://example.com"},
			want: "example.com",
		},
		{
			name: "success empty",
			tr:   &ToolResultInfo{IsError: false},
			want: "Fetch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMWebFetchResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMWebFetchResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMWebSearchResult(t *testing.T) {
	tests := []struct {
		name string
		tr   *ToolResultInfo
		want string
	}{
		{
			name: "error case",
			tr:   &ToolResultInfo{IsError: true, Result: "api error", Args: `{"query":"golang testing"}`},
			want: "golang testing",
		},
		{
			name: "success with query",
			tr:   &ToolResultInfo{IsError: false, Args: `{"query":"golang testing"}`},
			want: "golang testing",
		},
		{
			name: "success empty",
			tr:   &ToolResultInfo{IsError: false},
			want: "Search",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMWebSearchResult(tt.tr)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatIMWebSearchResult() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMGitResult(t *testing.T) {
	tests := []struct {
		name     string
		tr       *ToolResultInfo
		contains string
	}{
		{
			name:     "git_status clean",
			tr:       &ToolResultInfo{ToolName: "git_status", Result: ""},
			contains: "clean",
		},
		{
			name:     "git_status modified",
			tr:       &ToolResultInfo{ToolName: "git_status", Result: "M  main.go\nA  new.go\nD  old.go\n?? untracked.go"},
			contains: "modified",
		},
		{
			name:     "git_diff",
			tr:       &ToolResultInfo{ToolName: "git_diff", Result: "+added line\n-deleted line\n+++file\n---file"},
			contains: "Git Diff",
		},
		{
			name:     "git_log",
			tr:       &ToolResultInfo{ToolName: "git_log", Result: "abc123 fix bug\ndef456 add feature\nghi789 cleanup\njkl012 docs"},
			contains: "abc123",
		},
		{
			name:     "git error",
			tr:       &ToolResultInfo{ToolName: "git_status", IsError: true, Result: "not a repo"},
			contains: "not a repo",
		},
		{
			name:     "unknown git tool",
			tr:       &ToolResultInfo{ToolName: "git_stash", Result: ""},
			contains: "Git Stash",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMGitResult(tt.tr)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("formatIMGitResult() = %q, want to contain %q", got, tt.contains)
			}
		})
	}
}

func TestFormatIMMCPToolResult(t *testing.T) {
	got := formatIMMCPToolResult(&ToolResultInfo{ToolName: "mcp_search", Args: `{"query":"test"}`})
	if !strings.Contains(got, "Mcp Search") {
		t.Errorf("formatIMMCPToolResult() = %q", got)
	}
	got2 := formatIMMCPToolResult(&ToolResultInfo{ToolName: "mcp_run", Args: ""})
	if !strings.Contains(got2, "Mcp Run") {
		t.Errorf("formatIMMCPToolResult empty args: %q", got2)
	}
}

func TestSummarizeIMResult(t *testing.T) {
	tests := []struct {
		name   string
		result string
		maxLen int
		want   string
	}{
		{name: "empty", result: "", maxLen: 50, want: ""},
		{name: "first line", result: "hello\nworld", maxLen: 50, want: "hello"},
		{name: "truncate", result: strings.Repeat("a", 100), maxLen: 10, want: strings.Repeat("a", 7) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeIMResult(tt.result, tt.maxLen)
			if got != tt.want {
				t.Errorf("summarizeIMResult() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSummarizeMCPArgs(t *testing.T) {
	tests := []struct {
		name   string
		args   string
		maxLen int
		want   string
	}{
		{name: "empty", args: "", maxLen: 50, want: ""},
		{name: "invalid json", args: "not json", maxLen: 50, want: ""},
		{name: "with string value", args: `{"query":"find files"}`, maxLen: 50, want: "find files"},
		{name: "skip context", args: `{"context":"system prompt"}`, maxLen: 50, want: ""},
		{name: "truncate", args: `{"query":"` + strings.Repeat("x", 100) + `"}`, maxLen: 10, want: strings.Repeat("x", 7) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeMCPArgs(tt.args, tt.maxLen)
			if got != tt.want {
				t.Errorf("summarizeMCPArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCountDiffLines(t *testing.T) {
	added, deleted := countDiffLines("+added line\n-deleted line\n+++ file\n--- file\n+another")
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
}

func TestFormatIMGitStatusSummary(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "clean",
			input:  "",
			expect: "clean",
		},
		{
			name:   "modified and added",
			input:  "M  main.go\nA  new.go",
			expect: "1 modified, 1 added",
		},
		{
			name:   "untracked",
			input:  "?? untracked.go",
			expect: "1 untracked",
		},
		{
			name:   "deleted",
			input:  " D removed.go",
			expect: "1 deleted",
		},
		{
			name:   "mixed",
			input:  "M  a.go\nA  b.go\n?? c.go\n D d.go",
			expect: "1 modified, 1 added, 1 deleted, 1 untracked",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIMGitStatusSummary(tt.input)
			if got != tt.expect {
				t.Errorf("formatIMGitStatusSummary() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestFormatIMGitLogSummary(t *testing.T) {
	input := "abc123 fix bug\ndef456 add feature\nghi789 cleanup\njkl012 docs"
	got := formatIMGitLogSummary(input)
	lines := strings.Count(got, "\n") + 1
	if lines != 3 {
		t.Errorf("expected 3 lines, got %d: %q", lines, got)
	}

	got2 := formatIMGitLogSummary("")
	if got2 != "" {
		t.Errorf("empty input: %q", got2)
	}
}

func TestImPagesSummary(t *testing.T) {
	got := imPagesSummary(ToolLangEn, 5)
	if got != "5 pages" {
		t.Errorf("imPagesSummary en = %q", got)
	}
}

func TestImIsArchiveExt(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{".zip", true},
		{".tar", true},
		{".tar.gz", true},
		{".tgz", true},
		{".tar.bz2", true},
		{".tar.xz", true},
		{".pdf", false},
		{".go", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			if got := imIsArchiveExt(tt.ext); got != tt.want {
				t.Errorf("imIsArchiveExt(%q) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}

func TestTruncateEmitter(t *testing.T) {
	if got := truncateEmitter("short", 10); got != "short" {
		t.Errorf("short string: %q", got)
	}
	if got := truncateEmitter(strings.Repeat("x", 20), 10); len(got) != 10 {
		t.Errorf("truncated len: %d", len(got))
	}
	if !strings.HasSuffix(truncateEmitter(strings.Repeat("x", 20), 10), "...") {
		t.Error("should end with ...")
	}
}

func TestExtractAskUserTarget(t *testing.T) {
	got := extractAskUserTarget(`{"title":"Enter your name"}`)
	if got != "Enter your name" {
		t.Errorf("extractAskUserTarget() = %q, want %q", got, "Enter your name")
	}
	got2 := extractAskUserTarget("")
	if got2 != "" {
		t.Errorf("empty input: %q", got2)
	}
	got3 := extractAskUserTarget(`{"prompt":"hello"}`)
	if got3 != "" {
		t.Errorf("no title: %q", got3)
	}
}

func TestParseArchiveTruncation(t *testing.T) {
	shown, total := parseArchiveTruncation("[Showing first 42 of 100 files]")
	if shown != 42 || total != 100 {
		t.Errorf("parseArchiveTruncation() = (%d, %d), want (42, 100)", shown, total)
	}
	shown2, total2 := parseArchiveTruncation("no truncation marker here")
	if shown2 != 0 || total2 != 0 {
		t.Errorf("parseArchiveTruncation() = (%d, %d), want (0, 0)", shown2, total2)
	}
	// No "of" separator
	shown3, total3 := parseArchiveTruncation("[Showing first 50]")
	if shown3 != 0 || total3 != 0 {
		t.Errorf("parseArchiveTruncation no-of = (%d, %d), want (0, 0)", shown3, total3)
	}
}
