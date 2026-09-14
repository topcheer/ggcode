package im

import "testing"

func postLine(text string) []any {
	return []any{map[string]any{"text": text}}
}

// #2309: multi-language post must not lose text when the first-iterated
// language block is empty - map order is random, so the old first-block
// return dropped ~50% of translated posts.
func TestIssue2309MultiLangPrefersNonEmpty(t *testing.T) {
	a := &feishuAdapter{}
	content := `{"post":{"en_us":{"content":[]},"zh_cn":{"content":[` +
		`[{"text":"你好"},{"text":"世界"}]]}}}`
	got := a.parseMessageContent(content)
	if got != "你好世界" {
		t.Fatalf("zh_cn text must be extracted regardless of map order, got %q", got)
	}
}

// #2309: zh_cn preferred over en_us when both non-empty (one message,
// one language - never join duplicates).
func TestIssue2309ZhPreferredOverEn(t *testing.T) {
	a := &feishuAdapter{}
	content := `{"post":{"en_us":{"content":[[{"text":"hello"}]]},` +
		`"zh_cn":{"content":[[{"text":"你好"}]]}}}`
	got := a.parseMessageContent(content)
	if got != "你好" {
		t.Fatalf("zh_cn must win when both present, got %q", got)
	}
}

// #2309: bare (unwrapped) single-language form stays supported - the
// existing fixtures use this shape.
func TestIssue2309BareSingleLangStillWorks(t *testing.T) {
	a := &feishuAdapter{}
	content := `{"zh_cn":{"content":[[{"text":"纯文本"}]]}}`
	if got := a.parseMessageContent(content); got != "纯文本" {
		t.Fatalf("bare single-language post must still extract, got %q", got)
	}
}

// #2309: all languages empty falls through to the structured-but-textless
// branch ("") - not the raw JSON.
func TestIssue2309AllEmptyFallsThrough(t *testing.T) {
	a := &feishuAdapter{}
	content := `{"post":{"en_us":{"content":[]},"zh_cn":{"content":[]}}}`
	if got := a.parseMessageContent(content); got != "" {
		t.Fatalf("all-empty post must return empty text, got %q", got)
	}
}

// plain text passthrough unchanged
func TestIssue2309PlainText(t *testing.T) {
	a := &feishuAdapter{}
	if got := a.parseMessageContent("hello world"); got != "hello world" {
		t.Fatalf("plain text must pass through, got %q", got)
	}
}
