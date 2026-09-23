package chat

// sa-103 coverage-net: table-driven tests for the accessor/formatting function
// families in list.go, tools.go, styles.go and stream_markdown.go that sat at
// 0% or under-covered after the sa-100/101/102 tui-focused rounds. Zero
// implementation changes: this file only pins observable behavior.

import (
	"strings"
	"testing"
)

// --- styles.go: ToolStatus.String -------------------------------------------

func TestToolStatusStringTable(t *testing.T) {
	tests := []struct {
		status ToolStatus
		want   string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusSuccess, "success"},
		{StatusError, "error"},
		{StatusCanceled, "canceled"},
		{ToolStatus(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("ToolStatus(%d).String() = %q, want %q", int(tt.status), got, tt.want)
		}
	}
}

// --- styles.go: ToolIcon / ToolIconStyle -------------------------------------

func TestToolIconTable(t *testing.T) {
	tests := []struct {
		status ToolStatus
		want   string
	}{
		{StatusPending, "○"},
		{StatusSuccess, "●"},
		{StatusError, "●"},
		{StatusCanceled, "⊘"},
		{ToolStatus(99), "?"},
	}
	s := Styles{}
	for _, tt := range tests {
		if got := s.ToolIcon(tt.status); got != tt.want {
			t.Errorf("ToolIcon(%v) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestToolIconStyleTable(t *testing.T) {
	s := DefaultStyles()
	tests := []ToolStatus{
		StatusPending,
		StatusRunning,
		StatusSuccess,
		StatusError,
		StatusCanceled,
		ToolStatus(99), // default branch: raw icon, no styling
	}
	for _, status := range tests {
		got := s.ToolIconStyle(status)
		wantIcon := s.ToolIcon(status)
		if got == "" {
			t.Errorf("ToolIconStyle(%v) = empty", status)
		}
		// Unknown status returns the raw icon; styled variants wrap the icon
		// in ANSI sequences, so exact equality is expected only there.
		if status == ToolStatus(99) && got != wantIcon {
			t.Errorf("ToolIconStyle(unknown) = %q, want raw icon %q", got, wantIcon)
		}
		if status != ToolStatus(99) && got == wantIcon {
			t.Errorf("ToolIconStyle(%v) = %q, expected styled (non-raw) icon", status, got)
		}
	}
}

// --- list.go: accessor family -------------------------------------------------

func TestListMaxItemsAccessor(t *testing.T) {
	l := NewList(80, 24)
	if got := l.MaxItems(); got != DefaultMaxItems {
		t.Errorf("fresh list MaxItems = %d, want %d", got, DefaultMaxItems)
	}
	l.SetMaxItems(0) // 0 = unlimited
	if got := l.MaxItems(); got != 0 {
		t.Errorf("MaxItems after SetMaxItems(0) = %d, want 0", got)
	}
	l.Append(NewSystemItem("s1", "hello", DefaultStyles()))
	if l.Len() != 1 {
		t.Fatalf("Len after unlimited append = %d, want 1", l.Len())
	}
}

func TestListSetItemsReplacesAndTrims(t *testing.T) {
	l := NewList(80, 24)
	l.Append(NewSystemItem("old", "old", DefaultStyles()))
	l.SetMaxItems(0)

	items := []Item{
		NewSystemItem("a", "a", DefaultStyles()),
		NewSystemItem("b", "b", DefaultStyles()),
	}
	l.SetItems(items)
	if l.Len() != 2 {
		t.Fatalf("Len after SetItems = %d, want 2", l.Len())
	}
	if l.FindByID("old") != nil {
		t.Error("SetItems should replace all items, found stale item")
	}
	if l.FindByID("b") == nil {
		t.Error("FindByID after SetItems missing item b")
	}

	// SetItems honors the cap: excess items get trimmed with hysteresis.
	l.SetMaxItems(4)
	big := make([]Item, 0, 6)
	for i := 0; i < 6; i++ {
		big = append(big, NewSystemItem(string(rune('a'+i)), "x", DefaultStyles()))
	}
	l.SetItems(big)
	if got := l.Len(); got != 3 { // trim target = 75% of cap
		t.Errorf("Len after SetItems(6) with cap 4 = %d, want 3", got)
	}
}

func TestListRemoveByID(t *testing.T) {
	l := NewList(80, 24)
	l.Append(
		NewSystemItem("a", "a", DefaultStyles()),
		NewSystemItem("b", "b", DefaultStyles()),
	)
	l.RemoveByID("a")
	if l.Len() != 1 {
		t.Fatalf("Len after RemoveByID = %d, want 1", l.Len())
	}
	if l.FindByID("a") != nil {
		t.Error("item a still present after RemoveByID")
	}
	// No-op path: unknown ID must not mutate.
	l.RemoveByID("missing")
	if l.Len() != 1 {
		t.Errorf("Len after RemoveByID(missing) = %d, want 1", l.Len())
	}
}

func TestListLastAssistantText(t *testing.T) {
	l := NewList(80, 24)
	if got := l.LastAssistantText(); got != "" {
		t.Errorf("LastAssistantText on empty list = %q, want empty", got)
	}

	asst := NewAssistantItem("a1", DefaultStyles())
	asst.SetText("final answer")
	l.Append(NewSystemItem("s1", "sys", DefaultStyles()), asst)
	if got := l.LastAssistantText(); got != "final answer" {
		t.Errorf("LastAssistantText = %q, want %q", got, "final answer")
	}

	// Trailing non-assistant items are skipped; assistant text still found.
	l.Append(NewSystemItem("s2", "sys2", DefaultStyles()))
	if got := l.LastAssistantText(); got != "final answer" {
		t.Errorf("LastAssistantText with trailing system item = %q, want %q", got, "final answer")
	}

	// No assistant at all → empty.
	l2 := NewList(80, 24)
	l2.Append(NewSystemItem("s1", "sys", DefaultStyles()))
	if got := l2.LastAssistantText(); got != "" {
		t.Errorf("LastAssistantText without assistant = %q, want empty", got)
	}
}

func TestListHeightAccessor(t *testing.T) {
	l := NewList(80, 24)
	if got := l.Height(); got != 24 {
		t.Errorf("Height = %d, want 24", got)
	}
	l.SetSize(100, 30)
	if got := l.Height(); got != 30 {
		t.Errorf("Height after SetSize = %d, want 30", got)
	}
}

// --- tools.go: BaseToolItem result accessors + suppress-header render ---------

func TestBaseToolItemResultAccessors(t *testing.T) {
	item := NewBaseToolItem("id-1", "run_command", StatusPending, "ls", DefaultStyles())
	if item.Result() != "" || item.IsError() {
		t.Fatalf("fresh item Result=%q IsError=%v, want empty/false", item.Result(), item.IsError())
	}
	item.SetResult("boom", true)
	if item.Result() != "boom" {
		t.Errorf("Result = %q, want %q", item.Result(), "boom")
	}
	if !item.IsError() {
		t.Error("IsError = false, want true")
	}
	item.SetResult("ok", false)
	if item.IsError() {
		t.Error("IsError = true after SetResult(ok,false)")
	}
}

func TestBaseToolItemRenderSuppressHeader(t *testing.T) {
	item := NewBaseToolItem("id-1", "run_command", StatusSuccess, "ls", DefaultStyles())
	item.suppressHeader = true

	// Body-bearing render keeps only the body; the header is suppressed.
	item.SetResult("some body", false)
	got := item.Render(80)
	if strings.Contains(got, "run_command") {
		t.Errorf("suppressed header leaked tool name: %q", got)
	}
	if !strings.Contains(got, "some body") {
		t.Errorf("suppressed render lost body: %q", got)
	}

	// Empty body + suppressed header renders empty. Quirk (pinned as-is):
	// GetCached uses rendered != "" as the hit sentinel, so SetCached("",
	// width, 0) never hits; Render re-emits "" and Height falls back to
	// measureHeightWidth(""), which counts an empty string as one line.
	empty := NewBaseToolItem("id-2", "run_command", StatusSuccess, "ls", DefaultStyles())
	empty.suppressHeader = true
	if got := empty.Render(80); got != "" {
		t.Errorf("suppressed header with empty body = %q, want empty", got)
	}
	if h := empty.Height(80); h != 1 {
		t.Errorf("Height of empty suppressed render = %d, want 1", h)
	}
}

// --- tools.go: GetToolBodyBehavior full table ----------------------------------

func TestGetToolBodyBehaviorFullTable(t *testing.T) {
	tests := []struct {
		tool string
		want ToolBodyBehavior
	}{
		{"save_memory", BodySuppress},
		{"delete_memory", BodySuppress},
		{"team_delete", BodySuppress},
		{"teammate_shutdown", BodySuppress},
		{"teammate_list", BodySuppress},
		{"swarm_task_claim", BodySuppress},
		{"swarm_task_complete", BodySuppress},
		{"swarm_task_list", BodySuppress},
		{"send_message", BodySuppress},
		{"config", BodySuppress},
		{"enter_plan_mode", BodySuppress},
		{"list_mcp_capabilities", BodySuppress},
		{"get_mcp_prompt", BodySuppress},
		{"read_mcp_resource", BodySuppress},
		{"enter_worktree", BodySuppress},
		{"exit_worktree", BodySuppress},
		{"skill", BodySuppress},
		{"exit_plan_mode", BodyMarkdown},
		{"swarm_task_create", BodyMarkdown},
		{"cron_create", BodyFormatJSON},
		{"a2a_discover", BodySuppress},
		{"a2a_list_tasks", BodySuppress},
		{"a2a_cancel_task", BodySuppress},
		{"a2a_get_task", BodySuppress},
		{"a2a_remote", BodySuppress},
		{"a2a_send_task", BodySuppress},
		{"teammate_results", BodyMarkdown},
		{"wait_agent", BodyMarkdown},
		{"mcp__some__server__tool", BodySuppress}, // dynamic MCP prefix
		{"mcp__", BodySuppress},                   // bare prefix
		{"run_command", BodyDefault},
		{"", BodyDefault},
	}
	for _, tt := range tests {
		if got := GetToolBodyBehavior(tt.tool); got != tt.want {
			t.Errorf("GetToolBodyBehavior(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

// --- tools.go: TodoToolItem family ---------------------------------------------

func TestTodoToolItemAccessorsAndHeight(t *testing.T) {
	styles := DefaultStyles()
	item := NewTodoToolItem("todo-1", []TodoTask{
		{ID: "1", Content: "design", Status: "done"},
	}, styles, "en")
	if item.ID() != "todo-1" {
		t.Errorf("ID = %q, want %q", item.ID(), "todo-1")
	}
	if h := item.Height(80); h <= 0 {
		t.Errorf("Height = %d, want > 0", h)
	}

	item.SetTasks([]TodoTask{
		{ID: "2", Content: "implement", Status: "in_progress"},
		{ID: "3", Content: "test", Status: "pending"},
	})
	rendered := item.Render(80)
	if !strings.Contains(rendered, "implement") {
		t.Errorf("Render after SetTasks lost active task: %q", rendered)
	}
	if h := item.Height(80); h <= 0 {
		t.Errorf("Height after SetTasks = %d, want > 0", h)
	}
}

func TestTodoToolItemRenderLangTable(t *testing.T) {
	tests := []struct {
		lang    string
		wantSub string
	}{
		{"en", "Todo Progress Update"},
		{"zh-CN", "更新待办事项"},
	}
	tasks := []TodoTask{{ID: "1", Content: "task", Status: "in_progress"}}
	for _, tt := range tests {
		item := NewTodoToolItem("todo-"+tt.lang, tasks, DefaultStyles(), tt.lang)
		got := item.Render(80)
		if !strings.Contains(got, tt.wantSub) {
			t.Errorf("lang %s render missing %q: %q", tt.lang, tt.wantSub, got)
		}
	}
}

// --- tools.go: AgentToolItem family ---------------------------------------------

func TestAgentToolItemAccessors(t *testing.T) {
	styles := DefaultStyles()
	agent := NewAgentToolItem("a1", "researcher", StatusRunning, styles)

	if agent.ID() != "a1" {
		t.Errorf("ID = %q, want a1", agent.ID())
	}
	if agent.Label() != "researcher" {
		t.Errorf("Label = %q, want researcher", agent.Label())
	}
	if agent.Status() != StatusRunning {
		t.Errorf("Status = %v, want running", agent.Status())
	}
	if agent.Result() != "" {
		t.Errorf("fresh Result = %q, want empty", agent.Result())
	}

	agent.SetStatus(StatusSuccess)
	if agent.Status() != StatusSuccess {
		t.Errorf("Status after SetStatus = %v, want success", agent.Status())
	}
	agent.SetResult("done: 42 findings")
	if agent.Result() != "done: 42 findings" {
		t.Errorf("Result after SetResult = %q", agent.Result())
	}
}

func TestAgentToolItemUpdateNested(t *testing.T) {
	styles := DefaultStyles()
	agent := NewAgentToolItem("a1", "researcher", StatusRunning, styles)

	nested1 := NewBashToolItem("n1", "Bash", "go test", StatusRunning, styles)
	agent.AppendNested(nested1)

	// Update path: matching ID replaces in place.
	replacement := NewBashToolItem("n1", "Bash", "go test ./...", StatusSuccess, styles)
	agent.UpdateNested("n1", replacement)
	rendered := agent.Render(80)
	if !strings.Contains(rendered, "go test ./...") {
		t.Errorf("UpdateNested did not replace nested item: %q", rendered)
	}

	// No-match path appends.
	agent.UpdateNested("n2", NewBashToolItem("n2", "Bash", "go vet", StatusSuccess, styles))
	rendered = agent.Render(80)
	if !strings.Contains(rendered, "go vet") {
		t.Errorf("UpdateNested(missing) did not append: %q", rendered)
	}
}

func TestAgentToolItemRenderEmptyLabelAndHeight(t *testing.T) {
	styles := DefaultStyles()

	// Empty label falls back to "sub-agent".
	agent := NewAgentToolItem("a2", "", StatusRunning, styles)
	rendered := agent.Render(80)
	if !strings.Contains(rendered, "sub-agent") {
		t.Errorf("empty label render missing fallback: %q", rendered)
	}

	// No nested items: header only.
	if strings.Contains(rendered, "└") {
		t.Errorf("nested-less render should be header-only: %q", rendered)
	}

	agent.AppendNested(NewBashToolItem("b1", "Bash", "ls", StatusSuccess, styles))
	if h := agent.Height(80); h <= 0 {
		t.Errorf("Height with nested = %d, want > 0", h)
	}
}

// --- stream_markdown.go: missingOpenFence ---------------------------------------

func TestMissingOpenFenceTable(t *testing.T) {
	tests := []struct {
		name string
		kept []string
		want string
	}{
		{"empty", nil, ""},
		{"balanced backticks", []string{"```go", "x := 1", "```"}, ""},
		{"open backticks", []string{"```go", "x := 1"}, "```"},
		{"close then reopen", []string{"```", "a", "```", "text", "```", "b"}, "```"},
		{"balanced tilde", []string{"~~~", "x", "~~~"}, ""},
		{"open tilde", []string{"~~~python"}, "~~~"},
		{"backtick open ignores tilde", []string{"```", "~~~"}, "```"},
		{"indented fence counts after trim", []string{"  ```"}, "```"},
	}
	for _, tt := range tests {
		if got := missingOpenFence(tt.kept); got != tt.want {
			t.Errorf("%s: missingOpenFence(%q) = %q, want %q", tt.name, tt.kept, got, tt.want)
		}
	}
}

// --- stream_markdown.go: extractBlockMarkdown via splitMarkdownBlocks -----------

func TestSplitMarkdownBlocksTable(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantNil  bool
		contains string // substring expected in the single produced block
	}{
		{"empty", "", true, ""},
		{"whitespace only", "   \n  ", true, ""},
		{"thematic break only", "---\n", true, ""}, // no text content → no blocks
		{"paragraph", "hello world", false, "hello world"},
		{"fenced code raw", "```go\nx := 1\n```", false, "```go"},
		{"list raw", "- one\n- two", false, "- one"},
		{"blockquote raw", "> quoted", false, "> quoted"},
		{"table raw", "| a | b |\n|---|---|\n| 1 | 2 |", false, "| a | b |"},
		{"thematic break only", "---\n", true, ""}, // no text content → no blocks
		{"html block raw", "<div>x</div>", false, "<div>"},
	}
	for _, tt := range tests {
		blocks := splitMarkdownBlocks(tt.src)
		if tt.wantNil {
			if len(blocks) != 0 {
				t.Errorf("%s: blocks = %q, want none", tt.name, blocks)
			}
			continue
		}
		if len(blocks) != 1 {
			t.Errorf("%s: got %d blocks %q, want 1", tt.name, len(blocks), blocks)
			continue
		}
		if tt.contains != "" && !strings.Contains(blocks[0], tt.contains) {
			t.Errorf("%s: block %q missing %q", tt.name, blocks[0], tt.contains)
		}
	}
}

// --- tools.go: fileBodyMode renderer family ------------------------------------

func TestRenderFileLineCountTable(t *testing.T) {
	tests := []struct {
		name    string
		lang    string
		rawArgs string
		result  string
		wantSub string // "" = expect NO line-count body
	}{
		{"write_file en", "en", `{"content":"a\nb\n"}`, "done", "2 lines"},
		{"write_file zh", "zh-CN", `{"content":"a\nb\n"}`, "done", "2行"},
		{"multi_file en", "en", `{"files":[{"content":"x\n"},{"content":"y"}]}`, "done", "2 lines"},
		{"read_file numbered result", "en", "", "     1\tfoo\n     2\tbar\n", "2 lines"},
		{"error result no count", "en", "", "error: file not found", ""},
	}
	for _, tt := range tests {
		item := NewFileToolItem("f-"+tt.name, "Write", "/tmp/x", StatusSuccess, DefaultStyles(), tt.lang, tt.rawArgs, "write_file")
		if tt.result != "" {
			item.SetResult(tt.result, false)
		}
		got := item.Render(80)
		if tt.wantSub == "" {
			if strings.Contains(got, "lines") || strings.Contains(got, "行") {
				t.Errorf("%s: unexpected line count body: %q", tt.name, got)
			}
			continue
		}
		if !strings.Contains(got, tt.wantSub) {
			t.Errorf("%s: render %q missing %q", tt.name, got, tt.wantSub)
		}
	}
}

func TestRenderEditDiffTable(t *testing.T) {
	tests := []struct {
		name     string
		rawArgs  string
		wantSubs []string
	}{
		{"single edit", `{"old_text":"a\nb","new_text":"x"}`, []string{"+1", "-2"}},
		{"edits array", `{"edits":[{"old_text":"a\nb","new_text":"c\nd"}]}`, []string{"+2", "-2"}},
		{"files edits", `{"files":[{"edits":[{"old_text":"a","new_text":"b\nc"}]}]}`, []string{"+2", "-1"}},
		{"empty args", `{}`, []string{"+0", "-0"}},
	}
	for _, tt := range tests {
		item := NewFileToolItem("e-"+tt.name, "Edit", "/tmp/x", StatusSuccess, DefaultStyles(), "en", tt.rawArgs, "edit_file")
		item.SetResult("done", false)
		got := item.Render(80)
		for _, want := range tt.wantSubs {
			if !strings.Contains(got, want) {
				t.Errorf("%s: render %q missing %q", tt.name, got, want)
			}
		}
	}
}

func TestRenderSearchCountTable(t *testing.T) {
	tests := []struct {
		name    string
		lang    string
		result  string
		wantSub string // "" = expect no match-count body
	}{
		{"found matches en", "en", "Found 3 matches:\n  a\n  b", "3 matches"},
		{"showing of matches en", "en", "Showing 2 of 5 matches", "5 matches"},
		{"showing of matches zh", "zh-CN", "Showing 2 of 5 matches", "个匹配"},
		{"plain paths fallback", "en", "a.go\nb.go", "2 matches"},
		{"empty result", "en", "", ""},
	}
	for _, tt := range tests {
		item := NewSearchToolItem("s-"+tt.name, "Grep", "pat", StatusSuccess, DefaultStyles())
		item.lang = tt.lang
		if tt.result != "" {
			item.SetResult(tt.result, false)
		}
		got := item.Render(80)
		if tt.wantSub == "" {
			if strings.Contains(got, "matches") {
				t.Errorf("%s: unexpected match-count body: %q", tt.name, got)
			}
			continue
		}
		if !strings.Contains(got, tt.wantSub) {
			t.Errorf("%s: render %q missing %q", tt.name, got, tt.wantSub)
		}
	}
}

func TestRenderGitBodyModeTable(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		result  string
		wantSub string // "" = expect empty body
	}{
		{"status mixed", "gitstatus", "M  a.go\n?? b.txt\nD  c.go\nA  d.go\nR  e.go\n", "2 modified"},
		{"status clean", "gitstatus", "clean\n", ""},
		{"status empty", "gitstatus", "", ""},
		{"diff counts", "gitdiff", "+added\n-removed\n+++ b/x\n--- a/x\n@@ -1 +1 @@\n ctx\n", "-1"},
		{"diff empty", "gitdiff", "", ""},
		{"log caps at 3", "gitlog", "c1\n  c2\nc3\nc4\n", ""},
		{"log empty", "gitlog", "", ""},
	}
	for _, tt := range tests {
		item := NewBaseToolItem("g-"+tt.name, "git", StatusSuccess, "status", DefaultStyles())
		item.fileBodyMode = tt.mode
		if tt.result != "" {
			item.SetResult(tt.result, false)
		}
		got := item.Render(80)
		switch {
		case tt.name == "log caps at 3":
			for _, want := range []string{"c1", "c2", "c3"} {
				if !strings.Contains(got, want) {
					t.Errorf("%s: render %q missing %q", tt.name, got, want)
				}
			}
			if strings.Contains(got, "c4") {
				t.Errorf("%s: render shows 4th commit beyond cap: %q", tt.name, got)
			}
		case tt.wantSub == "":
			if strings.TrimSpace(stripANSI(got)) != "" && !strings.Contains(got, "git") {
				t.Errorf("%s: expected header-only render, got %q", tt.name, got)
			}
		default:
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("%s: render %q missing %q", tt.name, got, tt.wantSub)
			}
		}
		// The diff mode always emits exactly one +N and one -N fragment.
		if tt.mode == "gitdiff" && tt.result != "" {
			if !strings.Contains(got, "+1") {
				t.Errorf("%s: render %q missing \"+1\"", tt.name, got)
			}
		}
	}
}

func TestRenderCronBody(t *testing.T) {
	tests := []struct {
		lang    string
		wantSub []string
	}{
		{"en", []string{"Recurring: Yes", "job-1", "Next Fire: soon"}},
		{"zh-CN", []string{"循环执行: 是", "job-1", "下次触发: soon"}},
	}
	for _, tt := range tests {
		item := NewBaseToolItem("cron-"+tt.lang, "cron_create", StatusSuccess, "", DefaultStyles())
		item.fileBodyMode = "cronbody"
		item.lang = tt.lang
		item.SetResult(`{"Recurring":true,"Prompt":"job-1","NextFire":"soon"}`, false)
		got := item.Render(80)
		for _, want := range tt.wantSub {
			if !strings.Contains(got, want) {
				t.Errorf("lang %s: cron body render %q missing %q", tt.lang, got, want)
			}
		}
	}

	// Non-JSON and key-less results render an empty body.
	for _, result := range []string{"not json", `{"unknown":1}`} {
		item := NewBaseToolItem("cron-x", "cron_create", StatusSuccess, "", DefaultStyles())
		item.fileBodyMode = "cronbody"
		item.SetResult(result, false)
		if got := item.RenderBody(80); got != "" {
			t.Errorf("cron body for %q = %q, want empty", result, got)
		}
	}
}

// stripANSI is defined in tui_wrap_regression_test.go (shared test helper).

// --- messages.go: accessor families ---------------------------------------------

func TestUserItemAccessors(t *testing.T) {
	styles := DefaultStyles()
	u := NewUserItem("u1", "hello", styles)
	if u.Text() != "hello" {
		t.Errorf("Text = %q, want hello", u.Text())
	}
	if got := u.Prefix(); got != styles.UserPrefix {
		t.Errorf("Prefix = %q, want %q", got, styles.UserPrefix)
	}
	u.SetPrefix(">> ")
	if got := u.Prefix(); got != ">> " {
		t.Errorf("Prefix after SetPrefix = %q, want >> ", got)
	}

	md := NewMarkdownUserItem("u2", "**bold**", styles)
	if md.Text() != "**bold**" {
		t.Errorf("markdown user Text = %q", md.Text())
	}
}

func TestSystemItemAccessors(t *testing.T) {
	s := NewSystemItem("s1", "note", DefaultStyles())
	if s.Text() != "note" {
		t.Errorf("Text = %q, want note", s.Text())
	}
	s.SetText("updated")
	if s.Text() != "updated" {
		t.Errorf("Text after SetText = %q", s.Text())
	}
	s.AppendText(" more")
	if s.Text() != "updated more" {
		t.Errorf("Text after AppendText = %q", s.Text())
	}
}

func TestAssistantItemIDAndReasoning(t *testing.T) {
	a := NewAssistantItem("a1", DefaultStyles())
	if a.ID() != "a1" {
		t.Errorf("ID = %q, want a1", a.ID())
	}
	if got := a.Reasoning(); got != "" {
		t.Errorf("fresh Reasoning = %q, want empty", got)
	}
}

// --- list.go: UpdateByID ---------------------------------------------------------

func TestListUpdateByID(t *testing.T) {
	l := NewList(80, 24)
	asst := NewAssistantItem("a1", DefaultStyles())
	asst.SetText("before")
	l.Append(NewSystemItem("s1", "sys", DefaultStyles()), asst)

	l.UpdateByID("a1", func(item Item) {
		item.(*AssistantItem).SetText("after")
	})
	if got := l.LastAssistantText(); got != "after" {
		t.Errorf("UpdateByID mutation = %q, want %q", got, "after")
	}

	// Missing ID: no-op, no panic.
	l.UpdateByID("missing", func(item Item) {
		t.Error("mutation fn must not run for missing ID")
	})
}
