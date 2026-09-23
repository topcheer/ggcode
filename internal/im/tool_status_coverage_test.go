package im

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDescribeToolCoverage exercises the full DescribeTool dispatch across
// tool families and both languages (sa-102 coverage net, zero impl change).
func TestDescribeToolCoverage(t *testing.T) {
	tests := []struct {
		name string
		lang ToolLanguage
		tool string
		args string
		want ToolPresentation
	}{
		// --- English ---
		{"read file", ToolLangEn, "read_file", `{"file_path":"src/a.go"}`, ToolPresentation{DisplayName: "Read", Detail: "src/a.go", Activity: "Reading src/a.go"}},
		{"edit with old_text", ToolLangEn, "edit_file", `{"file_path":"src/a.go","old_text":"x"}`, ToolPresentation{DisplayName: "Edit", Detail: "src/a.go", Activity: "Editing src/a.go"}},
		{"edit create branch", ToolLangEn, "edit_file", `{"file_path":"new.go"}`, ToolPresentation{DisplayName: "Create", Detail: "new.go", Activity: "Creating new.go"}},
		{"edit create empty old_text", ToolLangEn, "edit_file", `{"file_path":"new.go","old_text":"  "}`, ToolPresentation{DisplayName: "Create", Detail: "new.go", Activity: "Creating new.go"}},
		{"write file", ToolLangEn, "write_file", `{"file_path":"b.txt"}`, ToolPresentation{DisplayName: "Write", Detail: "b.txt", Activity: "Writing b.txt"}},
		{"glob", ToolLangEn, "glob", `{"pattern":"**/*_test.go"}`, ToolPresentation{DisplayName: "Find", Detail: "**/*_test.go", Activity: "Finding **/*_test.go"}},
		{"grep pattern", ToolLangEn, "grep", `{"pattern":"TODO"}`, ToolPresentation{DisplayName: "Search", Detail: "TODO", Activity: "Searching TODO"}},
		{"search_files query", ToolLangEn, "search_files", `{"query":"auth flow"}`, ToolPresentation{DisplayName: "Search", Detail: "auth flow", Activity: "Searching auth flow"}},
		{"grep path fallback", ToolLangEn, "grep", `{"path":"internal"}`, ToolPresentation{DisplayName: "Search", Detail: "internal", Activity: "Searching internal"}},
		{"list directory path", ToolLangEn, "list_directory", `{"path":"docs"}`, ToolPresentation{DisplayName: "List", Detail: "docs", Activity: "Listing docs"}},
		{"list directory alt key", ToolLangEn, "list_directory", `{"directory":"cmd"}`, ToolPresentation{DisplayName: "List", Detail: "cmd", Activity: "Listing cmd"}},
		{"run command with desc", ToolLangEn, "run_command", `{"command":"npm test","description":"Running tests"}`, ToolPresentation{DisplayName: "Running tests", Detail: "npm test", Activity: "Running tests"}},
		{"run command plain", ToolLangEn, "run_command", `{"command":"npm test"}`, ToolPresentation{DisplayName: "Run", Detail: "npm test", Activity: "Running npm test"}},
		{"bash cmd key", ToolLangEn, "bash", `{"cmd":"ls -la"}`, ToolPresentation{DisplayName: "Run", Detail: "ls -la", Activity: "Running ls -la"}},
		{"powershell command", ToolLangEn, "powershell", `{"command":"Get-ChildItem"}`, ToolPresentation{DisplayName: "Run", Detail: "Get-ChildItem", Activity: "Running Get-ChildItem"}},
		{"start command with desc", ToolLangEn, "start_command", `{"command":"go run .","description":"Start server"}`, ToolPresentation{DisplayName: "Start server", Detail: "go run .", Activity: "Start server"}},
		{"start command plain", ToolLangEn, "start_command", `{"command":"make build"}`, ToolPresentation{DisplayName: "Run", Detail: "make build", Activity: "Running make build"}},
		{"write command input job", ToolLangEn, "write_command_input", `{"job_id":"job-1"}`, ToolPresentation{DisplayName: "Run", Detail: "job-1", Activity: "Running job-1"}},
		{"read command output fallback", ToolLangEn, "read_command_output", `{}`, ToolPresentation{DisplayName: "Run", Detail: "background command", Activity: "Running background command"}},
		{"wait command job", ToolLangEn, "wait_command", `{"job_id":"w1"}`, ToolPresentation{DisplayName: "Run", Detail: "w1", Activity: "Running w1"}},
		{"stop command fallback", ToolLangEn, "stop_command", `{}`, ToolPresentation{DisplayName: "Run", Detail: "background command", Activity: "Running background command"}},
		{"list commands fallback", ToolLangEn, "list_commands", `{}`, ToolPresentation{DisplayName: "Run", Detail: "background command", Activity: "Running background command"}},
		{"web fetch", ToolLangEn, "web_fetch", `{"url":"https://example.com"}`, ToolPresentation{DisplayName: "Fetch", Detail: "https://example.com", Activity: "Fetching https://example.com"}},
		{"web search", ToolLangEn, "web_search", `{"query":"ggcode"}`, ToolPresentation{DisplayName: "Search", Detail: "ggcode", Activity: "Searching ggcode"}},
		{"todo write", ToolLangEn, "todo_write", `{}`, ToolPresentation{DisplayName: "Update todos", Detail: "", Activity: "Updating todos"}},
		{"task description", ToolLangEn, "task", `{"description":"Fix bug"}`, ToolPresentation{DisplayName: "Run task", Detail: "Fix bug", Activity: "Running task Fix bug"}},
		{"task prompt fallback", ToolLangEn, "task", `{"prompt":"do it"}`, ToolPresentation{DisplayName: "Run task", Detail: "do it", Activity: "Running task do it"}},
		{"agent type fallback", ToolLangEn, "agent", `{"agent_type":"worker"}`, ToolPresentation{DisplayName: "Run task", Detail: "worker", Activity: "Running task worker"}},
		{"skill", ToolLangEn, "skill", `{"skill":"deploy"}`, ToolPresentation{DisplayName: "Load skill", Detail: "deploy", Activity: "Loading skill deploy"}},
		{"ask user title", ToolLangEn, "ask_user", `{"title":"Choose option"}`, ToolPresentation{DisplayName: "Ask", Detail: "Choose option", Activity: "Asking Choose option"}},
		{"git diff", ToolLangEn, "git_diff", `{}`, ToolPresentation{DisplayName: "Inspect", Detail: "git diff", Activity: "Inspecting git diff"}},
		{"git status", ToolLangEn, "git_status", `{}`, ToolPresentation{DisplayName: "Inspect", Detail: "git status", Activity: "Inspecting git status"}},
		{"git log", ToolLangEn, "git_log", `{}`, ToolPresentation{DisplayName: "Inspect", Detail: "git log", Activity: "Inspecting git log"}},
		{"unknown with path", ToolLangEn, "my_tool", `{"path":"p.txt"}`, ToolPresentation{DisplayName: "My Tool", Detail: "p.txt", Activity: "Running My Tool"}},
		{"unknown with file_path", ToolLangEn, "m", `{"file_path":"f.go"}`, ToolPresentation{DisplayName: "M", Detail: "f.go", Activity: "Running M"}},
		{"unknown with query", ToolLangEn, "my_tool", `{"query":"abc"}`, ToolPresentation{DisplayName: "My Tool", Detail: "abc", Activity: "Running My Tool"}},
		{"unknown with pattern", ToolLangEn, "fetch_thing", `{"pattern":"*.md"}`, ToolPresentation{DisplayName: "Fetch Thing", Detail: "*.md", Activity: "Running Fetch Thing"}},
		{"unknown with url", ToolLangEn, "fetch_thing", `{"url":"https://x.io"}`, ToolPresentation{DisplayName: "Fetch Thing", Detail: "https://x.io", Activity: "Running Fetch Thing"}},
		{"unknown with description", ToolLangEn, "custom", `{"description":"Do thing"}`, ToolPresentation{DisplayName: "Custom", Detail: "Do thing", Activity: "Running Custom"}},
		{"unknown no args", ToolLangEn, "unknown", ``, ToolPresentation{DisplayName: "Unknown", Detail: "", Activity: "Running Unknown"}},
		{"invalid json known tool", ToolLangEn, "read_file", `not-json`, ToolPresentation{DisplayName: "Read", Detail: "", Activity: "Reading file"}},
		{"invalid json unknown tool", ToolLangEn, "mystery", `not-json`, ToolPresentation{DisplayName: "Mystery", Detail: "", Activity: "Running Mystery"}},
		// --- Chinese ---
		{"read file zh", ToolLangZhCN, "read_file", `{"file_path":"src/a.go"}`, ToolPresentation{DisplayName: "读", Detail: "src/a.go", Activity: "读取 src/a.go"}},
		{"edit create zh", ToolLangZhCN, "edit_file", `{"file_path":"new.go"}`, ToolPresentation{DisplayName: "创建", Detail: "new.go", Activity: "创建 new.go"}},
		{"write file zh", ToolLangZhCN, "write_file", `{"file_path":"b.txt"}`, ToolPresentation{DisplayName: "写", Detail: "b.txt", Activity: "写入 b.txt"}},
		{"glob zh", ToolLangZhCN, "glob", `{"pattern":"**/*_test.go"}`, ToolPresentation{DisplayName: "查找", Detail: "**/*_test.go", Activity: "查找 **/*_test.go"}},
		{"grep zh", ToolLangZhCN, "grep", `{"pattern":"TODO"}`, ToolPresentation{DisplayName: "搜索", Detail: "TODO", Activity: "搜索 TODO"}},
		{"list directory zh", ToolLangZhCN, "list_directory", `{"path":"docs"}`, ToolPresentation{DisplayName: "列出", Detail: "docs", Activity: "列出 docs"}},
		{"run command desc zh", ToolLangZhCN, "run_command", `{"command":"npm test","description":"Running tests"}`, ToolPresentation{DisplayName: "Running tests", Detail: "npm test", Activity: "正在Running tests"}},
		{"run command plain zh", ToolLangZhCN, "run_command", `{"command":"npm test"}`, ToolPresentation{DisplayName: "执行", Detail: "npm test", Activity: "执行 npm test"}},
		{"write command input zh", ToolLangZhCN, "write_command_input", `{"job_id":"job-1"}`, ToolPresentation{DisplayName: "执行", Detail: "job-1", Activity: "执行 job-1"}},
		{"web fetch zh", ToolLangZhCN, "web_fetch", `{"url":"https://example.com"}`, ToolPresentation{DisplayName: "抓取", Detail: "https://example.com", Activity: "抓取 https://example.com"}},
		{"web search zh", ToolLangZhCN, "web_search", `{"query":"ggcode"}`, ToolPresentation{DisplayName: "搜索", Detail: "ggcode", Activity: "搜索 ggcode"}},
		{"todo write zh", ToolLangZhCN, "todo_write", `{}`, ToolPresentation{DisplayName: "更新待办", Detail: "", Activity: "更新待办"}},
		{"task zh", ToolLangZhCN, "task", `{"description":"Fix bug"}`, ToolPresentation{DisplayName: "执行任务", Detail: "Fix bug", Activity: "执行任务 Fix bug"}},
		{"skill zh", ToolLangZhCN, "skill", `{"skill":"deploy"}`, ToolPresentation{DisplayName: "加载技能", Detail: "deploy", Activity: "加载技能 deploy"}},
		{"ask user zh", ToolLangZhCN, "ask_user", `{"title":"Choose option"}`, ToolPresentation{DisplayName: "提问", Detail: "Choose option", Activity: "提问 Choose option"}},
		{"git diff zh", ToolLangZhCN, "git_diff", `{}`, ToolPresentation{DisplayName: "检查", Detail: "git diff", Activity: "检查 git diff"}},
		{"unknown with path zh", ToolLangZhCN, "my_tool", `{"path":"p.txt"}`, ToolPresentation{DisplayName: "My Tool", Detail: "p.txt", Activity: "运行 My Tool"}},
		{"unknown no args zh", ToolLangZhCN, "unknown", ``, ToolPresentation{DisplayName: "Unknown", Detail: "", Activity: "运行 Unknown"}},
		{"invalid json known zh", ToolLangZhCN, "read_file", `not-json`, ToolPresentation{DisplayName: "读", Detail: "", Activity: "读取文件"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeTool(tt.lang, tt.tool, tt.args)
			if got.DisplayName != tt.want.DisplayName {
				t.Errorf("DisplayName = %q, want %q", got.DisplayName, tt.want.DisplayName)
			}
			if got.Detail != tt.want.Detail {
				t.Errorf("Detail = %q, want %q", got.Detail, tt.want.Detail)
			}
			if got.Activity != tt.want.Activity {
				t.Errorf("Activity = %q, want %q", got.Activity, tt.want.Activity)
			}
		})
	}
}

