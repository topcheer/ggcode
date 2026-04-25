package chat

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestCachedItem(t *testing.T) {
	var c CachedItem
	if _, _, ok := c.GetCached(80); ok {
		t.Fatal("expected cache miss")
	}
	c.SetCached("hello", 80, 1)
	got, h, ok := c.GetCached(80)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}
	if h != 1 {
		t.Fatalf("expected height 1, got %d", h)
	}
	if _, _, ok := c.GetCached(60); ok {
		t.Fatal("expected cache miss for different width")
	}
	c.Invalidate()
	if _, _, ok := c.GetCached(80); ok {
		t.Fatal("expected cache miss after invalidate")
	}
}

func TestMeasureHeight(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"hello", 1},
		{"hello\nworld", 2},
		{"a\nb\nc\n", 3},
		{"", 1},
	}
	for _, tt := range tests {
		got := measureHeight(tt.input)
		if got != tt.want {
			t.Errorf("measureHeight(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestWrapLines(t *testing.T) {
	tests := []struct {
		text  string
		width int
		want  int // expected number of lines
	}{
		{"short", 80, 1},
		{"short", 3, 2},         // "sho" + "rt"
		{"a b c d", 5, 2},       // "a b c" + "d" (wraps at space)
		{"line1\nline2", 80, 2}, // preserves explicit newlines
		{"", 80, 1},             // empty = one empty line
	}
	for _, tt := range tests {
		lines := wrapLines(tt.text, tt.width)
		if len(lines) != tt.want {
			t.Errorf("wrapLines(%q, %d) = %d lines, want %d: %v", tt.text, tt.width, len(lines), tt.want, lines)
		}
	}
}

func TestWrapLinesMultibyte(t *testing.T) {
	t.Run("pure CJK no panic", func(t *testing.T) {
		// Pure Chinese text, each rune is ~2 cells wide.
		// Small width forces multiple wraps — must not panic.
		text := "你好世界这是一段用于测试多字节文字换行的中文文本内容"
		lines := wrapLines(text, 10)
		if len(lines) < 2 {
			t.Errorf("expected multiple lines for long CJK text, got %d: %v", len(lines), lines)
		}
		for _, l := range lines {
			if w := lipgloss.Width(l); w > 12 { // allow a little slack for a single wide char overshoot
				t.Errorf("line too wide (width=%d): %q", w, l)
			}
		}
	})

	t.Run("CJK with spaces no panic", func(t *testing.T) {
		// The exact pattern that caused the original panic:
		// multi-byte chars + spaces + small width → byte index used as rune index.
		text := "你好 世界 测试 多字节 文本 换行 功能 是否 正常 工作"
		lines := wrapLines(text, 10)
		if len(lines) < 2 {
			t.Errorf("expected multiple lines, got %d: %v", len(lines), lines)
		}
		for _, l := range lines {
			if w := lipgloss.Width(l); w > 12 {
				t.Errorf("line too wide (width=%d): %q", w, l)
			}
		}
	})

	t.Run("mixed ASCII and CJK", func(t *testing.T) {
		text := "Hello 你好 World 世界 Test 测试 a b c d e f g h i j k"
		lines := wrapLines(text, 14)
		if len(lines) < 2 {
			t.Errorf("expected wrapping, got %d lines: %v", len(lines), lines)
		}
	})

	t.Run("very narrow width with CJK", func(t *testing.T) {
		// Width so small that even a single CJK char (2 cells) exceeds it
		text := "你好世界"
		lines := wrapLines(text, 1)
		// Each character should be emitted on its own line
		if len(lines) != 4 {
			t.Errorf("expected 4 lines for 4 CJK chars at width 1, got %d: %v", len(lines), lines)
		}
	})

	t.Run("emoji wide characters", func(t *testing.T) {
		text := "🎉🎊🎈🎁🎀🎊🎉 balloon party time is here"
		lines := wrapLines(text, 12)
		if len(lines) < 2 {
			t.Errorf("expected wrapping, got %d lines: %v", len(lines), lines)
		}
	})
}

func TestUserItemRender(t *testing.T) {
	styles := DefaultStyles()
	item := NewUserItem("u1", "hello world", styles)
	rendered := item.Render(80)
	if !strings.Contains(rendered, "hello world") {
		t.Fatalf("expected content in render, got: %s", rendered)
	}
	// Prefix is now styled with ANSI — check for the icon character
	if !strings.Contains(rendered, "❯") {
		t.Fatalf("expected ❯ icon in render, got: %s", rendered)
	}
}

func TestAssistantItemStreaming(t *testing.T) {
	styles := DefaultStyles()
	item := NewAssistantItem("a1", styles)
	item.SetText("hello")
	r1 := item.Render(80)
	if !strings.Contains(r1, "hello") {
		t.Fatalf("expected 'hello', got: %s", r1)
	}

	item.SetText("hello world")
	r2 := item.Render(80)
	if !strings.Contains(r2, "hello world") {
		t.Fatalf("expected 'hello world', got: %s", r2)
	}
}

func TestToolHeader(t *testing.T) {
	styles := DefaultStyles()
	header := styles.ToolHeader(StatusSuccess, "Bash", 80, "go build ./...")
	if !strings.Contains(header, "Bash") {
		t.Fatalf("expected tool name in header: %s", header)
	}
	if !strings.Contains(header, "go build") {
		t.Fatalf("expected params in header: %s", header)
	}
	if !strings.Contains(header, "✓") {
		t.Fatalf("expected success icon: %s", header)
	}
}

func TestToolHeaderTruncation(t *testing.T) {
	styles := DefaultStyles()

	t.Run("long command truncated with ellipsis", func(t *testing.T) {
		longCmd := strings.Repeat("x", 200)
		header := styles.ToolHeader(StatusSuccess, "Bash", 80, longCmd)
		if w := lipgloss.Width(header); w > 80 {
			t.Fatalf("header width %d exceeds 80", w)
		}
		clean := stripTestAnsi(header)
		if !strings.HasSuffix(clean, "…") {
			t.Fatalf("long command should end with …, got: %q", clean)
		}
	})

	t.Run("long path truncated from head", func(t *testing.T) {
		longPath := "/very/long/path/to/some/deeply/nested/directory/structure/with/many/components/file.go"
		header := styles.ToolHeader(StatusSuccess, "Read", 80, longPath)
		if w := lipgloss.Width(header); w > 80 {
			t.Fatalf("header width %d exceeds 80", w)
		}
		clean := stripTestAnsi(header)
		if !strings.Contains(clean, "file.go") {
			t.Fatalf("should keep filename, got: %q", clean)
		}
	})

	t.Run("short params not truncated", func(t *testing.T) {
		header := styles.ToolHeader(StatusSuccess, "Bash", 80, "go test ./...")
		clean := stripTestAnsi(header)
		if !strings.Contains(clean, "go test ./...") {
			t.Fatalf("short param should be preserved, got: %q", clean)
		}
	})

	t.Run("CJK characters truncated correctly", func(t *testing.T) {
		cjk := strings.Repeat("你好世界", 20) // 80 CJK chars, each ~2 cells wide
		header := styles.ToolHeader(StatusSuccess, "Tool", 60, cjk)
		if w := lipgloss.Width(header); w > 60 {
			t.Fatalf("CJK header width %d exceeds 60", w)
		}
	})

	t.Run("narrow width still renders", func(t *testing.T) {
		header := styles.ToolHeader(StatusSuccess, "Bash", 30, "go build ./...")
		if w := lipgloss.Width(header); w > 30 {
			t.Fatalf("narrow header width %d exceeds 30", w)
		}
		clean := stripTestAnsi(header)
		if !strings.Contains(clean, "Bash") {
			t.Fatalf("should contain tool name, got: %q", clean)
		}
	})
}

func TestBashToolItem(t *testing.T) {
	styles := DefaultStyles()
	item := NewBashToolItem("t1", "Bash", "go test ./...", StatusRunning, styles)
	rendered := item.Render(80)
	if !strings.Contains(rendered, "Bash") {
		t.Fatalf("expected Bash in render: %s", rendered)
	}
	if !strings.Contains(rendered, "go test") {
		t.Fatalf("expected command in render: %s", rendered)
	}

	item.SetResult("ok  github.com/example  1.234s", false)
	rendered = item.Render(80)
	if !strings.Contains(rendered, "1.234s") {
		t.Fatalf("expected result in render: %s", rendered)
	}
}

func TestFileToolItem(t *testing.T) {
	styles := DefaultStyles()
	item := NewFileToolItem("t2", "Edit", "internal/tui/model.go", StatusSuccess, styles)
	rendered := item.Render(80)
	if !strings.Contains(rendered, "Edit") {
		t.Fatalf("expected Edit in render: %s", rendered)
	}
	if !strings.Contains(rendered, "model.go") {
		t.Fatalf("expected file path in render: %s", rendered)
	}
}

func TestTodoToolItem(t *testing.T) {
	styles := DefaultStyles()
	item := NewTodoToolItem("t3", []TodoTask{
		{ID: "1", Content: "design", Status: "done"},
		{ID: "2", Content: "implement", Status: "in_progress"},
		{ID: "3", Content: "test", Status: "pending"},
	}, styles)
	rendered := item.Render(80)
	if !strings.Contains(rendered, "To-Do") {
		t.Fatalf("expected To-Do in render: %s", rendered)
	}
	if !strings.Contains(rendered, "1/3") {
		t.Fatalf("expected ratio in render: %s", rendered)
	}
	if !strings.Contains(rendered, "✓") || !strings.Contains(rendered, "→") || !strings.Contains(rendered, "○") {
		t.Fatalf("expected task icons in render: %s", rendered)
	}
}

func TestAgentToolItem(t *testing.T) {
	styles := DefaultStyles()
	agent := NewAgentToolItem("a1", "implement auth", StatusRunning, styles)
	bash := NewBashToolItem("a1-b1", "Bash", "go test ./auth", StatusSuccess, styles)
	bash.SetResult("PASS", false)
	agent.AppendNested(bash)

	rendered := agent.Render(80)
	if !strings.Contains(rendered, "Agent") {
		t.Fatalf("expected Agent in render: %s", rendered)
	}
	if !strings.Contains(rendered, "auth") {
		t.Fatalf("expected task in render: %s", rendered)
	}
	if !strings.Contains(rendered, "└") || !strings.Contains(rendered, "Bash") {
		t.Fatalf("expected nested tool with tree line: %s", rendered)
	}
}

func TestFormatBody(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	content := strings.Join(lines, "\n")

	body, truncated := FormatBody(content, 80, 10)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(body, "10 more lines") {
		t.Fatalf("expected truncation message: %s", body)
	}

	singleBody, singleTrunc := FormatBody("short", 80, 10)
	if singleTrunc {
		t.Fatal("expected no truncation for short content")
	}
	if singleBody != "short" {
		t.Fatalf("expected 'short', got %q", singleBody)
	}
}

func TestNewToolItem(t *testing.T) {
	styles := DefaultStyles()
	tests := []struct {
		toolName string
		wantType string
	}{
		{"bash", "*chat.BashToolItem"},
		{"run_command", "*chat.BashToolItem"},
		{"read_file", "*chat.FileToolItem"},
		{"write_file", "*chat.FileToolItem"},
		{"edit_file", "*chat.FileToolItem"},
		{"search_files", "*chat.SearchToolItem"},
		{"glob", "*chat.SearchToolItem"},
		{"list_directory", "*chat.ListToolItem"},
		{"web_fetch", "*chat.WebToolItem"},
		{"web_search", "*chat.WebToolItem"},
		{"git_status", "*chat.GitToolItem"},
		{"git_diff", "*chat.GitToolItem"},
		{"start_command", "*chat.CmdToolItem"},
		{"read_command_output", "*chat.CmdToolItem"},
		{"unknown", "*chat.GenericToolItem"},
	}
	for _, tt := range tests {
		ctx := ToolContext{ToolName: tt.toolName, RawArgs: "{}"}
		item := NewToolItem("id1", ctx, StatusPending, styles)
		typeName := fmtTypeName(item)
		if typeName != tt.wantType {
			t.Errorf("NewToolItem(%q) = %s, want %s", tt.toolName, typeName, tt.wantType)
		}
	}
}

func fmtTypeName(v interface{}) string {
	s := fmt.Sprintf("%T", v)
	// Remove "chat." prefix for comparison
	return s
}

func TestNewToolItemDetailFlowsToRender(t *testing.T) {
	styles := DefaultStyles()

	tests := []struct {
		name      string
		toolName  string
		detail    string
		wantType  string
		wantParam string
	}{
		{"bash command", "run_command", "ls -la", "BashToolItem", "ls -la"},
		{"read file", "read_file", "/tmp/test.go", "FileToolItem", "/tmp/test.go"},
		{"edit file", "edit_file", "/tmp/test.go", "FileToolItem", "/tmp/test.go"},
		{"write file", "write_file", "/tmp/out.go", "FileToolItem", "/tmp/out.go"},
		{"search pattern", "search_files", "TODO", "SearchToolItem", "TODO"},
		{"glob pattern", "glob", "**/*.go", "SearchToolItem", "**/*.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := ToolContext{
				ToolName:    tt.toolName,
				DisplayName: PrettifyToolName(tt.toolName),
				Detail:      tt.detail,
				RawArgs:     "{}",
			}
			item := NewToolItem("test-id", ctx, StatusRunning, styles)
			if item == nil {
				t.Fatal("expected non-nil item")
			}

			typeName := fmt.Sprintf("%T", item)
			if !strings.Contains(typeName, tt.wantType) {
				t.Errorf("expected type to contain %q, got %T", tt.wantType, item)
			}

			rendered := item.Render(80)
			// Strip ANSI for comparison
			clean := stripTestAnsi(rendered)
			if !strings.Contains(clean, tt.wantParam) {
				t.Errorf("expected render to contain %q, got:\n%s", tt.wantParam, clean)
			}
		})
	}
}

