package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDescribeTool(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		rawArgs     string
		wantDisplay string // FormatToolInline(DisplayName, Detail)
		wantName    string
		wantDetail  string
	}{
		// File operations
		{
			name: "read_file", toolName: "read_file",
			rawArgs:     `{"path":"/tmp/test.go"}`,
			wantName:    "Read",
			wantDetail:  "/tmp/test.go",
			wantDisplay: "Read /tmp/test.go",
		},
		{
			name: "read_file with offset", toolName: "read_file",
			rawArgs:     `{"path":"/tmp/test.go","offset":10,"limit":20}`,
			wantName:    "Read",
			wantDetail:  "/tmp/test.go",
			wantDisplay: "Read /tmp/test.go",
		},
		{
			name: "edit_file", toolName: "edit_file",
			rawArgs:     `{"file_path":"/tmp/test.go","old_text":"foo","new_text":"bar"}`,
			wantName:    "Edit",
			wantDetail:  "/tmp/test.go",
			wantDisplay: "Edit /tmp/test.go",
		},
		{
			name: "edit_file create (no old_text)", toolName: "edit_file",
			rawArgs:     `{"file_path":"/tmp/new.go","new_text":"package main"}`,
			wantName:    "Create",
			wantDetail:  "/tmp/new.go",
			wantDisplay: "Create /tmp/new.go",
		},
		{
			name: "write_file", toolName: "write_file",
			rawArgs:     `{"path":"/tmp/out.go","content":"package main"}`,
			wantName:    "Write",
			wantDetail:  "/tmp/out.go",
			wantDisplay: "Write /tmp/out.go",
		},
		{
			name: "multi_edit", toolName: "multi_edit",
			rawArgs:     `{"file_path":"/tmp/test.go"}`,
			wantName:    "Edit",
			wantDetail:  "/tmp/test.go",
			wantDisplay: "Edit /tmp/test.go",
		},

		// Search
		{
			name: "glob", toolName: "glob",
			rawArgs:     `{"pattern":"**/*.go"}`,
			wantName:    "Find",
			wantDetail:  "**/*.go",
			wantDisplay: "Find **/*.go",
		},
		{
			name: "search_files with pattern", toolName: "search_files",
			rawArgs:     `{"pattern":"TODO","directory":"/tmp"}`,
			wantName:    "Search",
			wantDetail:  "TODO",
			wantDisplay: "Search TODO",
		},
		{
			name: "grep", toolName: "grep",
			rawArgs:     `{"pattern":"func Test"}`,
			wantName:    "Search",
			wantDetail:  "func Test",
			wantDisplay: "Search func Test",
		},

		// Directory
		{
			name: "list_directory with path", toolName: "list_directory",
			rawArgs:     `{"path":"/tmp/project"}`,
			wantName:    "List",
			wantDetail:  "/tmp/project",
			wantDisplay: "List /tmp/project",
		},
		{
			name: "list_directory with directory", toolName: "list_directory",
			rawArgs:     `{"directory":"/tmp/project"}`,
			wantName:    "List",
			wantDetail:  "/tmp/project",
			wantDisplay: "List /tmp/project",
		},

		// Commands
		{
			name: "swarm_task_create prefers subject", toolName: "swarm_task_create",
			rawArgs:     `{"team_id":"team-1","subject":"Fix tunnel replay","description":"## plan\n- step"}`,
			wantName:    "Fix tunnel replay",
			wantDetail:  "",
			wantDisplay: "Fix tunnel replay",
		},
		{
			name: "run_command simple", toolName: "run_command",
			rawArgs:     `{"command":"go test ./..."}`,
			wantName:    "go test ./...",
			wantDetail:  "",
			wantDisplay: "go test ./...",
		},
		{
			name: "run_command long command", toolName: "run_command",
			rawArgs:     `{"command":"go build -o /usr/local/bin/my-very-long-binary-name ./cmd/my-app"}`,
			wantName:    "go build -o /usr/local/bin/my-very-long-binary-name ./cmd/my-app",
			wantDetail:  "",
			wantDisplay: "go build -o /usr/local/bin/my-very-long-binary-name ./cmd/my-app",
		},
		{
			name: "start_command", toolName: "start_command",
			rawArgs:     `{"command":"npm run dev"}`,
			wantName:    "npm run dev",
			wantDetail:  "",
			wantDisplay: "npm run dev",
		},
		{
			name: "write_command_input", toolName: "write_command_input",
			rawArgs:     `{"job_id":"abc123456789","input":"hello"}`,
			wantName:    "Input",
			wantDetail:  "[abc12345] → hello",
			wantDisplay: "Input [abc12345] → hello",
		},
		{
			name: "write_command_input long text", toolName: "write_command_input",
			rawArgs:     `{"job_id":"abc123","input":"this is a very long input text that should be truncated at sixty characters limit"}`,
			wantName:    "Input",
			wantDetail:  "[abc123] → this is a very long input text that should be truncated a…",
			wantDisplay: "Input [abc123] → this is a very long input text that should be truncated a…",
		},
		{
			name: "write_command_input no input", toolName: "write_command_input",
			rawArgs:     `{"job_id":"abc123"}`,
			wantName:    "Input",
			wantDetail:  "abc123",
			wantDisplay: "Input abc123",
		},
		{
			name: "read_command_output", toolName: "read_command_output",
			rawArgs:     `{"job_id":"abcdef123456"}`,
			wantName:    "Output",
			wantDetail:  "abcdef12",
			wantDisplay: "Output abcdef12",
		},
		{
			name: "wait_command", toolName: "wait_command",
			rawArgs:     `{"job_id":"abc123","wait_seconds":"30"}`,
			wantName:    "Wait",
			wantDetail:  "abc123 (30s)",
			wantDisplay: "Wait abc123 (30s)",
		},
		{
			name: "stop_command", toolName: "stop_command",
			rawArgs:     `{"job_id":"abcdefgh"}`,
			wantName:    "Stop",
			wantDetail:  "abcdefgh",
			wantDisplay: "Stop abcdefgh",
		},
		{
			name: "list_commands", toolName: "list_commands",
			rawArgs:     `{}`,
			wantName:    "Background Jobs",
			wantDetail:  "",
			wantDisplay: "Background Jobs",
		},

		// Web
		{
			name: "web_fetch", toolName: "web_fetch",
			rawArgs:     `{"url":"https://example.com/api"}`,
			wantName:    "Fetch",
			wantDetail:  "https://example.com/api",
			wantDisplay: "Fetch https://example.com/api",
		},
		{
			name: "web_search", toolName: "web_search",
			rawArgs:     `{"query":"golang json marshal"}`,
			wantName:    "Search",
			wantDetail:  "golang json marshal",
			wantDisplay: "Search golang json marshal",
		},

		// Productivity
		{
			name: "todo_write", toolName: "todo_write",
			rawArgs:     `{"todos":[{"id":"1","content":"test"}]}`,
			wantName:    "Update Todos",
			wantDetail:  "",
			wantDisplay: "Update Todos",
		},
		{
			name: "ask_user with prompt", toolName: "ask_user",
			rawArgs:     `{"prompt":"Which file to edit?"}`,
			wantName:    "Ask",
			wantDetail:  "Which file to edit?",
			wantDisplay: "Ask Which file to edit?",
		},
		{
			name: "skill", toolName: "skill",
			rawArgs:     `{"skill":"debug"}`,
			wantName:    "Skill",
			wantDetail:  "debug",
			wantDisplay: "Skill debug",
		},
		{
			name: "save_memory", toolName: "save_memory",
			rawArgs:     `{"key":"my-pattern","content":"use X for Y"}`,
			wantName:    "Save Memory",
			wantDetail:  "my-pattern",
			wantDisplay: "Save Memory my-pattern",
		},

		// Git
		{
			name: "git_status", toolName: "git_status",
			rawArgs:     `{}`,
			wantName:    "Inspect",
			wantDetail:  "",
			wantDisplay: "Inspect",
		},
		{
			name: "git_status with path", toolName: "git_status",
			rawArgs:     `{"path":"/tmp/repo"}`,
			wantName:    "Inspect",
			wantDetail:  "/tmp/repo",
			wantDisplay: "Inspect /tmp/repo",
		},
		{
			name: "git_diff", toolName: "git_diff",
			rawArgs:     `{"cached":true,"file":"main.go"}`,
			wantName:    "Diff",
			wantDetail:  "--cached main.go",
			wantDisplay: "Diff --cached main.go",
		},
		{
			name: "git_diff no cached", toolName: "git_diff",
			rawArgs:     `{"file":"main.go"}`,
			wantName:    "Diff",
			wantDetail:  "main.go",
			wantDisplay: "Diff main.go",
		},
		{
			name: "git_log", toolName: "git_log",
			rawArgs:     `{}`,
			wantName:    "Log",
			wantDetail:  "",
			wantDisplay: "Log",
		},
		{
			name: "git_show", toolName: "git_show",
			rawArgs:     `{"revision":"abc123"}`,
			wantName:    "Show",
			wantDetail:  "abc123",
			wantDisplay: "Show abc123",
		},
		{
			name: "git_blame", toolName: "git_blame",
			rawArgs:     `{"file":"main.go"}`,
			wantName:    "Blame",
			wantDetail:  "main.go",
			wantDisplay: "Blame main.go",
		},
		{
			name: "git_branch_list remote", toolName: "git_branch_list",
			rawArgs:     `{"remote":true}`,
			wantName:    "Branches",
			wantDetail:  "--remote",
			wantDisplay: "Branches --remote",
		},
		{
			name: "git_branch_list local", toolName: "git_branch_list",
			rawArgs:     `{}`,
			wantName:    "Branches",
			wantDetail:  "",
			wantDisplay: "Branches",
		},
		{
			name: "git_remote", toolName: "git_remote",
			rawArgs:     `{}`,
			wantName:    "Remote",
			wantDetail:  "",
			wantDisplay: "Remote",
		},
		{
			name: "git_stash_list", toolName: "git_stash_list",
			rawArgs:     `{}`,
			wantName:    "Stash",
			wantDetail:  "list",
			wantDisplay: "Stash list",
		},
		{
			name: "git_add", toolName: "git_add",
			rawArgs:     `{"files":["main.go","test.go"]}`,
			wantName:    "Stage",
			wantDetail:  "main.go, test.go",
			wantDisplay: "Stage main.go, test.go",
		},
		{
			name: "git_commit", toolName: "git_commit",
			rawArgs:     `{"message":"feat: add new feature"}`,
			wantName:    "Commit",
			wantDetail:  "feat: add new feature",
			wantDisplay: "Commit feat: add new feature",
		},
		{
			name: "git_stash push", toolName: "git_stash",
			rawArgs:     `{"action":"push"}`,
			wantName:    "Stash",
			wantDetail:  "push",
			wantDisplay: "Stash push",
		},
		{
			name: "git_stash default", toolName: "git_stash",
			rawArgs:     `{}`,
			wantName:    "Stash",
			wantDetail:  "push",
			wantDisplay: "Stash push",
		},

		// Sleep
		{
			name: "sleep seconds", toolName: "sleep",
			rawArgs:     `{"seconds":5}`,
			wantName:    "Sleep",
			wantDetail:  "5s",
			wantDisplay: "Sleep 5s",
		},
		{
			name: "sleep ms", toolName: "sleep",
			rawArgs:     `{"milliseconds":500}`,
			wantName:    "Sleep",
			wantDetail:  "500ms",
			wantDisplay: "Sleep 500ms",
		},

		// Agents
		{
			name: "spawn_agent", toolName: "spawn_agent",
			rawArgs:     `{"description":"fix bug in auth"}`,
			wantName:    "Spawn Agent",
			wantDetail:  "fix bug in auth",
			wantDisplay: "Spawn Agent fix bug in auth",
		},
		{
			name: "wait_agent", toolName: "wait_agent",
			rawArgs:     `{"task_id":"task-123"}`,
			wantName:    "Wait Agent",
			wantDetail:  "task-123",
			wantDisplay: "Wait Agent task-123",
		},
		{
			name: "list_agents", toolName: "list_agents",
			rawArgs:     `{}`,
			wantName:    "List Agents",
			wantDetail:  "",
			wantDisplay: "List Agents",
		},

		// Unknown / MCP tools
		{
			name: "unknown tool", toolName: "my_mcp_tool",
			rawArgs:     `{"query":"test"}`,
			wantName:    "My Mcp Tool",
			wantDetail:  `{"query":"test"}`,
			wantDisplay: `My Mcp Tool {"query":"test"}`,
		},
		{
			name: "unknown tool empty args", toolName: "custom_tool",
			rawArgs:     `{}`,
			wantName:    "Custom Tool",
			wantDetail:  "",
			wantDisplay: "Custom Tool",
		},

		// Edge cases
		{
			name: "empty args", toolName: "read_file",
			rawArgs:     `{}`,
			wantName:    "Read",
			wantDetail:  "",
			wantDisplay: "Read",
		},
		{
			name: "invalid json args", toolName: "run_command",
			rawArgs:     `not json`,
			wantName:    "Run",
			wantDetail:  "",
			wantDisplay: "Run",
		},
		{
			name: "path with trailing slash", toolName: "list_directory",
			rawArgs:     `{"path":"/tmp/project/"}`,
			wantName:    "List",
			wantDetail:  "/tmp/project",
			wantDisplay: "List /tmp/project",
		},
		{
			name: "run_command with cmd field", toolName: "run_command",
			rawArgs:     `{"cmd":"ls -la"}`,
			wantName:    "ls -la",
			wantDetail:  "",
			wantDisplay: "ls -la",
		},
		{
			name: "run_command empty command", toolName: "run_command",
			rawArgs:     `{}`,
			wantName:    "Run",
			wantDetail:  "",
			wantDisplay: "Run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			present := DescribeTool(tt.toolName, tt.rawArgs)
			if present.DisplayName != tt.wantName {
				t.Errorf("DisplayName = %q, want %q", present.DisplayName, tt.wantName)
			}
			if present.Detail != tt.wantDetail {
				t.Errorf("Detail = %q, want %q", present.Detail, tt.wantDetail)
			}
			got := FormatToolInline(present.DisplayName, present.Detail)
			if got != tt.wantDisplay {
				t.Errorf("FormatToolInline = %q, want %q", got, tt.wantDisplay)
			}
		})
	}
}

