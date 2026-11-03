package webui

import (
	"io/fs"
	"strings"
	"testing"
)

func TestSPA_StreamMarkdownIncrementalBlocks(t *testing.T) {
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if got := strings.Count(content, "function splitMarkdownBlocks("); got != 1 {
		t.Errorf("expected exactly 1 splitMarkdownBlocks definition, got %d", got)
	}
	if got := strings.Count(content, "function renderChatTextHTML("); got != 1 {
		t.Errorf("expected exactly 1 renderChatTextHTML definition, got %d", got)
	}

	if strings.Contains(content, "renderMarkdown(chatTextBuffer)") {
		t.Error("full-buffer renderMarkdown(chatTextBuffer) still present; streaming must use renderChatTextHTML")
	}

	if !strings.Contains(content, "chatStreamBlocks[reused].text === blocks[reused]") {
		t.Error("stable-block cache reuse must compare cached block text against current blocks")
	}

	if got := strings.Count(content, `<span class="chat-cursor"></span>`); got != 1 {
		t.Errorf("expected exactly 1 chat-cursor span (tail-only), got %d", got)
	}

	if !strings.Contains(content, "chatTextBuffer = '';\n      chatStreamBlocks = [];") {
		t.Error("user_ack must reset chatStreamBlocks alongside chatTextBuffer")
	}

	if !strings.Contains(content, "```|~~~") {
		t.Error("splitMarkdownBlocks must treat ``` and ~~~ fences as non-splittable regions")
	}
}
