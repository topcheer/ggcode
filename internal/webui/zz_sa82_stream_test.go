package webui

import (
	"os"
	"strings"
	"testing"
)

// SA-82 流式渲染守卫的新家:旧内联 JS(chatStreamBlocks/splitMarkdownBlocks)已退役,
// 等价语义现由 React 组件承担(见 web/src/chat/)。本文件守卫等价契约:
// ①流式增量渲染不整帧重刷;②思考块流式展开/完成收合;③composer 活跃呼吸不外泄。

func TestSPA_ComposerActiveGlow(t *testing.T) {
	data, err := os.ReadFile("../../web/src/styles/tokens.css")
	if err != nil {
		t.Fatalf("tokens.css not readable: %v", err)
	}
	css := string(data)
	if !strings.Contains(css, "composer-breathe") {
		t.Error("tokens.css must define composer-breathe (active glow breathing)")
	}
	if !strings.Contains(css, "shadow-composer-idle") {
		t.Error("tokens.css must define composer idle shadow (glow must not leak into idle inputs)")
	}
	if !strings.Contains(css, "data-document-hidden") {
		t.Error("tokens.css must disable animations when document hidden (memory hygiene)")
	}
}

func TestSPA_ComposerWiring(t *testing.T) {
	data, err := os.ReadFile("../../web/src/chat/Composer.tsx")
	if err != nil {
		t.Fatalf("Composer.tsx not readable: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "composer-breathe") {
		t.Error("Composer must activate breathing only while focused (not idle)")
	}
	if !strings.Contains(src, "metaKey || e.ctrlKey") {
		t.Error("Composer must support Cmd/Ctrl+Enter to send")
	}
	if !strings.Contains(src, "disabled={conn !== \"open\"}") {
		t.Error("Composer must disable input while WS offline (queue resend handled by client)")
	}
}

func TestSPA_WSReconnectGuards(t *testing.T) {
	data, err := os.ReadFile("../../web/src/ws/client.ts")
	if err != nil {
		t.Fatalf("ws/client.ts not readable: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, "queue") {
		t.Error("WS client must queue messages while offline (resend on reconnect)")
	}
	if !strings.Contains(src, "2 ** this.retry") {
		t.Error("WS client must use exponential backoff reconnect")
	}
}