func TestAssistantItemPrefixOnSameLine(t *testing.T) {
	styles := DefaultStyles()
	item := NewAssistantItem("a1", styles)
	item.SetText("Hello, this is a test.")

	rendered := item.Render(80)

	// The prefix icon and the first line of text must be on the same line.
	// Before the fix, glamour added a leading newline, so the prefix
	// ended up alone on the first line with the text on the next line.
	firstLine := strings.SplitN(rendered, "\n", 2)[0]

	// First line must contain both the prefix and some text content
	prefix := styles.AssistantPrefix
	if !strings.Contains(firstLine, prefix) {
		t.Fatalf("first line should contain prefix %q, got: %q", prefix, firstLine)
	}
	// Strip ANSI to check for actual text content
	clean := stripTestAnsi(firstLine)
	if !strings.Contains(clean, "Hello") {
		t.Fatalf("first line should contain text content, got:\n%q\nfull render:\n%s", clean, rendered)
	}
}

func TestAssistantItemMarkdownPrefixAlignment(t *testing.T) {
	styles := DefaultStyles()
	item := NewAssistantItem("a2", styles)
	item.SetText("# Title\n\nParagraph text here.")

	rendered := item.Render(80)
	lines := strings.Split(rendered, "\n")

	prefix := styles.AssistantPrefix
	prefixWidth := lipgloss.Width(styles.AssistantStyle.Render(prefix))

	// Every continuation line (after the first) should be indented by prefixWidth
	for i, line := range lines {
		if i == 0 {
			continue // first line has the prefix icon
		}
		// Skip empty lines (they're fine)
		clean := stripTestAnsi(line)
		if clean == "" {
			continue
		}
		// Count leading spaces
		leading := countLeadingSpaces(line)
		if leading < prefixWidth {
			t.Errorf("line %d: expected at least %d leading spaces, got %d (line: %q)",
				i+1, prefixWidth, leading, clean)
		}
	}
}