func TestDescribeTaskToolResult(t *testing.T) {
	present, ok := DescribeTaskToolResult(
		"task_update",
		`{"taskId":"task-1","status":"in_progress","owner":"agent-1"}`,
		`{"id":"task-1","subject":"Fix mobile parity","status":"in_progress","owner":"agent-1"}`,
		false,
	)
	if !ok {
		t.Fatal("expected task result presentation")
	}
	if present.Summary != "Updated Fix mobile parity [in progress] — task-1 (status, owner)" {
		t.Fatalf("unexpected summary: %q", present.Summary)
	}
	if present.PayloadMode != "task_fields" {
		t.Fatalf("unexpected payload mode: %q", present.PayloadMode)
	}
	if want := "Task ID: task-1"; !strings.Contains(present.Payload, want) {
		t.Fatalf("payload %q missing %q", present.Payload, want)
	}
}

func TestDescribeTaskListResult(t *testing.T) {
	present, ok := DescribeTaskToolResult(
		"task_list",
		`{}`,
		"- task-1 [pending] one\n- task-2 [in_progress] two\n",
		false,
	)
	if !ok {
		t.Fatal("expected task list presentation")
	}
	if present.Summary != "2 tasks" {
		t.Fatalf("unexpected summary: %q", present.Summary)
	}
	if present.PayloadMode != "task_list" {
		t.Fatalf("unexpected payload mode: %q", present.PayloadMode)
	}
}

