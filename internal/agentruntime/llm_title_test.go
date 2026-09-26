package agentruntime

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTitleWantsLLMRefine(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"New session", true},
		{"新会话", true},
		{"hi", true},
		{"help", true},
		{"test", true},
		{"Fix the login timeout bug", false},
		{"修复登录超时问题", false},
		{"refactor session store locking", false},
	}
	for _, tc := range cases {
		if got := TitleWantsLLMRefine(tc.title); got != tc.want {
			t.Errorf("TitleWantsLLMRefine(%q) = %v, want %v", tc.title, got, tc.want)
		}
	}
}

func TestBuildLLMTitlePrompt(t *testing.T) {
	prompt, ok := BuildLLMTitlePrompt("帮我看看这个报错", "好的，这是 NullPointerException，出在 UserService.login")
	if !ok {
		t.Fatal("BuildLLMTitlePrompt returned ok=false for usable content")
	}
	for _, want := range []string{"User: 帮我看看这个报错", "Assistant: 好的", "Title:", "Same language"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nprompt:\n%s", want, prompt)
		}
	}

	// No assistant reply yet (first turn still running) must still work.
	if _, ok := BuildLLMTitlePrompt("explain the retry logic", ""); !ok {
		t.Error("empty assistant excerpt should be acceptable")
	}
	// Whitespace-only / tiny user content must skip the call.
	if p, ok := BuildLLMTitlePrompt("  \n\t ", "reply"); ok || p != "" {
		t.Error("whitespace-only user content must return ok=false")
	}
	if p, ok := BuildLLMTitlePrompt("hi", "hello"); ok {
		t.Errorf("too-short user content must return ok=false, got prompt %q", p)
	}
}

func TestBuildLLMTitlePromptExcerptCap(t *testing.T) {
	long := strings.Repeat("栈帧信息 ", 500) // 2500 runes
	prompt, ok := BuildLLMTitlePrompt(long, long)
	if !ok {
		t.Fatal("expected ok=true for long content")
	}
	if n := utf8.RuneCountInString(prompt); n > llmTitleUserExcerptRunes+llmTitleAssistantExcerptRunes+400 {
		t.Errorf("prompt not excerpt-capped: %d runes", n)
	}
}

func TestSanitizeLLMTitle(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "Fix login bug", "Fix login bug"},
		{"double quotes", `"Fix login bug"`, "Fix login bug"},
		{"single quotes", "'Fix login bug'", "Fix login bug"},
		{"backticks", "`Fix login bug`", "Fix login bug"},
		{"cjk corner brackets", "「修复登录问题」", "修复登录问题"},
		{"cjk curly quotes", "“修复登录问题”", "修复登录问题"},
		{"bold markers", "**Fix login bug**", "Fix login bug"},
		{"multiline takes first line", "Fix login bug\nhere is why\nmore", "Fix login bug"},
		{"label prefix", "Title: Fix login bug", "Title: Fix login bug"},
		{"trailing punctuation", "修复登录问题。", "修复登录问题"},
		{"trailing ascii punct", "Fix login bug...", "Fix login bug"},
		{"collapse whitespace", "Fix   login\tbug", "Fix login bug"},
		{"leading heading marks", "## Fix login bug", "Fix login bug"},
		{"empty", "   ", ""},
	}
	for _, tc := range cases {
		if got := SanitizeLLMTitle(tc.raw); got != tc.want {
			t.Errorf("%s: SanitizeLLMTitle(%q) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

func TestSanitizeLLMTitleCapsLength(t *testing.T) {
	raw := strings.Repeat("长标题字符", 40) // 200 runes
	got := SanitizeLLMTitle(raw)
	// truncateTitle's established semantics: content capped at
	// titleMaxRunes, then one ellipsis rune appended.
	if n := utf8.RuneCountInString(got); n > titleMaxRunes+1 {
		t.Errorf("sanitized title %d runes > cap %d(+1)", n, titleMaxRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated title should end with ellipsis, got %q", got)
	}
}

func TestLLMTitleAcceptable(t *testing.T) {
	if LLMTitleAcceptable("hi", "") {
		t.Error("empty candidate must be rejected")
	}
	if LLMTitleAcceptable("Fix login bug", "Fix login bug") {
		t.Error("identical candidate must be rejected")
	}
	if LLMTitleAcceptable("hi", "hello") {
		t.Error("generic candidate must be rejected")
	}
	if LLMTitleAcceptable("hi", "ok") {
		t.Error("too-short candidate must be rejected")
	}
	if !LLMTitleAcceptable("hi", "Fix login timeout bug") {
		t.Error("good candidate must be accepted")
	}
	if !LLMTitleAcceptable("", "修复登录超时问题") {
		t.Error("good CJK candidate over empty title must be accepted")
	}
}
