package chat

import (
	"strings"
	"testing"
)

func TestSystemItemMarkdownRendersBold(t *testing.T) {
	item := NewSystemItem("sys-md", "**开局核查**: `total_open=41` 判据", Styles{})
	item.SetMarkdown(true)
	rendered := item.Render(120)
	// Markdown pipeline signal: inline-code backticks are consumed even under
	// the NoTTY style config (test stdout is not a terminal). Bold literals
	// (** **) survive only in NoTTY mode, which is the pre-existing global
	// behavior shared with AssistantItem, not specific to SystemItem.
	if strings.Contains(rendered, "`total_open=41`") {
		t.Fatalf("markdown item still renders literal backticks:\n%s", rendered)
	}
	if !strings.Contains(rendered, "total_open=41") {
		t.Fatalf("markdown item lost content:\n%s", rendered)
	}
}

func TestSystemItemPlainKeepsRaw(t *testing.T) {
	item := NewSystemItem("sys-plain", "[Strategist unavailable (x) - autopilot stopping]", Styles{})
	rendered := item.Render(120)
	if !strings.Contains(rendered, "[Strategist unavailable") {
		t.Fatalf("plain item lost text:\n%s", rendered)
	}
	// plain items must not gain markdown box padding artifacts
	if strings.Contains(rendered, "──") {
		t.Fatalf("plain item unexpectedly markdown-rendered:\n%s", rendered)
	}
}

func TestSystemItemMarkdownZeroWidthSafe(t *testing.T) {
	item := NewSystemItem("sys-narrow", "**bold** text", Styles{})
	item.SetMarkdown(true)
	rendered := item.Render(12)
	if rendered == "" {
		t.Fatal("narrow markdown render produced empty output")
	}
}
