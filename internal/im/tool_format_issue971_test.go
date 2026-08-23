package im

import (
	"strings"
	"testing"
)

// --- Issue #971 Problem 1: lsp_* tool results must hit the hidden LSP branch
// (empty message), NOT the MCP formatting path, regardless of the "_" in the
// tool name.

func TestFormatSpecialIMToolResult_LSPToolsHidden(t *testing.T) {
	lspTools := []string{
		"lsp_definition", "lsp_references", "lsp_document_highlights",
		"lsp_hover", "lsp_symbols", "lsp_workspace_symbols",
		"lsp_diagnostics", "lsp_code_actions", "lsp_rename",
		"lsp_implementation", "lsp_incoming_calls", "lsp_outgoing_calls",
		"lsp_prepare_call_hierarchy",
	}
	for _, name := range lspTools {
		tr := &ToolResultInfo{
			ToolName: name,
			Result:   `{"line": 10, "character": 4}`,
			Lang:     "en",
		}
		handled, msg := formatSpecialIMToolResult(tr)
		if !handled {
			t.Fatalf("%s: expected handled=true", name)
		}
		if msg != "" {
			t.Fatalf("%s: expected empty hidden message, got %q", name, msg)
		}
		if strings.Contains(msg, "🔧") || strings.Contains(msg, "Lsp") {
			t.Fatalf("%s: result leaked MCP formatting: %q", name, msg)
		}
	}
}

// --- Issue #971 Problem 2: previously missing imLabel keys.

func TestIMLabel_Issue971MissingKeys(t *testing.T) {
	cases := []struct {
		key string
		en  string
		zh  string
	}{
		{"matches", "matches", "处匹配"},
		{"command_failed", "Command failed", "命令失败"},
		{"git_stash_list", "Git stash list", "储藏列表"},
	}
	for _, tc := range cases {
		if got := imLabel(ToolLangEn, tc.key); got != tc.en {
			t.Errorf("imLabel(en, %q) = %q, want %q", tc.key, got, tc.en)
		}
		if got := imLabel(ToolLangZhCN, tc.key); got != tc.zh {
			t.Errorf("imLabel(zh-CN, %q) = %q, want %q", tc.key, got, tc.zh)
		}
	}
}

// --- Issue #971 Problem 3: fence-safe code block wrapping.

func TestImFenceLen(t *testing.T) {
	cases := []struct {
		content string
		want    int
	}{
		{"plain text", 3},
		{"has `single` backtick", 3},
		{"has ``` triple fence", 4},
		{"has ```` quad run", 5},
		{"trailing ``", 3},
	}
	for _, tc := range cases {
		if got := imFenceLen(tc.content); got != tc.want {
			t.Errorf("imFenceLen(%q) = %d, want %d", tc.content, got, tc.want)
		}
	}
}

func TestImCodeBlock_FenceSafe(t *testing.T) {
	content := "func main() {\n\tfmt.Println(```go nested```)\n}"
	block := imCodeBlock(content)
	// Outer fence must be 4 backticks and must wrap the whole content.
	if !strings.HasPrefix(block, "````\n") || !strings.HasSuffix(block, "\n````") {
		t.Fatalf("imCodeBlock did not use extended fence: %q", block)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(block, "````\n"), "\n````")
	if inner != content {
		t.Fatalf("imCodeBlock altered content: %q", inner)
	}
	// A simple 3-fence content still uses the standard triple fence.
	simple := imCodeBlock("echo hello")
	if !strings.HasPrefix(simple, "```\n") || !strings.HasSuffix(simple, "\n```") {
		t.Fatalf("imCodeBlock should use 3-tick fence for plain content: %q", simple)
	}
}

// FormatSpecial grep success path must use the "matches" label, not the raw key.
func TestFormatSpecialIMToolResult_GrepMatchesLabel(t *testing.T) {
	tr := &ToolResultInfo{
		ToolName: "grep",
		Args:     `{"pattern":"foo","path":"/tmp"}`,
		Result:   "file1.go:1:foo",
		Lang:     "en",
	}
	msg := formatToolResultText(tr)
	if !strings.Contains(msg, "matches") {
		t.Fatalf("expected 'matches' label in grep result, got %q", msg)
	}
	if strings.Contains(msg, "\"matches\"") && strings.Contains(msg, "imLabel") {
		t.Fatalf("raw key leaked: %q", msg)
	}
}