// TestDescribeToolCoverageBreadth sweeps all tool families in both languages
// and asserts presentation fields are never empty where a target exists.
func TestDescribeToolCoverageBreadth(t *testing.T) {
	tools := []struct {
		name string
		args string
	}{
		{"read_file", `{"file_path":"a.go"}`},
		{"edit_file", `{"file_path":"a.go","old_text":"x"}`},
		{"write_file", `{"file_path":"a.go"}`},
		{"glob", `{"pattern":"*.go"}`},
		{"grep", `{"pattern":"p"}`},
		{"search_files", `{"query":"q"}`},
		{"list_directory", `{"path":"d"}`},
		{"run_command", `{"command":"c"}`},
		{"start_command", `{"command":"c"}`},
		{"write_command_input", `{"job_id":"j"}`},
		{"read_command_output", `{}`},
		{"web_fetch", `{"url":"https://e.com"}`},
		{"web_search", `{"query":"q"}`},
		{"todo_write", `{}`},
		{"task", `{"description":"d"}`},
		{"skill", `{"skill":"s"}`},
		{"ask_user", `{"title":"t"}`},
		{"git_diff", `{}`},
		{"totally_unknown_tool", `{}`},
	}
	for _, lang := range []ToolLanguage{ToolLangEn, ToolLangZhCN} {
		for _, tool := range tools {
			got := DescribeTool(lang, tool.name, tool.args)
			if got.DisplayName == "" {
				t.Errorf("%s/%s: empty DisplayName", lang, tool.name)
			}
			if got.Activity == "" {
				t.Errorf("%s/%s: empty Activity", lang, tool.name)
			}
		}
	}
}

