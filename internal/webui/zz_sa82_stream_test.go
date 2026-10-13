package webui

import (
	"io/fs"
	"strings"
	"testing"
)

// #sa-82: webui streaming markdown must render incrementally (stable-block
// prefix cache + tail-only re-render) instead of re-parsing the full
// accumulated buffer on every text_delta (O(n^2)). Ports the TUI
// streamRenderCache semantics from internal/chat/stream_markdown.go.
func TestSPA_StreamMarkdownIncrementalBlocks(t *testing.T) {
	data, err := fs.ReadFile(spafs, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Exactly one definition of each streaming helper.
	if got := strings.Count(content, "function splitMarkdownBlocks("); got != 1 {
		t.Errorf("expected exactly 1 splitMarkdownBlocks definition, got %d", got)
	}
	if got := strings.Count(content, "function renderChatTextHTML("); got != 1 {
		t.Errorf("expected exactly 1 renderChatTextHTML definition, got %d", got)
	}

	// The old per-delta full-buffer re-render path must be gone.
	if strings.Contains(content, "renderMarkdown(chatTextBuffer)") {
		t.Error("full-buffer renderMarkdown(chatTextBuffer) still present; streaming must use renderChatTextHTML")
	}

	// Prefix cache must be validated by block-text identity before reuse -
	// a mismatched prefix forces re-render instead of serving stale HTML.
	if !strings.Contains(content, "chatStreamBlocks[reused].text === blocks[reused]") {
		t.Error("stable-block cache reuse must compare cached block text against current blocks")
	}

	// While streaming, the cursor must live on the tail render only.
	if got := strings.Count(content, `<span class="chat-cursor"></span>`); got != 1 {
		t.Errorf("expected exactly 1 chat-cursor span (tail-only), got %d", got)
	}

	// Cache must be reset per assistant turn: the user_ack handler resets
	// the block cache together with chatTextBuffer, before the next bubble.
	if !strings.Contains(content, "chatTextBuffer = '';\n      chatStreamBlocks = [];") {
		t.Error("user_ack must reset chatStreamBlocks alongside chatTextBuffer")
	}

	// Block splitting must not break inside fenced code regions.
	if !strings.Contains(content, "```|~~~") {
		t.Error("splitMarkdownBlocks must treat ``` and ~~~ fences as non-splittable regions")
	}
}
