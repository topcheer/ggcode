package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ResolveLang ---

func TestResolveLangTableSa106(t *testing.T) {
	cases := []struct {
		in   string
		want Lang
	}{
		{"zh-CN", LangZhCN},
		{"zh", LangZhCN},
		{"en", LangEn},
		{"", LangEn},
		{"fr", LangEn},
		{"ZH", LangEn}, // case-sensitive by design
	}
	for _, c := range cases {
		if got := ResolveLang(c.in); got != c.want {
			t.Errorf("ResolveLang(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- PlatformDisplayName ---

func TestPlatformDisplayNameTableSa106(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"qq", "QQ"},
		{"feishu", "Feishu"},
		{"telegram", "Telegram"},
		{"discord", "Discord"},
		{"dingtalk", "DingTalk"},
		{"slack", "Slack"},
		{"unknown", "IM"},
		{"", "IM"},
	}
	for _, c := range cases {
		if got := PlatformDisplayName(c.in); got != c.want {
			t.Errorf("PlatformDisplayName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- Tr ---

func TestTrCatalogSa106(t *testing.T) {
	cases := []struct {
		lang Lang
		key  string
		args []any
		want string
	}{
		{LangEn, "follow.user", nil, "User"},
		{LangZhCN, "follow.user", nil, "用户"},
		{LangEn, "daemon.stopped", nil, "ggcode daemon stopped"},
		{LangZhCN, "daemon.stopped", nil, "ggcode daemon 已停止"},
		// Missing zh key falls back to English catalog.
		{LangZhCN, "nonexistent_key", nil, ""},
		{LangEn, "nonexistent_key", nil, ""},
		// Formatting args.
		{LangEn, "daemon.started_bg", []any{42}, "ggcode daemon started in background (PID: 42)"},
		{LangZhCN, "daemon.bg_ok", []any{7}, "已切换到后台 (PID: 7)"},
		{LangEn, "follow.more_lines", []any{3}, "... (3 more lines)"},
	}
	for _, c := range cases {
		if got := Tr(c.lang, c.key, c.args...); got != c.want {
			t.Errorf("Tr(%q, %q) = %q, want %q", c.lang, c.key, got, c.want)
		}
	}
}

// --- summarizeToolResult ---

func TestSummarizeToolResultTableSa106(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{
		{"empty", "", 10, ""},
		{"whitespace only", "  \n \t ", 10, ""},
		{"first meaningful line", "\n\n  hello world  \nsecond", 20, "hello world"},
		{"long line truncated", strings.Repeat("a", 50), 20, strings.Repeat("a", 17) + "..."},
		{"exact length", "abcdef", 6, "abcdef"},
	}
	for _, c := range cases {
		if got := summarizeToolResult(c.in, c.maxLen); got != c.want {
			t.Errorf("%s: summarizeToolResult(%q, %d) = %q, want %q", c.name, c.in, c.maxLen, got, c.want)
		}
	}
}

// --- prettifyToolName ---

func TestPrettifyToolNameTableSa106(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"read_file", "Read File"},
		{"git-status", "Git Status"},
		{"mcp__github__create_issue", "Mcp Github Create Issue"},
		{"run_command", "Run Command"},
		{"", ""},
	}
	for _, c := range cases {
		if got := prettifyToolName(c.in); got != c.want {
			t.Errorf("prettifyToolName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- truncateForTerminal ---

func TestTruncateForTerminalTableSa106(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{
		{"newline conversion", "a\nb", 20, "a\r\nb"},
		{"trim spaces", "  padded  ", 20, "padded"},
		{"no truncation", "short", 20, "short"},
		{"truncated", strings.Repeat("x", 30), 10, strings.Repeat("x", 7) + "..."},
		{"multiline truncated", "aa\nbb", 5, "aa..."}, // \r\n counts 2 toward len
	}
	for _, c := range cases {
		if got := truncateForTerminal(c.in, c.maxLen); got != c.want {
			t.Errorf("%s: truncateForTerminal(%q, %d) = %q, want %q", c.name, c.in, c.maxLen, got, c.want)
		}
	}
}

// --- formatFileBody ---

func TestFormatFileBodyTableSa106(t *testing.T) {
	cases := []struct {
		name    string
		lang    Lang
		result  string
		isError bool
		wantSub string
		empty   bool
	}{
		{"en newline-separator count (no trailing nl)", LangEn, "l1\nl2\nl3", false, "2 lines", false},
		{"en trailing newline not counted", LangEn, "l1\nl2\n", false, "1 lines", false},
		{"zh", LangZhCN, "a\nb", false, "1行", false},
		{"error suppressed", LangEn, "x", true, "", true},
		{"empty result", LangEn, "", false, "", true},
		{"single line", LangEn, "only", false, "0 lines", false},
	}
	for _, c := range cases {
		got := formatFileBody(c.lang, c.result, c.isError)
		if c.empty {
			if got != "" {
				t.Errorf("%s: want empty, got %q", c.name, got)
			}
			continue
		}
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("%s: output %q missing %q", c.name, got, c.wantSub)
		}
	}
}

// --- formatEditBody ---

func TestFormatEditBodyTableSa106(t *testing.T) {
	cases := []struct {
		name    string
		args    string
		wantSub []string
		empty   bool
	}{
		{"single edit", `{"old_text":"a\nb","new_text":"x\ny\nz"}`, []string{"+2", "-1"}, false},
		{"multi edits", `{"edits":[{"old_text":"a","new_text":"b\nc"},{"old_text":"d\ne","new_text":"f"}]}`, []string{"+1", "-1"}, false},
		{"invalid json renders zero delta", `not-json`, []string{"+0", "-0"}, false},
		{"error suppressed", `{}`, nil, true},
	}
	for _, c := range cases {
		got := formatEditBody(LangEn, c.args, false)
		if c.empty {
			if got := formatEditBody(LangEn, c.args, true); got != "" {
				t.Errorf("%s: error path want empty, got %q", c.name, got)
			}
			continue
		}
		for _, sub := range c.wantSub {
			if !strings.Contains(got, sub) {
				t.Errorf("%s: output %q missing %q", c.name, got, sub)
			}
		}
	}
}

// --- formatSearchCount ---

func TestFormatSearchCountTableSa106(t *testing.T) {
	cases := []struct {
		name    string
		lang    Lang
		result  string
		wantSub string
		empty   bool
	}{
		{"showing-of pattern", LangEn, "Showing 3 of 10 matches", "10 matches", false},
		{"found pattern", LangEn, "Found 5 matches", "5 matches", false},
		{"results pattern", LangEn, "1 of 7 results", "7 matches", false},
		{"zh lang", LangZhCN, "Found 5 matches", "5 个匹配", false},
		{"fallback counts non-empty lines", LangEn, "file1.go\nfile2.go\n\n", "2 matches", false},
		{"fallback single line", LangEn, "no hits", "1 matches", false},
		{"zero matches", LangEn, "", "", true},
		{"empty result", LangEn, "", "", true},
	}
	for _, c := range cases {
		got := formatSearchCount(c.lang, c.result, false)
		if c.empty {
			if got != "" {
				t.Errorf("%s: want empty, got %q", c.name, got)
			}
			continue
		}
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("%s: output %q missing %q", c.name, got, c.wantSub)
		}
	}
	if got := formatSearchCount(LangEn, "Found 5 matches", true); got != "" {
		t.Errorf("error path: want empty, got %q", got)
	}
}

// --- formatGitStatus ---

func TestFormatGitStatusTableSa106(t *testing.T) {
	cases := []struct {
		name    string
		result  string
		wantSub []string
		empty   bool
	}{
		{"full counts", "M  a.go\nA  b.go\nD  c.go\n?? d.go\n", []string{"1 modified", "1 added", "1 deleted", "1 untracked"}, false},
		{"single modified", "M  a.go\n", []string{"1 modified"}, false},
		{"clean tree", "", nil, true},
		{"short lines skipped", "M\n?\n", nil, true},
	}
	for _, c := range cases {
		got := formatGitStatus(c.result, false)
		if c.empty {
			if got != "" {
				t.Errorf("%s: want empty, got %q", c.name, got)
			}
			continue
		}
		for _, sub := range c.wantSub {
			if !strings.Contains(got, sub) {
				t.Errorf("%s: output %q missing %q", c.name, got, sub)
			}
		}
	}
	if got := formatGitStatus("M a.go", true); got != "" {
		t.Errorf("error path: want empty, got %q", got)
	}
}

// --- formatGitDiff ---

func TestFormatGitDiffTableSa106(t *testing.T) {
	result := "diff --git a/f b/f\n--- a/f\n+++ b/f\n+added\n-removed\n+added2\n"
	got := formatGitDiff(result, false)
	for _, sub := range []string{"+2", "-1"} {
		if !strings.Contains(got, sub) {
			t.Errorf("output %q missing %q", got, sub)
		}
	}
	if got := formatGitDiff(result, true); got != "" {
		t.Errorf("error path: want empty, got %q", got)
	}
	if got := formatGitDiff("", false); got != "" {
		t.Errorf("empty path: want empty, got %q", got)
	}
}

// --- formatGitLog ---

func TestFormatGitLogTableSa106(t *testing.T) {
	five := "c1\n c2 \nc3\nc4\nc5\n"
	got := formatGitLog(five, false)
	if !strings.Contains(got, "c1") || !strings.Contains(got, "c3") {
		t.Errorf("output %q should include first three commits", got)
	}
	if strings.Contains(got, "c4") {
		t.Errorf("output %q should cap at 3 lines", got)
	}
	if got := formatGitLog("\n\n", false); got != "" {
		t.Errorf("blank-only: want empty, got %q", got)
	}
	if got := formatGitLog("c1", true); got != "" {
		t.Errorf("error path: want empty, got %q", got)
	}
}

// --- formatDaemonCronBody ---

func TestFormatDaemonCronBodyTableSa106(t *testing.T) {
	cases := []struct {
		name    string
		lang    Lang
		result  string
		wantSub []string
		empty   bool
	}{
		{"en full", LangEn, `{"Recurring":true,"Prompt":"run tests","NextFire":"soon"}`, []string{"Recurring: Yes", "Prompt: run tests", "Next Fire: soon"}, false},
		{"zh full", LangZhCN, `{"Recurring":false,"Prompt":"x","NextFire":"y"}`, []string{"循环执行: 否", "任务: x", "下次触发: y"}, false},
		{"invalid json", LangEn, `not-json`, nil, true},
		{"no known fields", LangEn, `{"Other":1}`, nil, true},
		{"recurring non-bool skipped", LangEn, `{"Recurring":"yes","Prompt":"p"}`, []string{"Prompt: p"}, false},
	}
	for _, c := range cases {
		got := formatDaemonCronBody(c.result, c.lang)
		if c.empty {
			if got != "" {
				t.Errorf("%s: want empty, got %q", c.name, got)
			}
			continue
		}
		for _, sub := range c.wantSub {
			if !strings.Contains(got, sub) {
				t.Errorf("%s: output %q missing %q", c.name, got, sub)
			}
		}
	}
}

// --- formatTodoResult ---

func TestFormatTodoResultTableSa106(t *testing.T) {
	valid := `{"todos":[{"id":"1","content":"alpha","status":"done"},{"id":"2","content":"beta","status":"in_progress"},{"id":"3","content":"gamma","status":"pending"}]}`
	got := formatTodoResult(LangEn, valid)
	for _, sub := range []string{"1/3", "✓ alpha", "→ beta", "○ gamma"} {
		if !strings.Contains(got, sub) {
			t.Errorf("output %q missing %q", got, sub)
		}
	}
	if got := formatTodoResult(LangEn, `not-json`); got != "" {
		t.Errorf("invalid json: want empty, got %q", got)
	}
	if got := formatTodoResult(LangEn, `{"todos":[]}`); got != "" {
		t.Errorf("empty todos: want empty, got %q", got)
	}
}

// --- formatMCPToolBody ---

func TestFormatMCPToolBodyTableSa106(t *testing.T) {
	if got := formatMCPToolBody(LangEn, "mcp__x__y", `{}`, "hello preview", false); !strings.Contains(got, "hello preview") {
		t.Errorf("output %q missing preview", got)
	}
	if got := formatMCPToolBody(LangEn, "mcp__x__y", `{}`, "  ", false); got != "" {
		t.Errorf("empty result: want empty, got %q", got)
	}
	// Note: formatMCPToolBody ignores isError and always previews the result.
	if got := formatMCPToolBody(LangEn, "mcp__x__y", `{}`, "boom", true); !strings.Contains(got, "boom") {
		t.Errorf("error path should still preview result, got %q", got)
	}
}

// --- formatSpecialBody (dispatch) ---

func TestFormatSpecialBodyDispatchSa106(t *testing.T) {
	d := NewTerminalFollowDisplay(nil, LangEn, "/tmp/proj", nil)
	cases := []struct {
		name    string
		tool    string
		args    string
		result  string
		wantSub string
		empty   bool
	}{
		{"read_file routes to file body", "read_file", `{}`, "l1\nl2", "1 lines", false},
		{"edit_file routes to edit body", "edit_file", `{"old_text":"a","new_text":"b\nc"}`, "", "+1", false},
		{"list_directory suppressed", "list_directory", `{}`, "x", "", true},
		{"git_status routes", "git_status", `{}`, "M  a.go\n", "1 modified", false},
		{"git_show group suppressed", "git_show", `{}`, "x", "", true},
		{"todo_write routes", "todo_write", `{"todos":[{"id":"1","content":"t","status":"done"}]}`, "", "1/1", false},
		{"run_command suppressed", "run_command", `{}`, "x", "", true},
		{"skill suppressed", "skill", `{}`, "x", "", true},
		{"mcp-style routes to preview", "mcp__srv__tool", `{}`, "mcp body", "mcp body", false},
		{"plain unknown empty", "notes", `{}`, "x", "", true},
	}
	for _, c := range cases {
		got := d.formatSpecialBody(c.tool, c.args, c.result, false)
		if c.empty {
			if got != "" {
				t.Errorf("%s: want empty, got %q", c.name, got)
			}
			continue
		}
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("%s: output %q missing %q", c.name, got, c.wantSub)
		}
	}
	if got := d.formatSpecialBody("read_file", `{}`, "x", true); got != "" {
		t.Errorf("isError path: want empty, got %q", got)
	}
}

// --- TerminalFollowDisplay end-to-end via temp file ---

type fakePresenterSa106 struct{}

func (fakePresenterSa106) Present(toolName, rawArgs string) (string, string, string) {
	return "Fake " + toolName, "detail", "running"
}

func newDisplayForTestSa106(t *testing.T, lang Lang) (*TerminalFollowDisplay, string) {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "follow.out"))
	if err != nil {
		t.Fatalf("create temp out: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return NewTerminalFollowDisplay(f, lang, t.TempDir(), fakePresenterSa106{}), f.Name()
}

func readOutSa106(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	return string(data)
}

func TestFollowDisplayLifecycleSa106(t *testing.T) {
	d, out := newDisplayForTestSa106(t, LangEn)

	d.OnUserMessage("  hi there  ")
	got := readOutSa106(t, out)
	if !strings.Contains(got, "hi there") {
		t.Errorf("OnUserMessage output %q missing text", got)
	}

	// OnToolStatus is intentionally a no-op.
	before := readOutSa106(t, out)
	d.OnToolStatus("run_command", `{}`)
	if readOutSa106(t, out) != before {
		t.Errorf("OnToolStatus should not write output")
	}

	// Hidden tools are fully suppressed.
	for _, tool := range []string{"git_commit", "task_list", "a2a_get_task", "wait_command"} {
		before := readOutSa106(t, out)
		d.OnToolResult(tool, `{}`, "should not appear", false)
		if got := readOutSa106(t, out); got != before {
			t.Errorf("hidden tool %s should produce no output, got %q", tool, got)
		}
	}

	// LSP tools are skipped.
	before = readOutSa106(t, out)
	d.OnToolResult("lsp_diagnostics", `{}`, "x", false)
	if got := readOutSa106(t, out); got != before {
		t.Errorf("lsp tool should produce no output, got %q", got)
	}

	// Regular tool: presenter display name + file body.
	d.OnToolResult("read_file", `{"path":"a.go"}`, "l1\nl2\nl3", false)
	got = readOutSa106(t, out)
	if !strings.Contains(got, "Fake read_file") || !strings.Contains(got, "2 lines") {
		t.Errorf("OnToolResult output %q missing presenter header or file body", got)
	}

	// Error result: header uses error status, no special body.
	d.OnToolResult("read_file", `{}`, "boom", true)
	if got := readOutSa106(t, out); !strings.Contains(got, "Fake read_file") {
		t.Errorf("error result output %q missing header", got)
	}

	// exit_plan_mode renders plan markdown; invalid/empty plan writes nothing.
	before = readOutSa106(t, out)
	d.OnToolResult("exit_plan_mode", `{"plan":""}`, "", false)
	if got := readOutSa106(t, out); got != before {
		t.Errorf("empty plan should write nothing, got %q", got)
	}
	d.OnToolResult("exit_plan_mode", `{"plan":"# Step 1\n- do thing"}`, "", false)
	if got := readOutSa106(t, out); !strings.Contains(got, "Step") || !strings.Contains(got, "thing") {
		t.Errorf("plan output %q missing rendered plan", got)
	}

	// Hidden tools stay suppressed even after other activity.
	before = readOutSa106(t, out)
	d.OnToolResult("task_create", `{"subject":"s"}`, `{"ok":true}`, false)
	if got := readOutSa106(t, out); got != before {
		t.Errorf("task_create is hidden and should write nothing, got %q", got)
	}

	// Streaming accumulation and round completion.
	d.OnStreamText("hello ")
	d.OnStreamText("world")
	d.OnRoundDone()
	if got := readOutSa106(t, out); !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Errorf("OnRoundDone output %q missing accumulated text", got)
	}
	// Empty round writes nothing.
	before = readOutSa106(t, out)
	d.OnRoundDone()
	if got := readOutSa106(t, out); got != before {
		t.Errorf("empty round should write nothing, got %q", got)
	}

	// OnError flushes half-streamed text (#1535) and prints the error.
	d.OnStreamText("partial ")
	d.OnError(os.ErrPermission)
	got = readOutSa106(t, out)
	if !strings.Contains(got, "❌") {
		t.Errorf("OnError output %q missing error marker", got)
	}
	d.OnStreamText("fresh")
	d.OnRoundDone()
	if got := readOutSa106(t, out); strings.Contains(got, "partial") {
		t.Errorf("stale partial text leaked into next round: %q", got)
	}

	// Pairing challenge (bind + rebind) and resolved.
	d.OnPairingChallenge("qq", "chan-1", "1234", "bind")
	d.OnPairingChallenge("qq", "chan-1", "5678", "rebind")
	d.OnPairingResolved()
	got = readOutSa106(t, out)
	for _, sub := range []string{"binding requested from channel chan-1", "rebind requested", "1   2   3   4", "Pairing resolved"} {
		if !strings.Contains(got, sub) {
			t.Errorf("pairing output %q missing %q", got, sub)
		}
	}

	d.Close() // must be safe to call
}

func TestFollowDisplayNilPresenterFallsBackSa106(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "follow.out"))
	if err != nil {
		t.Fatalf("create temp out: %v", err)
	}
	defer f.Close()
	d := NewTerminalFollowDisplay(f, LangEn, "", nil)
	d.OnToolResult("custom_tool", `{}`, "body", false)
	data, _ := os.ReadFile(f.Name())
	if got := string(data); !strings.Contains(got, "Custom Tool") {
		t.Errorf("nil presenter should fall back to prettifyToolName, got %q", got)
	}
}
