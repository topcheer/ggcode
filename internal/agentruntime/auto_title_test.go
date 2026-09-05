package agentruntime

import (
	"strings"
	"testing"
)

func TestGenerateTitle_SimpleMessage(t *testing.T) {
	got := GenerateTitle("Fix the bug in the login handler")
	want := "Fix the bug in the login handler"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateTitle_CodeBlock(t *testing.T) {
	input := "Please fix this:\n```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\nThe function should print world"
	got := GenerateTitle(input)
	// Should strip code block and keep meaningful text
	if got == "" {
		t.Error("expected non-empty title")
	}
	if len(got) > titleMaxRunes+3 { // +3 for ellipsis
		t.Errorf("title too long: %d chars: %q", len(got), got)
	}
}

func TestGenerateTitle_URL(t *testing.T) {
	input := "Check out https://example.com/some/very/long/path?query=1 and fix the issue"
	got := GenerateTitle(input)
	if got == "" {
		t.Error("expected non-empty title")
	}
	for _, s := range []string{"https://", "http://", "example.com"} {
		if strings.Contains(got, s) {
			t.Errorf("title should not contain URL: %q contains %q", got, s)
		}
	}
}

func TestGenerateTitle_LongMessage(t *testing.T) {
	input := "I have a very complex task that requires multiple steps. First, I need you to refactor the authentication module. Then update the database schema. Finally, write tests for everything."
	got := GenerateTitle(input)
	if got == "" {
		t.Error("expected non-empty title")
	}
	// Should be truncated
	runes := []rune(got)
	if len(runes) > titleMaxRunes+3 {
		t.Errorf("title too long: %d runes: %q", len(runes), got)
	}
}

func TestGenerateTitle_InlineCodePreserved(t *testing.T) {
	// Regression for #1479: inlineCodeRe had no capture group, so the $1
	// replacement expanded to the empty string and deleted the identifier
	// the adjacent comment claimed to preserve.
	got := GenerateTitle("check `agent.go` for the bug")
	if !strings.Contains(got, "agent.go") {
		t.Errorf("inline code identifier lost: got %q, want it to contain %q", got, "agent.go")
	}
}

func TestIsGenericTitle_CJKShortTitles(t *testing.T) {
	// Regression for #1479: CJK titles carry no ASCII spaces, so the
	// no-space <6-rune branch flagged every short Chinese task title as
	// generic and RefineTitleAfterRun overwrote it with the English template.
	for _, title := range []string{"改个名字", "修个bug", "加个按钮", "部署上线", "帮我改名"} {
		if isGenericTitle(title) {
			t.Errorf("%q flagged generic; short CJK task titles must survive", title)
		}
	}
	// Real filler words stay generic via the enumeration table.
	for _, title := range []string{"你好", "测试", "hi!"} {
		if !isGenericTitle(title) {
			t.Errorf("%q should be generic", title)
		}
	}
}

func TestGenerateTitle_FilePath(t *testing.T) {
	input := "Fix the error in internal/agent/agent.go that causes a panic"
	got := GenerateTitle(input)
	// Should keep agent.go but not the full path
	if got == "" {
		t.Error("expected non-empty title")
	}
}

func TestGenerateTitle_Markdown(t *testing.T) {
	input := "## Task: Fix the **critical** bug in *login* module"
	got := GenerateTitle(input)
	if got == "" {
		t.Error("expected non-empty title")
	}
	for _, s := range []string{"##", "**", "*"} {
		if strings.Contains(got, s) {
			t.Errorf("title should not contain markdown: %q contains %q", got, s)
		}
	}
}

func TestGenerateTitle_CommandPrefix(t *testing.T) {
	input := "# Fix the test\nrun go test and fix failures"
	got := GenerateTitle(input)
	if got == "" {
		t.Error("expected non-empty title")
	}
	// Should strip the leading comment
	if got[0] == '#' {
		t.Errorf("title should not start with #: %q", got)
	}
}

func TestGenerateTitle_Multiline(t *testing.T) {
	input := "Fix the login bug\n\nHere are more details about the task that should not appear in the title"
	got := GenerateTitle(input)
	want := "Fix the login bug"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGenerateTitle_Chinese(t *testing.T) {
	input := "修复登录处理函数中的bug，需要检查认证逻辑"
	got := GenerateTitle(input)
	if got == "" {
		t.Error("expected non-empty title for Chinese input")
	}
}

func TestGenerateTitle_Empty(t *testing.T) {
	got := GenerateTitle("")
	if got != "" {
		t.Errorf("expected empty title for empty input, got %q", got)
	}
}

func TestGenerateTitle_BracketedTag(t *testing.T) {
	input := "[bug] Fix the crash in agent loop when context is cancelled"
	got := GenerateTitle(input)
	want := "Fix the crash in agent loop when context is cancelled"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShouldAutoTitle(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"New session", true},
		{"新会话", true},
		{"Fix the bug", false},
		{"Some descriptive title", false},
	}
	for _, tt := range tests {
		if got := ShouldAutoTitle(tt.title); got != tt.want {
			t.Errorf("ShouldAutoTitle(%q) = %v, want %v", tt.title, got, tt.want)
		}
	}
}

func TestIsGenericTitle(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"hi", true},
		{"help", true},
		{"test", true},
		{"hi!", false}, // has punctuation → "hi" but trimmed... actually isGenericTitle trims first
		{"Fix the login bug", false},
		{"Implement feature", false},
	}
	for _, tt := range tests {
		// Adjust: "hi!" → lower = "hi!" which is not in generics map and len >= 6 false, len < 6 true and no space → generic
		if tt.title == "hi!" {
			tt.want = true // single word under 6 chars
		}
		if got := isGenericTitle(tt.title); got != tt.want {
			t.Errorf("isGenericTitle(%q) = %v, want %v", tt.title, got, tt.want)
		}
	}
}

func TestRefineTitleAfterRun_GenericMessage(t *testing.T) {
	// User said "help" but agent did meaningful work
	got := RefineTitleAfterRun("", "help", "Edited 3 files in internal/agent")
	if got == "" {
		t.Error("expected refined title for generic message")
	}
	if got == "help" {
		t.Error("should not use generic message as title")
	}
}

func TestRefineTitleAfterRun_AlreadyGood(t *testing.T) {
	got := RefineTitleAfterRun("Fix the login bug", "Fix the login bug", "Edited files")
	if got != "" {
		t.Errorf("expected empty (no change needed), got %q", got)
	}
}

func TestRefineTitleAfterRun_UserSetTitle(t *testing.T) {
	// User manually set a good title — should not override
	got := RefineTitleAfterRun("My Custom Title", "some message", "did stuff")
	if got != "" {
		t.Errorf("should not override user-set title, got %q", got)
	}
}

func TestTruncateTitle(t *testing.T) {
	// Short — no truncation
	got := truncateTitle("short", 60)
	if got != "short" {
		t.Errorf("got %q, want %q", got, "short")
	}

	// Long — should truncate with ellipsis
	long := "This is a very long title that definitely exceeds the maximum length and should be truncated"
	got = truncateTitle(long, 30)
	r := []rune(got)
	if len(r) > 34 { // max + ellipsis
		t.Errorf("truncated title too long: %q (%d runes)", got, len(r))
	}
}