func countLeadingSpaces(s string) int {
	// Skip ANSI escape sequences
	n := 0
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if c == 'm' {
				inEscape = false
			}
			continue
		}
		if c == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

func stripTestAnsi(s string) string {
	var result strings.Builder
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if c == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}

func TestToolHeaderShowsParams(t *testing.T) {
	styles := DefaultStyles()

	// BashToolItem — should show command in header
	bash := NewBashToolItem("t1", "Bash", "go build ./...", StatusSuccess, styles)
	bash.SetResult("ok", false)
	rendered := bash.Render(80)
	firstLine := strings.SplitN(rendered, "\n", 2)[0]
	clean := stripTestAnsi(firstLine)
	if !strings.Contains(clean, "go build") {
		t.Fatalf("BashToolItem first line should contain command, got: %q", clean)
	}
	if !strings.Contains(clean, "Bash") {
		t.Fatalf("BashToolItem first line should contain 'Bash', got: %q", clean)
	}

	// FileToolItem — should show path in header
	file := NewFileToolItem("t2", "Read", "internal/config/config.go", StatusSuccess, styles)
	rendered2 := file.Render(80)
	firstLine2 := strings.SplitN(rendered2, "\n", 2)[0]
	clean2 := stripTestAnsi(firstLine2)
	if !strings.Contains(clean2, "config.go") {
		t.Fatalf("FileToolItem first line should contain file path, got: %q", clean2)
	}

	// Generic tool — should show query/pattern or fallback to truncated input
	generic := NewGenericToolItem("t3", "SomeTool", StatusRunning, `target: fix the bug, scope: full`, styles)
	rendered3 := generic.Render(80)
	firstLine3 := strings.SplitN(rendered3, "\n", 2)[0]
	clean3 := stripTestAnsi(firstLine3)
	if clean3 == "" {
		t.Fatal("generic tool should have non-empty first line")
	}
	// Should at least show the tool name and detail
	if !strings.Contains(clean3, "SomeTool") {
		t.Fatalf("generic tool first line should contain tool name, got: %q", clean3)
	}
	if !strings.Contains(clean3, "fix the bug") {
		t.Fatalf("generic tool first line should contain detail, got: %q", clean3)
	}
}

