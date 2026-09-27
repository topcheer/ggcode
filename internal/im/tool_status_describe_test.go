package im

import (
	"reflect"
	"testing"
)

// TestDescribeToolRoutingMap pins that every former DescribeTool case label
// is registered in toolPresentationBuilders and routes to the exact builder
// carrying that case's body (r193 flatten). Unknown tools stay absent so the
// orchestrator default branch handles them.
func TestDescribeToolRoutingMap(t *testing.T) {
	cases := []struct {
		tool string
		want toolPresentationBuilder
	}{
		{"read_file", readFilePresentation},
		{"edit_file", editFilePresentation},
		{"write_file", writeFilePresentation},
		{"glob", globPresentation},
		{"grep", searchToolPresentation},
		{"search_files", searchToolPresentation},
		{"list_directory", listDirectoryPresentation},
		{"run_command", commandToolPresentation},
		{"bash", commandToolPresentation},
		{"powershell", commandToolPresentation},
		{"start_command", commandToolPresentation},
		{"write_command_input", backgroundJobPresentation},
		{"read_command_output", backgroundJobPresentation},
		{"wait_command", backgroundJobPresentation},
		{"stop_command", backgroundJobPresentation},
		{"list_commands", backgroundJobPresentation},
		{"web_fetch", webFetchPresentation},
		{"web_search", webSearchPresentation},
		{"todo_write", todoWritePresentation},
		{"task", agentTaskPresentation},
		{"agent", agentTaskPresentation},
		{"skill", skillPresentation},
		{"ask_user", askUserPresentation},
		{"git_diff", gitInspectPresentation},
		{"git_status", gitInspectPresentation},
		{"git_log", gitInspectPresentation},
	}
	if len(toolPresentationBuilders) != len(cases) {
		t.Fatalf("toolPresentationBuilders has %d entries, want %d", len(toolPresentationBuilders), len(cases))
	}
	for _, tc := range cases {
		got, ok := toolPresentationBuilders[tc.tool]
		if !ok {
			t.Errorf("tool %q missing from toolPresentationBuilders", tc.tool)
			continue
		}
		if reflect.ValueOf(got).Pointer() != reflect.ValueOf(tc.want).Pointer() {
			t.Errorf("tool %q routed to wrong builder", tc.tool)
		}
	}
	for _, unknown := range []string{"mystery_tool", "", "READ_FILE"} {
		if _, ok := toolPresentationBuilders[unknown]; ok {
			t.Errorf("tool %q must not be routed via table", unknown)
		}
	}
}