// TestFormatIMStatusCoverage pins the full status pipeline branch matrix.
func TestFormatIMStatusCoverage(t *testing.T) {
	tests := []struct {
		name     string
		lang     ToolLanguage
		activity string
		tool     string
		arg      string
		want     string
	}{
		{"thinking no tool zh", ToolLangZhCN, "思考中...", "", "", ""},
		{"thinking no tool en", ToolLangEn, "Thinking...", "", "", ""},
		{"writing no tool zh", ToolLangZhCN, "输出中...", "", "", ""},
		{"writing no tool en", ToolLangEn, "Writing...", "", "", ""},
		{"thinking with tool en", ToolLangEn, "Thinking...", "Read", "a.go", "Working on Read a.go..."},
		{"thinking with tool zh", ToolLangZhCN, "思考中...", "读", "a.go", "正在读 a.go..."},
		{"empty activity tool only en", ToolLangEn, "", "Edit", "b.txt", "Working on Edit b.txt..."},
		{"activity only zh kept", ToolLangZhCN, "正在读取 a.go", "", "", "正在读取 a.go"},
		{"activity only en wrapped", ToolLangEn, "Reading a.go", "", "", "Working on Reading a.go..."},
		{"command activity zh kept", ToolLangZhCN, "正在Running tests", "", "", "正在Running tests"},
		{"both empty", ToolLangEn, "  ", "", "", ""},
		{"trivial arg collapses", ToolLangEn, "", "Read", "{}", "Working on Read..."},
		{"whitespace thinking trimmed", ToolLangZhCN, "  思考中...  ", "", "", ""},
		{"unrelated activity and tool prefers activity", ToolLangEn, "Deploying now", "Run", "x", "Working on Deploying now..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatIMStatus(tt.lang, tt.activity, tt.tool, tt.arg)
			if got != tt.want {
				t.Errorf("FormatIMStatus(%s, %q, %q, %q) = %q, want %q", tt.lang, tt.activity, tt.tool, tt.arg, got, tt.want)
			}
		})
	}
}

