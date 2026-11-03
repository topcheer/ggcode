package im

import "testing"

func postLine(text string) []any {
	return []any{map[string]any{"text": text}}
}

func TestIssue2309MultiLangPrefersNonEmpty(t *testing.T) {
	a := &feishuAdapter{}
	content := `{"post":{"en_us":{"content":[]},"zh_cn":{"content":[` +
		`[{"text":"你好"},{"text":"世界"}]]}}}`
	got := a.parseMessageContent(content)
	if got != "你好世界" {
		t.Fatalf("zh_cn text must be extracted regardless of map order, got %q", got)
	}
}

func TestIssue2309ZhPreferredOverEn(t *testing.T) {
	a := &feishuAdapter{}
	content := `{"post":{"en_us":{"content":[[{"text":"hello"}]]},` +
		`"zh_cn":{"content":[[{"text":"你好"}]]}}}`
	got := a.parseMessageContent(content)
	if got != "你好" {
		t.Fatalf("zh_cn must win when both present, got %q", got)
	}
}

func TestIssue2309BareSingleLangStillWorks(t *testing.T) {
	a := &feishuAdapter{}
	content := `{"zh_cn":{"content":[[{"text":"纯文本"}]]}}`
	if got := a.parseMessageContent(content); got != "纯文本" {
		t.Fatalf("bare single-language post must still extract, got %q", got)
	}
}

func TestIssue2309AllEmptyFallsThrough(t *testing.T) {
	a := &feishuAdapter{}
	content := `{"post":{"en_us":{"content":[]},"zh_cn":{"content":[]}}}`
	if got := a.parseMessageContent(content); got != "" {
		t.Fatalf("all-empty post must return empty text, got %q", got)
	}
}

func TestIssue2309PlainText(t *testing.T) {
	a := &feishuAdapter{}
	if got := a.parseMessageContent("hello world"); got != "hello world" {
		t.Fatalf("plain text must pass through, got %q", got)
	}
}
