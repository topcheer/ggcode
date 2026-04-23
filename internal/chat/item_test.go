package chat

import (
	"fmt"
	"strings"
	"testing"
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
	longParam := strings.Repeat("x", 200)
	header := styles.ToolHeader(StatusSuccess, "Bash", 40, longParam)
	// Allow ANSI escape codes overhead — just check the visible portion isn't too wide
	if strings.Contains(header, strings.Repeat("x", 100)) {
		t.Fatalf("header should be truncated: %s", header)
	}
}

func TestBashToolItem(t *testing.T) {
	styles := DefaultStyles()
	item := NewBashToolItem("t1", "go test ./...", StatusRunning, styles)
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
	bash := NewBashToolItem("a1-b1", "go test ./auth", StatusSuccess, styles)
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
		input    string
		wantType string
	}{
		{"bash", `{"command":"ls"}`, "*chat.BashToolItem"},
		{"read", `{"path":"main.go"}`, "*chat.FileToolItem"},
		{"write", `{"path":"out.go"}`, "*chat.FileToolItem"},
		{"edit", `{"path":"model.go"}`, "*chat.FileToolItem"},
		{"grep", `{"pattern":"TODO"}`, "*chat.SearchToolItem"},
		{"glob", `{"pattern":"*.go"}`, "*chat.SearchToolItem"},
		{"unknown", `{}`, "*chat.GenericToolItem"},
	}
	for _, tt := range tests {
		item := NewToolItem("id1", tt.toolName, StatusPending, tt.input, styles)
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