// TestLocalizeIMProgressCoverage pins zh-CN and en localization branches.
func TestLocalizeIMProgressCoverage(t *testing.T) {
	tests := []struct {
		name string
		lang ToolLanguage
		in   string
		want string
	}{
		{"empty en", ToolLangEn, "", ""},
		{"spaces zh", ToolLangZhCN, "   ", ""},
		{"zh thinking dots", ToolLangZhCN, "思考中...", "我先想一下..."},
		{"zh thinking ellipsis", ToolLangZhCN, "思考中…", "我先想一下..."},
		{"zh writing dots", ToolLangZhCN, "输出中...", "我整理一下结果..."},
		{"zh writing ellipsis", ToolLangZhCN, "输出中…", "我整理一下结果..."},
		{"zh wo prefix kept", ToolLangZhCN, "我来了...", "我来了..."},
		{"zh zhengzai prefix kept", ToolLangZhCN, "正在运行", "正在运行"},
		{"zh wrapped", ToolLangZhCN, "读文件", "正在读文件..."},
		{"zh dots only", ToolLangZhCN, "...", ""},
		{"zh ellipsis only", ToolLangZhCN, "…", ""},
		{"en thinking", ToolLangEn, "Thinking...", "Let me think..."},
		{"en thinking ellipsis", ToolLangEn, "Thinking…", "Let me think..."},
		{"en writing", ToolLangEn, "Writing...", "I'm drafting the answer..."},
		{"en im prefix kept", ToolLangEn, "I'm working", "I'm working"},
		{"en i am prefix kept", ToolLangEn, "I am busy...", "I am busy..."},
		{"en let me prefix kept", ToolLangEn, "Let me check", "Let me check"},
		{"en wrapped", ToolLangEn, "Read file", "Working on Read file..."},
		{"en dots only", ToolLangEn, "...", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocalizeIMProgress(tt.lang, tt.in); got != tt.want {
				t.Errorf("LocalizeIMProgress(%s, %q) = %q, want %q", tt.lang, tt.in, got, tt.want)
			}
		})
	}
}