func TestDescribeToolResultCronCreate(t *testing.T) {
	present, ok := DescribeToolResult(
		"cron_create",
		`{"cron":"*/5 * * * *","prompt":"check status"}`,
		`{"ID":"job-1","CronExpr":"*/5 * * * *","Prompt":"check status","Recurring":true,"NextFire":"2026-05-24T17:30:00+08:00"}`,
		false,
	)
	if !ok {
		t.Fatal("expected cron result presentation")
	}
	if present.Summary != "Scheduled */5 * * * * — job-1" {
		t.Fatalf("unexpected summary: %q", present.Summary)
	}
	if present.PayloadMode != "cron_job" {
		t.Fatalf("unexpected payload mode: %q", present.PayloadMode)
	}
	if want := "Prompt: check status"; !strings.Contains(present.Payload, want) {
		t.Fatalf("payload %q missing %q", present.Payload, want)
	}
}

func TestDescribeToolResultCronDelete(t *testing.T) {
	present, ok := DescribeToolResult(
		"cron_delete",
		`{"jobId":"job-1"}`,
		`Job job-1 deleted`,
		false,
	)
	if !ok {
		t.Fatal("expected cron_delete result presentation")
	}
	if present.Summary != "Deleted job-1" {
		t.Fatalf("unexpected summary: %q", present.Summary)
	}
}