// TestDescribeToolBranchSeams pins each builder's argument wiring: the
// pre-existing helpers are recomputed in-process as the want side, so any
// drift in delegation args (key names, precedence, action labels) fails.
func TestDescribeToolBranchSeams(t *testing.T) {
	zh, en := ToolLangZhCN, ToolLangEn

	t.Run("edit_create_vs_edit", func(t *testing.T) {
		got := editFilePresentation(zh, "edit_file", map[string]any{}, "chart.html")
		want := toolPresentationFor(zh, "create", "chart.html")
		if got != want {
			t.Errorf("empty old_text + target = %+v, want %+v", got, want)
		}
		got = editFilePresentation(zh, "edit_file", map[string]any{"old_text": "x"}, "chart.html")
		want = toolPresentationFor(zh, "edit", "chart.html")
		if got != want {
			t.Errorf("old_text set = %+v, want %+v", got, want)
		}
		got = editFilePresentation(zh, "edit_file", map[string]any{}, "")
		want = toolPresentationFor(zh, "edit", "")
		if got != want {
			t.Errorf("empty target forces edit = %+v, want %+v", got, want)
		}
	})

	t.Run("search_precedence", func(t *testing.T) {
		got := searchToolPresentation(zh, "grep", map[string]any{}, "")
		want := toolPresentationFor(zh, "search", displayToolTarget(""))
		if got != want {
			t.Errorf("empty args = %+v, want %+v", got, want)
		}
		got = searchToolPresentation(zh, "grep", map[string]any{"pattern": "a", "query": "q", "path": "p"}, "")
		want = toolPresentationFor(zh, "search", displayToolTarget("a"))
		if got != want {
			t.Errorf("pattern beats query/path = %+v, want %+v", got, want)
		}
		got = searchToolPresentation(zh, "grep", map[string]any{"query": "q", "path": "p"}, "")
		want = toolPresentationFor(zh, "search", displayToolTarget("q"))
		if got != want {
			t.Errorf("query beats path = %+v, want %+v", got, want)
		}
		got = searchToolPresentation(zh, "grep", map[string]any{"path": "p"}, "")
		want = toolPresentationFor(zh, "search", displayToolTarget("p"))
		if got != want {
			t.Errorf("path fallback = %+v, want %+v", got, want)
		}
	})

	t.Run("command_desc_branch", func(t *testing.T) {
		got := commandToolPresentation(en, "run_command", map[string]any{"command": "npm test", "cmd": "ignored", "description": "Run tests"}, "")
		want := ToolPresentation{DisplayName: "Run tests", Detail: displayToolTarget("npm test"), Activity: localizedCommandActivity(en, "Run tests")}
		if got != want {
			t.Errorf("desc set = %+v, want %+v", got, want)
		}
		got = commandToolPresentation(zh, "bash", map[string]any{"cmd": "fallback"}, "")
		want = toolPresentationFor(zh, "run", displayToolTarget("fallback"))
		if got != want {
			t.Errorf("no desc, cmd fallback = %+v, want %+v", got, want)
		}
	})

	t.Run("background_job_fallback", func(t *testing.T) {
		got := backgroundJobPresentation(en, "wait_command", map[string]any{}, "")
		want := toolPresentationFor(en, "run", displayToolTarget("background command"))
		if got != want {
			t.Errorf("no job_id = %+v, want %+v", got, want)
		}
		got = backgroundJobPresentation(zh, "stop_command", map[string]any{"job_id": "j1"}, "")
		want = toolPresentationFor(zh, "run", displayToolTarget("j1"))
		if got != want {
			t.Errorf("job_id set = %+v, want %+v", got, want)
		}
	})

	t.Run("task_precedence", func(t *testing.T) {
		got := agentTaskPresentation(zh, "task", map[string]any{"prompt": "p", "agent_type": "a"}, "")
		want := toolPresentationFor(zh, "task", displayToolTarget("p"))
		if got != want {
			t.Errorf("prompt beats agent_type = %+v, want %+v", got, want)
		}
		got = agentTaskPresentation(zh, "agent", map[string]any{"description": "d", "prompt": "p"}, "")
		want = toolPresentationFor(zh, "task", displayToolTarget("d"))
		if got != want {
			t.Errorf("description beats prompt = %+v, want %+v", got, want)
		}
	})

	t.Run("git_inspect_toolname", func(t *testing.T) {
		got := gitInspectPresentation(zh, "git_status", nil, "")
		want := toolPresentationFor(zh, "inspect", displayToolTarget("git status"))
		if got != want {
			t.Errorf("git_status = %+v, want %+v", got, want)
		}
		got = gitInspectPresentation(en, "git_diff", nil, "")
		want = toolPresentationFor(en, "inspect", displayToolTarget("git diff"))
		if got != want {
			t.Errorf("git_diff = %+v, want %+v", got, want)
		}
	})

	t.Run("list_directory_args", func(t *testing.T) {
		got := listDirectoryPresentation(zh, "list_directory", map[string]any{"directory": "sub"}, "")
		want := toolPresentationFor(zh, "list", displayToolFileTarget("sub"))
		if got != want {
			t.Errorf("directory fallback = %+v, want %+v", got, want)
		}
	})

	t.Run("ask_user_title", func(t *testing.T) {
		got := askUserPresentation(zh, "ask_user", map[string]any{"title": "确认部署"}, "")
		want := toolPresentationFor(zh, "ask", displayToolTarget("确认部署"))
		if got != want {
			t.Errorf("title = %+v, want %+v", got, want)
		}
	})

	t.Run("fixed_targets", func(t *testing.T) {
		if got := todoWritePresentation(zh, "todo_write", nil, ""); got != toolPresentationFor(zh, "todo", "") {
			t.Errorf("todo_write = %+v", got)
		}
		if got := webFetchPresentation(en, "web_fetch", map[string]any{"url": "https://example.com"}, ""); got != toolPresentationFor(en, "fetch", displayToolTarget("https://example.com")) {
			t.Errorf("web_fetch = %+v", got)
		}
		if got := webSearchPresentation(zh, "web_search", map[string]any{"query": "gg"}, ""); got != toolPresentationFor(zh, "search", displayToolTarget("gg")) {
			t.Errorf("web_search = %+v", got)
		}
		if got := globPresentation(zh, "glob", map[string]any{"pattern": "*.go"}, ""); got != toolPresentationFor(zh, "find", displayToolTarget("*.go")) {
			t.Errorf("glob = %+v", got)
		}
		if got := readFilePresentation(zh, "read_file", nil, "a.md"); got != toolPresentationFor(zh, "read", "a.md") {
			t.Errorf("read_file = %+v", got)
		}
		if got := writeFilePresentation(en, "write_file", nil, "a.md"); got != toolPresentationFor(en, "write", "a.md") {
			t.Errorf("write_file = %+v", got)
		}
		if got := skillPresentation(zh, "skill", map[string]any{"skill": "deploy"}, ""); got != toolPresentationFor(zh, "skill", displayToolTarget("deploy")) {
			t.Errorf("skill = %+v", got)
		}
	})
}