// TestLocalizedToolLabelCoverage pins every action label in both languages.
func TestLocalizedToolLabelCoverage(t *testing.T) {
	zh := map[string]string{
		"read": "读", "edit": "编辑", "create": "创建", "write": "写",
		"search": "搜索", "find": "查找", "list": "列出", "run": "执行",
		"fetch": "抓取", "todo": "更新待办", "task": "执行任务",
		"skill": "加载技能", "ask": "提问", "inspect": "检查",
	}
	en := map[string]string{
		"read": "Read", "edit": "Edit", "create": "Create", "write": "Write",
		"search": "Search", "find": "Find", "list": "List", "run": "Run",
		"fetch": "Fetch", "todo": "Update todos", "task": "Run task",
		"skill": "Load skill", "ask": "Ask", "inspect": "Inspect",
	}
	for action, want := range zh {
		if got := localizedToolLabel(ToolLangZhCN, action); got != want {
			t.Errorf("zh label %q = %q, want %q", action, got, want)
		}
	}
	for action, want := range en {
		if got := localizedToolLabel(ToolLangEn, action); got != want {
			t.Errorf("en label %q = %q, want %q", action, got, want)
		}
	}
	if got := localizedToolLabel(ToolLangZhCN, "my_action"); got != "my action" {
		t.Errorf("zh unknown label = %q", got)
	}
	if got := localizedToolLabel(ToolLangEn, "my_action"); got != "My Action" {
		t.Errorf("en unknown label = %q", got)
	}
	if got := localizedToolLabel(ToolLangEn, "foo"); got != "Foo" {
		t.Errorf("en unknown single label = %q", got)
	}
}

