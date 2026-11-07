package webui

import (
	"os"
	"strings"
	"testing"
)

// 旧单文件内联 JS 已由 Vite+React SPA 替代(web/ → internal/webui/dist,go:embed)。
// 本文件守卫新产物契约:SPA 挂载点/资源引用/主题先行/旧实现确已退役。

func spaDistIndex(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath_dist_index)
	if err != nil {
		t.Fatalf("dist/index.html not readable (run `make webui-build`): %v", err)
	}
	return string(data)
}

const filepath_dist_index = "dist/index.html"

func TestSPA_IndexHTMLExists(t *testing.T) {
	f, err := spafs.Open("index.html")
	if err != nil {
		t.Fatalf("index.html not found in embedded FS: %v", err)
	}
	defer f.Close()
}

func TestSPA_ViteContract(t *testing.T) {
	content := spaDistIndex(t)
	if !strings.Contains(content, `id="root"`) {
		t.Error("dist/index.html must contain React mount point #root")
	}
	if !strings.Contains(content, "/assets/") || !strings.Contains(content, `type="module"`) {
		t.Error("dist/index.html must reference Vite /assets/ module bundle")
	}
	if !strings.Contains(content, "ggcode-theme") {
		t.Error("dist/index.html must contain theme bootstrap (pre-paint, avoids flash)")
	}
}

func TestSPA_LegacyMonolithRetired(t *testing.T) {
	content := spaDistIndex(t)
	if strings.Contains(content, "function renderMarkdown(") {
		t.Error("legacy inline renderMarkdown still present in dist; monolith must stay retired")
	}
	if strings.Contains(content, "function splitMarkdownBlocks(") {
		t.Error("legacy inline splitMarkdownBlocks still present in dist; monolith must stay retired")
	}
}

// 源码守卫:流式渲染语义现居 web/src/chat(MessageBubble/ReasoningBlock)。
// repo-root 相对路径从包目录(internal/webui)上跳两级。
const webSrcChatDir = "../../web/src/chat"

func webSrcFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(webSrcChatDir + "/" + name)
	if err != nil {
		t.Fatalf("web source %s not readable: %v", name, err)
	}
	return string(data)
}

func TestSPA_StreamRenderGuards(t *testing.T) {
	bubble := webSrcFile(t, "MessageBubble.tsx")

	if !strings.Contains(bubble, "```") {
		t.Error("MessageBubble markdown must handle fenced code blocks")
	}
	if !strings.Contains(bubble, "StreamCursor") {
		t.Error("MessageBubble must render a streaming tail cursor")
	}
	if !strings.Contains(bubble, "message-enter") {
		t.Error("MessageBubble must apply message-enter animation (120ms fade+slide)")
	}

	reasoning := webSrcFile(t, "ReasoningBlock.tsx")
	if !strings.Contains(reasoning, "setOpen(false)") {
		t.Error("ReasoningBlock must collapse after streaming completes")
	}
}