// TestDescribeToolDefaultBranch pins the pristine default branch verbatim:
// prettified name as DisplayName, the seven-way firstNonEmptyStr detail
// chain, and the generic activity.
func TestDescribeToolDefaultBranch(t *testing.T) {
	args := map[string]any{"path": "a.txt", "pattern": "pat", "query": "q", "url": "u", "description": "d"}
	pretty := prettifyToolName("mystery_tool")
	want := ToolPresentation{
		DisplayName: pretty,
		Detail: displayToolTarget(firstNonEmptyStr(
			displayToolFileTarget("a.txt"),
			displayToolFileTarget(argString(args, "path")),
			displayToolFileTarget(argString(args, "file_path")),
			argString(args, "pattern"),
			argString(args, "query"),
			argString(args, "url"),
			argString(args, "description"),
		)),
		Activity: localizedGenericActivity(ToolLangZhCN, pretty),
	}
	got := DescribeTool(ToolLangZhCN, "mystery_tool", `{"path":"a.txt","pattern":"pat","query":"q","url":"u","description":"d"}`)
	if got != want {
		t.Errorf("default branch = %+v, want %+v", got, want)
	}
	if want.DisplayName != "Mystery Tool" || want.Detail != "a.txt" || want.Activity != "运行 Mystery Tool" {
		t.Errorf("literal default pins drifted: %+v", want)
	}
}

// TestDescribeToolEndToEndPins pins full DescribeTool outputs byte-for-byte
// for representative cases. Inputs avoid absolute paths so the result is
// cwd-independent (util.FormatToolDetail is an identity function).
func TestDescribeToolEndToEndPins(t *testing.T) {
	cases := []struct {
		name string
		lang ToolLanguage
		tool string
		args string
		want ToolPresentation
	}{
		{"read zh", ToolLangZhCN, "read_file", `{"file_path":"chart.html"}`,
			ToolPresentation{DisplayName: "读", Detail: "chart.html", Activity: "读取 chart.html"}},
		{"edit create zh", ToolLangZhCN, "edit_file", `{"file_path":"new.txt"}`,
			ToolPresentation{DisplayName: "创建", Detail: "new.txt", Activity: "创建 new.txt"}},
		{"edit old_text zh", ToolLangZhCN, "edit_file", `{"file_path":"a.md","old_text":"x"}`,
			ToolPresentation{DisplayName: "编辑", Detail: "a.md", Activity: "编辑 a.md"}},
		{"write en", ToolLangEn, "write_file", `{"file_path":"out.txt"}`,
			ToolPresentation{DisplayName: "Write", Detail: "out.txt", Activity: "Writing out.txt"}},
		{"command desc en", ToolLangEn, "run_command", `{"command":"npm test","description":"Run tests"}`,
			ToolPresentation{DisplayName: "Run tests", Detail: "npm test", Activity: "Run tests"}},
		{"command zh zh", ToolLangZhCN, "bash", `{"command":"ls -la"}`,
			ToolPresentation{DisplayName: "执行", Detail: "ls -la", Activity: "执行 ls -la"}},
		{"grep zh pattern first", ToolLangZhCN, "grep", `{"pattern":"TODO","query":"q","path":"p"}`,
			ToolPresentation{DisplayName: "搜索", Detail: "TODO", Activity: "搜索 TODO"}},
		{"job fallback en", ToolLangEn, "write_command_input", `{}`,
			ToolPresentation{DisplayName: "Run", Detail: "background command", Activity: "Running background command"}},
		{"todo zh", ToolLangZhCN, "todo_write", `{}`,
			ToolPresentation{DisplayName: "更新待办", Detail: "", Activity: "更新待办"}},
		{"git status zh", ToolLangZhCN, "git_status", `{}`,
			ToolPresentation{DisplayName: "检查", Detail: "git status", Activity: "检查 git status"}},
		{"ask user zh", ToolLangZhCN, "ask_user", `{"title":"确认部署"}`,
			ToolPresentation{DisplayName: "提问", Detail: "确认部署", Activity: "提问 确认部署"}},
		{"web fetch en", ToolLangEn, "web_fetch", `{"url":"https://example.com"}`,
			ToolPresentation{DisplayName: "Fetch", Detail: "https://example.com", Activity: "Fetching https://example.com"}},
		{"list dir zh", ToolLangZhCN, "list_directory", `{"path":"src"}`,
			ToolPresentation{DisplayName: "列出", Detail: "src", Activity: "列出 src"}},
		{"bad json falls to empty args", ToolLangZhCN, "web_search", `not-json`,
			ToolPresentation{DisplayName: "搜索", Detail: "", Activity: "搜索中..."}},
	}
	for _, tc := range cases {
		if got := DescribeTool(tc.lang, tc.tool, tc.args); got != tc.want {
			t.Errorf("%s: DescribeTool(%s, %s, %s) = %+v, want %+v", tc.name, tc.lang, tc.tool, tc.args, got, tc.want)
		}
	}
}