// TestLocalizedToolActivityCoverage pins both the empty-target and
// with-target branches for every action in both languages, plus the
// generic fallbacks (including the todo-with-target and unknown-action edges).
func TestLocalizedToolActivityCoverage(t *testing.T) {
	emptyZh := map[string]string{
		"read": "读取文件", "edit": "编辑文件", "create": "创建文件", "write": "写入文件",
		"search": "搜索中...", "find": "查找文件", "list": "列出目录", "run": "执行命令",
		"fetch": "抓取网页", "todo": "更新待办", "task": "执行任务",
		"skill": "加载技能", "ask": "等待用户输入", "inspect": "检查中...",
	}
	emptyEn := map[string]string{
		"read": "Reading file", "edit": "Editing file", "create": "Creating file", "write": "Writing file",
		"search": "Searching...", "find": "Finding files", "list": "Listing directory", "run": "Running command",
		"fetch": "Fetching page", "todo": "Updating todos", "task": "Running task",
		"skill": "Loading skill", "ask": "Waiting for user input", "inspect": "Inspecting...",
	}
	for action, want := range emptyZh {
		if got := localizedToolActivity(ToolLangZhCN, action, ""); got != want {
			t.Errorf("zh empty-target %q = %q, want %q", action, got, want)
		}
	}
	for action, want := range emptyEn {
		if got := localizedToolActivity(ToolLangEn, action, ""); got != want {
			t.Errorf("en empty-target %q = %q, want %q", action, got, want)
		}
	}

	withZh := map[string]string{
		"read": "读取 x", "edit": "编辑 x", "create": "创建 x", "write": "写入 x",
		"search": "搜索 x", "find": "查找 x", "list": "列出 x", "run": "执行 x",
		"fetch": "抓取 x", "task": "执行任务 x", "skill": "加载技能 x",
		"ask": "提问 x", "inspect": "检查 x",
	}
	withEn := map[string]string{
		"read": "Reading x", "edit": "Editing x", "create": "Creating x", "write": "Writing x",
		"search": "Searching x", "find": "Finding x", "list": "Listing x", "run": "Running x",
		"fetch": "Fetching x", "task": "Running task x", "skill": "Loading skill x",
		"ask": "Asking x", "inspect": "Inspecting x",
	}
	for action, want := range withZh {
		if got := localizedToolActivity(ToolLangZhCN, action, "x"); got != want {
			t.Errorf("zh target %q = %q, want %q", action, got, want)
		}
	}
	for action, want := range withEn {
		if got := localizedToolActivity(ToolLangEn, action, "x"); got != want {
			t.Errorf("en target %q = %q, want %q", action, got, want)
		}
	}
	// "todo" has no with-target branch: falls through to the generic activity.
	if got := localizedToolActivity(ToolLangZhCN, "todo", "x"); got != "运行 x" {
		t.Errorf("zh todo-with-target = %q", got)
	}
	if got := localizedToolActivity(ToolLangEn, "todo", "x"); got != "Running x" {
		t.Errorf("en todo-with-target = %q", got)
	}
	// Unknown action with empty target falls through to the generic activity too.
	if got := localizedToolActivity(ToolLangZhCN, "nope", ""); got != "运行 " {
		t.Errorf("zh unknown-empty = %q", got)
	}
	if got := localizedToolActivity(ToolLangEn, "nope", ""); got != "Running " {
		t.Errorf("en unknown-empty = %q", got)
	}
}