func TestDescribeToolResultCronList(t *testing.T) {
	present, ok := DescribeToolResult(
		"cron_list",
		`{}`,
		"- job-1 [recurring] */5 * * * * next=2026-05-24T17:30:00+08:00\n- job-2 [one-shot] 0 9 * * * next=2026-05-25T09:00:00+08:00\n",
		false,
	)
	if !ok {
		t.Fatal("expected cron_list result presentation")
	}
	if present.Summary != "2 scheduled jobs" {
		t.Fatalf("unexpected summary: %q", present.Summary)
	}
	if present.PayloadMode != "cron_list" {
		t.Fatalf("unexpected payload mode: %q", present.PayloadMode)
	}
}

func TestStartCommandResultText(t *testing.T) {
	tests := []struct {
		name    string
		result  string
		isError bool
		want    string
	}{
		{name: "success empty", result: "", want: "Started"},
		{name: "success snapshot", result: "Job ID: cmd-1\nStatus: running\nDuration: 1s", want: "Started"},
		{name: "failed snapshot", result: "Job ID: cmd-1\nStatus: failed\nError: boom", want: "Failed"},
		{name: "error flag wins", result: "permission denied", isError: true, want: "Failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StartCommandResultText(tt.result, tt.isError); got != tt.want {
				t.Fatalf("StartCommandResultText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSwarmTaskCreateResultMarkdown(t *testing.T) {
	got := SwarmTaskCreateResultMarkdown(`{"ID":"task-1","Subject":"Fix tunnel replay","Description":"## Summary\n- keep markdown"}`)
	if got != "## Summary\n- keep markdown" {
		t.Fatalf("expected extracted markdown description, got %q", got)
	}
}

func TestTeamCreateResultText(t *testing.T) {
	got := TeamCreateResultText(`{"ID":"team-1","Name":"research-squad"}`)
	if got != "Team research-squad Created" {
		t.Fatalf("expected formatted team_create result, got %q", got)
	}
}

func TestPrettifyToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"read_file", "Read File"},
		{"run_command", "Run Command"},
		{"git_status", "Git Status"},
		{"mcp_my_server_tool", "Mcp My Server Tool"},
		{"simple", "Simple"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := prettifyToolName(tt.input); got != tt.want {
				t.Errorf("prettifyToolName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatToolInline(t *testing.T) {
	tests := []struct {
		name, detail, want string
	}{
		{"Read", "/tmp/test.go", "Read /tmp/test.go"},
		{"go test ./...", "", "go test ./..."},
		{"Search", "", "Search"},
		{"", "/tmp/test.go", " /tmp/test.go"},
	}
	for _, tt := range tests {
		got := FormatToolInline(tt.name, tt.detail)
		if got != tt.want {
			t.Errorf("FormatToolInline(%q, %q) = %q, want %q", tt.name, tt.detail, got, tt.want)
		}
	}
}

func TestCompactSingleLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello\nworld", "hello world"},
		{"  lots   of   space  ", "lots of space"},
		{func() string {
			b := make([]byte, 200)
			for i := range b {
				b[i] = 'x'
			}
			return string(b)
		}(), func() string {
			b := make([]byte, 120)
			for i := range b {
				b[i] = 'x'
			}
			return string(b) + "..."
		}()},
	}
	for _, tt := range tests {
		got := compactSingleLine(tt.input)
		if got != tt.want {
			t.Errorf("compactSingleLine() = %q, want %q", got, tt.want)
		}
	}
}

func TestArgStr(t *testing.T) {
	args := map[string]any{
		"str":   "hello",
		"num":   float64(42),
		"bool":  true,
		"array": []any{"a", "b"},
	}
	if got := argStr(args, "str"); got != "hello" {
		t.Errorf("str = %q", got)
	}
	if got := argStr(args, "num"); got != "42" {
		t.Errorf("num = %q", got)
	}
	if got := argStr(args, "bool"); got != "true" {
		t.Errorf("bool = %q", got)
	}
	if got := argStr(args, "array"); got != `["a","b"]` {
		t.Errorf("array = %q", got)
	}
	if got := argStr(args, "missing"); got != "" {
		t.Errorf("missing = %q", got)
	}
	if got := argStr(nil, "any"); got != "" {
		t.Errorf("nil = %q", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "hello", "world"); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("", "  "); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestIsTrivialDetail(t *testing.T) {
	for _, s := range []string{"", "  ", "{}", "[]", "null"} {
		if !isTrivialDetail(s) {
			t.Errorf("expected %q to be trivial", s)
		}
	}
	for _, s := range []string{"hello", `{"a":1}`, "[1]", "false"} {
		if isTrivialDetail(s) {
			t.Errorf("expected %q to NOT be trivial", s)
		}
	}
}

func TestShortenID(t *testing.T) {
	if got := shortenID("12345678"); got != "12345678" {
		t.Errorf("got %q", got)
	}
	if got := shortenID("1234567890ab"); got != "12345678" {
		t.Errorf("got %q", got)
	}
	if got := shortenID("short"); got != "short" {
		t.Errorf("got %q", got)
	}
}

func TestRelativizePath(t *testing.T) {
	tests := []struct {
		path, workDir, want string
	}{
		{"/tmp/project/main.go", "/tmp/project", "main.go"},
		{"/tmp/project/sub/test.go", "/tmp/project", "sub/test.go"},
		{"/other/path/test.go", "/tmp/project", "/other/path/test.go"},
		{"/tmp/test.go", "", "/tmp/test.go"},
	}
	for _, tt := range tests {
		got := RelativizePath(tt.path, tt.workDir)
		if got != tt.want {
			t.Errorf("RelativizePath(%q, %q) = %q, want %q", tt.path, tt.workDir, got, tt.want)
		}
	}
}

func TestParseStringSlice(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		key  string
		want []string
	}{
		{"nil args", nil, "files", nil},
		{"missing key", map[string]any{"x": 1}, "files", nil},
		{"[]string", map[string]any{"files": []string{"a.go", "b.go"}}, "files", []string{"a.go", "b.go"}},
		{"[]any", map[string]any{"files": []any{"a.go", "b.go"}}, "files", []string{"a.go", "b.go"}},
		{"wrong type", map[string]any{"files": "not a slice"}, "files", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStringSlice(tt.args, tt.key)
			if len(got) != len(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestDescribeToolJSONRoundTrip(t *testing.T) {
	// Verify that the presentation output is valid in JSON context
	present := DescribeTool("read_file", `{"path":"/tmp/test.go"}`)
	title := FormatToolInline(present.DisplayName, present.Detail)

	// Should be serializable as JSON string value without issues
	m := map[string]string{"title": title}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["title"] != title {
		t.Errorf("roundtrip mismatch: %q vs %q", decoded["title"], title)
	}
}

func TestDescribeExternalToolCall(t *testing.T) {
	t.Run("prefers descriptive external title", func(t *testing.T) {
		present := DescribeExternalToolCall(
			"tool",
			"Viewing /repo/Makefile",
			`{"filePath":"/repo/Makefile"}`,
		)
		if present.DisplayName != "Viewing /repo/Makefile" {
			t.Fatalf("display = %q", present.DisplayName)
		}
		if present.Detail != "" {
			t.Fatalf("detail = %q, want empty because title already includes it", present.Detail)
		}
	})

	t.Run("ignores generic title", func(t *testing.T) {
		present := DescribeExternalToolCall(
			"bash",
			"bash",
			`{"command":"go test ./..."}`,
		)
		if present.DisplayName != "go test ./..." {
			t.Fatalf("display = %q", present.DisplayName)
		}
		if present.Detail != "" {
			t.Fatalf("detail = %q", present.Detail)
		}
	})
}

func TestDescribeToolResultExternalWrappers(t *testing.T) {
	t.Run("copilot wrapper", func(t *testing.T) {
		present, ok := DescribeToolResult(
			"tool",
			"",
			`{"content":"Makefile","detailedContent":"all:\n\tgo test ./..."}`,
			false,
		)
		if !ok {
			t.Fatal("expected wrapper to be recognized")
		}
		if present.Summary != "Makefile" {
			t.Fatalf("summary = %q", present.Summary)
		}
		if present.Payload != "all:\n\tgo test ./..." {
			t.Fatalf("payload = %q", present.Payload)
		}
		if present.PayloadMode != "text" {
			t.Fatalf("payload mode = %q", present.PayloadMode)
		}
	})

	t.Run("opencode wrapper", func(t *testing.T) {
		present, ok := DescribeToolResult(
			"read",
			"",
			`{"output":"package main\nfunc main() {}\n","metadata":{"preview":"package main"}}`,
			false,
		)
		if !ok {
			t.Fatal("expected wrapper to be recognized")
		}
		if present.Summary != "package main" {
			t.Fatalf("summary = %q", present.Summary)
		}
		if !strings.Contains(present.Payload, "func main") {
			t.Fatalf("payload = %q", present.Payload)
		}
	})
}