// TestVisualWidthDoesNotExceedViewport verifies that every rendered line
// (after adding prefix padding) does not exceed the available width.
// If a line's visual width exceeds the viewport width, the terminal will
// auto-wrap it, creating extra visual lines that measureHeight() doesn't
// count — causing scroll-position miscalculation and content overflow.
func TestVisualWidthDoesNotExceedViewport(t *testing.T) {
	styles := DefaultStyles()

	testTexts := []struct {
		label string
		text  string
	}{
		{"short ASCII", "hello world"},
		{"long ASCII", strings.Repeat("this is a test sentence that should wrap. ", 10)},
		{"short CJK", "你好世界"},
		{"long CJK", strings.Repeat("这是一段用于测试中文文本换行的文字内容。", 15)},
		{"mixed ASCII+CJK", "Hello 你好 World 世界 " + strings.Repeat("测试文本混合换行功能", 10)},
		{"multiline", "line1\nline2\nline3\nline4"},
		{"long multiline", strings.Repeat("这是一行很长的中文文本用于测试换行计算", 3) + "\n" + strings.Repeat("Another long English line for testing word wrapping behavior in terminal", 3)},
		{"markdown headings", "# Title\n## Subtitle\n### Sub-subtitle\nSome body text here"},
		{"markdown list", "- item one\n- item two\n- item three with a longer description that should wrap"},
		{"markdown code", "```go\nfmt.Println(\"hello\")\n```"},
	}

	widths := []int{20, 30, 40, 60, 76, 80}

	for _, tt := range testTexts {
		t.Run(tt.label, func(t *testing.T) {
			for _, w := range widths {
				t.Run(fmt.Sprintf("width_%d", w), func(t *testing.T) {
					testVisualWidthForItemType(t, styles, tt.text, w)
				})
			}
		})
	}
}

func testVisualWidthForItemType(t *testing.T, styles Styles, text string, width int) {
	t.Helper()

	itemTypes := []struct {
		name string
		item Item
	}{
		{"user", NewUserItem("u1", text, styles)},
		{"assistant", func() *AssistantItem {
			a := NewAssistantItem("a1", styles)
			a.SetText(text)
			return a
		}()},
		{"system", NewSystemItem("s1", text, styles)},
	}

	for _, it := range itemTypes {
		t.Run(it.name, func(t *testing.T) {
			rendered := it.item.Render(width)
			lines := strings.Split(rendered, "\n")
			for i, line := range lines {
				visualW := lipgloss.Width(line)
				if visualW > width {
					clean := stripTestAnsi(line)
					if len(clean) > 80 {
						clean = clean[:80] + "…"
					}
					t.Errorf("%s line %d/%d: visual width %d exceeds maxWidth %d\n  clean: %q",
						it.name, i+1, len(lines), visualW, width, clean)
				}
			}
		})
	}
}