// TestArgStringCoverage pins type coercion and single-line compaction.
func TestArgStringCoverage(t *testing.T) {
	if got := argString(nil, "k"); got != "" {
		t.Errorf("nil map = %q", got)
	}
	args := map[string]any{
		"str":   "hello",
		"multi": "a\nb\tc  d",
		"num":   float64(42),
		"pi":    3.5,
		"flag":  true,
		"list":  []any{float64(1), float64(2)},
		"empty": "",
	}
	if got := argString(args, "missing"); got != "" {
		t.Errorf("missing = %q", got)
	}
	if got := argString(args, "empty"); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := argString(args, "str"); got != "hello" {
		t.Errorf("string = %q", got)
	}
	if got := argString(args, "multi"); got != "a b c d" {
		t.Errorf("compacted = %q", got)
	}
	if got := argString(args, "num"); got != "42" {
		t.Errorf("float64 = %q", got)
	}
	if got := argString(args, "pi"); got != "3.5" {
		t.Errorf("float64 frac = %q", got)
	}
	if got := argString(args, "flag"); got != "true" {
		t.Errorf("bool = %q", got)
	}
	if got := argString(args, "list"); got != "[1,2]" {
		t.Errorf("list = %q", got)
	}
	// json.Marshal failure path: channels cannot be marshalled.
	failing := map[string]any{"ch": make(chan int)}
	if got := argString(failing, "ch"); got != "" {
		t.Errorf("unmarshalable = %q", got)
	}
}

// TestDisplayToolTargetCoverage pins trimming and compaction of targets.
func TestDisplayToolTargetCoverage(t *testing.T) {
	if got := displayToolTarget(""); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := displayToolTarget("  x\ny  "); got != "x y" {
		t.Errorf("compact = %q", got)
	}
	if got := displayToolTarget("npm test"); got != "npm test" {
		t.Errorf("plain = %q", got)
	}
}

// TestDisplayToolFileTargetCoverage pins path display: trimming, slash
// normalization, and cwd-relative resolution for absolute paths.
func TestDisplayToolFileTargetCoverage(t *testing.T) {
	if got := displayToolFileTarget(""); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := displayToolFileTarget("/"); got != "" {
		t.Errorf("root only = %q", got)
	}
	if got := displayToolFileTarget("  src/a.go  "); got != "src/a.go" {
		t.Errorf("trim = %q", got)
	}
	if got := displayToolFileTarget("src/a.go/"); got != "src/a.go" {
		t.Errorf("trailing slash = %q", got)
	}
	if got := displayToolFileTarget(`src\a.go\`); got != `src\a.go` {
		t.Errorf("trailing backslash = %q", got)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if got := displayToolFileTarget(filepath.Join(cwd, "tt_x.go")); got != "tt_x.go" {
		t.Errorf("abs under cwd = %q, want tt_x.go", got)
	}
	if got := displayToolFileTarget(filepath.Join(cwd, "sub", "tt_y.go")); got != "sub/tt_y.go" {
		t.Errorf("nested abs under cwd = %q, want sub/tt_y.go", got)
	}
	if got := displayToolFileTarget("/etc/hosts"); got != "/etc/hosts" {
		t.Errorf("abs outside cwd = %q", got)
	}
}

// TestNormalizeDisplayPathCoverage pins symlink resolution fallbacks.
func TestNormalizeDisplayPathCoverage(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(existing, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := normalizeDisplayPath(existing); got != resolved {
		t.Errorf("existing file = %q, want %q", got, resolved)
	}
	missing := filepath.Join(dir, "missing.txt")
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks dir: %v", err)
	}
	if got := normalizeDisplayPath(missing); got != filepath.Join(wantDir, "missing.txt") {
		t.Errorf("missing file in existing dir = %q", got)
	}
	deep := filepath.Join(dir, "no", "such", "file.txt")
	if got := normalizeDisplayPath(deep); got != filepath.Clean(deep) {
		t.Errorf("deep missing = %q, want %q", got, filepath.Clean(deep))
	}
}

// TestIsTrivialAndInlineCoverage pins trivial-detail detection and inline
// formatting.
func TestIsTrivialAndInlineCoverage(t *testing.T) {
	for _, trivial := range []string{"", "  ", "{}", "[]", "null", " null "} {
		if !isTrivialToolDetail(trivial) {
			t.Errorf("isTrivialToolDetail(%q) = false, want true", trivial)
		}
	}
	for _, real := range []string{"a.go", "{ }", "nulls"} {
		if isTrivialToolDetail(real) {
			t.Errorf("isTrivialToolDetail(%q) = true, want false", real)
		}
	}
	if got := FormatToolInline("读", "{}"); got != "读" {
		t.Errorf("trivial inline = %q", got)
	}
	if got := FormatToolInline("读", "a.go"); got != "读 a.go" {
		t.Errorf("detail inline = %q", got)
	}
}

// TestAskUserToolTargetNonMapFirst covers the branch where the questions
// array exists but its first element is not an object.
func TestAskUserToolTargetNonMapFirst(t *testing.T) {
	args := map[string]any{"questions": []any{"not-an-object"}}
	if got := askUserToolTarget(args); got != "" {
		t.Errorf("non-map first question = %q, want empty", got)
	}
	// First element object with no title/prompt also yields empty.
	args2 := map[string]any{"questions": []any{map[string]any{"other": "field"}}}
	if got := askUserToolTarget(args2); got != "" {
		t.Errorf("no title/prompt = %q, want empty", got)
	}
}

// TestExtractFilePathCoverage pins per-tool path extraction keys.
func TestExtractFilePathCoverage(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args string
		want string
	}{
		{"read file_path first", "read_file", `{"file_path":"a","path":"b"}`, "a"},
		{"read path fallback", "read_file", `{"path":"b"}`, "b"},
		{"edit file_path", "edit_file", `{"file_path":"e.go"}`, "e.go"},
		{"write file_path", "write_file", `{"file_path":"w.go"}`, "w.go"},
		{"glob pattern", "glob", `{"pattern":"*.go"}`, "*.go"},
		{"grep path", "grep", `{"path":"d"}`, "d"},
		{"search_files directory", "search_files", `{"directory":"dd"}`, "dd"},
		{"list_directory directory", "list_directory", `{"directory":"d2"}`, "d2"},
		{"unknown path", "custom_tool", `{"path":"p"}`, "p"},
		{"unknown file_path", "custom_tool", `{"file_path":"f"}`, "f"},
		{"empty args", "read_file", `{}`, ""},
		{"invalid json", "read_file", `nope`, ""},
		{"empty raw", "read_file", ``, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractFilePath(tt.tool, tt.args); got != tt.want {
				t.Errorf("extractFilePath(%q, %q) = %q, want %q", tt.tool, tt.args, got, tt.want)
			}
		})
	}
}

// TestFirstNonEmptyStrCoverage pins whitespace-only skipping.
func TestFirstNonEmptyStrCoverage(t *testing.T) {
	if got := firstNonEmptyStr("", "  ", "x"); got != "x" {
		t.Errorf("skip whitespace = %q", got)
	}
	if got := firstNonEmptyStr("a", "b"); got != "a" {
		t.Errorf("first wins = %q", got)
	}
	if got := firstNonEmptyStr("", ""); got != "" {
		t.Errorf("all empty = %q", got)
	}
	if got := firstNonEmptyStr(); got != "" {
		t.Errorf("no args = %q", got)
	}
}

// TestParseToolArgsCoverage pins JSON parsing fallbacks.
func TestParseToolArgsCoverage(t *testing.T) {
	args := parseToolArgs(`{"a":1,"b":"x"}`)
	if args == nil || len(args) != 2 {
		t.Errorf("valid args = %v", args)
	}
	if _, ok := args["b"].(string); !ok {
		t.Errorf("b not string: %v", args["b"])
	}
	for _, raw := range []string{"not json", "", "null", "[1,2]", `"str"`} {
		if got := parseToolArgs(raw); got != nil {
			t.Errorf("parseToolArgs(%q) = %v, want nil", raw, got)
		}
	}
}
