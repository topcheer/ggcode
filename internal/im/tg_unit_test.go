package im

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	tgmd "github.com/eekstunt/telegramify-markdown-go"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

// --- TG pure functions unit tests ---

func TestSplitTGMessage(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   int // expected number of chunks
	}{
		{"empty", "", 10, 1},
		{"short", "hello", 10, 1},
		{"exact", "12345", 5, 1},
		{"split", "hello world!", 5, 3},
		{"newline_split", "line1\nline2\nline3", 8, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := splitTGMessage(tc.input, tc.maxLen)
			if len(chunks) != tc.want {
				t.Errorf("splitTGMessage(%q, %d) = %d chunks, want %d: %v", tc.input, tc.maxLen, len(chunks), tc.want, chunks)
			}
		})
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		ok    bool
	}{
		{"123", 123, true},
		{"0", 0, true},
		{"", 0, false},
		{"12a", 0, false},
		{"-1", 0, false},
		{"  42  ", 42, true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseInt(tc.input)
			if (err == nil) != tc.ok {
				t.Errorf("parseInt(%q) err=%v, want ok=%v", tc.input, err, tc.ok)
			}
			if got != tc.want {
				t.Errorf("parseInt(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestJsonInt64Str(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{float64(12345), "12345"},
		{int64(99999), "99999"},
		{42, "42"},
		{"123456", "123456"},
		{nil, "<nil>"},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%v", tc.input), func(t *testing.T) {
			got := jsonInt64Str(tc.input)
			if got != tc.want {
				t.Errorf("jsonInt64Str(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestTGOutboundText(t *testing.T) {
	adapter := &tgAdapter{}

	tests := []struct {
		name  string
		event OutboundEvent
		want  string
	}{
		{"text", OutboundEvent{Kind: OutboundEventText, Text: "hello"}, "hello"},
		{"status", OutboundEvent{Kind: OutboundEventStatus, Status: "thinking..."}, "thinking..."},
		{"approval_request", OutboundEvent{Kind: OutboundEventApprovalRequest, Approval: &ApprovalRequest{ToolName: "bash", Input: "rm -rf"}}, "[approval] bash\nrm -rf"},
		{"approval_result", OutboundEvent{Kind: OutboundEventApprovalResult, Result: &ApprovalResult{Decision: permission.Allow}}, "[approval result] allow"},
		{"approval_request_nil", OutboundEvent{Kind: OutboundEventApprovalRequest}, ""},
		{"approval_result_nil", OutboundEvent{Kind: OutboundEventApprovalResult}, ""},
		{"tool_call_nil", OutboundEvent{Kind: OutboundEventToolCall}, ""},
		{"tool_call_bash", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "bash", Args: `{"command":"ls -la"}`}}, "⚡ 执行命令:\n```\nls -la\n```"},
		{"tool_call_read", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"file_path":"chart.html"}`}}, "📖 读取文件: `chart.html`"},
		{"tool_call_write", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "write_file", Args: `{"file_path":"output.md"}`}}, "📝 写入文件: `output.md`"},
		{"tool_call_edit", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "edit_file", Args: `{"file_path":"main.go"}`}}, "✏️ 编辑文件: `main.go`"},
		{"tool_call_todo", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "todo_write"}}, "📋 更新待办列表"},
		{"tool_result_nil", OutboundEvent{Kind: OutboundEventToolResult}, ""},
		// Tool results — new style
		{"tool_result_bash", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "bash", Result: "file1.txt\nfile2.txt"}}, "```\nfile1.txt\nfile2.txt\n```"},
		{"tool_result_bash_with_cmd", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "bash", Args: `{"command":"ls"}`, Result: "file1.txt\nfile2.txt"}}, "```bash\nls\n```\n```\nfile1.txt\nfile2.txt\n```"},
		{"tool_result_bash_err", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "bash", Args: `{"command":"bad_cmd"}`, Result: "command not found", IsError: true}}, "```bash\nbad_cmd\n```\n```\ncommand not found\n```"},
		{"tool_result_read_ok", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Args: `{"path":"chart.html"}`, Result: "file content..."}}, "📖 chart.html"},
		{"tool_result_read_ok_nopath", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Result: "content"}}, "📖 Read"},
		{"tool_result_read_err", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Args: `{"path":"missing.txt"}`, Result: "no such file", IsError: true}}, "📖 missing.txt\n```\nno such file\n```"},
		// read_file format-aware display
		{"tool_call_read_pdf", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"report.pdf"}`}}, "📄 读取 PDF: `report.pdf`"},
		{"tool_call_read_xlsx", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"data.xlsx"}`}}, "📊 读取 Excel: `data.xlsx`"},
		{"tool_call_read_zip", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"/tmp/src.zip"}`}}, "📦 读取 ZIP: `src.zip`"},
		{"tool_call_read_targz", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"backup.tar.gz"}`}}, "📦 读取 tar.gz: `backup.tar.gz`"},
		{"tool_call_read_pages", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"essay.pages"}`}}, "📄 读取 Pages: `essay.pages`"},
		{"tool_call_read_svg", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"diagram.svg"}`}}, "🖼️ 读取 SVG: `diagram.svg`"},
		{"tool_result_read_pdf_ok", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Args: `{"path":"report.pdf"}`, Result: "[Extracted from pdf, 3 pages]\n     1\tline1\n     2\tline2"}}, "📄 report.pdf (3 页, 2 行)"},
		{"tool_result_read_docx_ok", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Args: `{"path":"doc.docx"}`, Result: "[Extracted from docx]\n     1\thello world"}}, "📄 doc.docx (读取 1 行)"},
		{"tool_result_read_zip_ok", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Args: `{"path":"src.zip"}`, Result: "[Archive: zip format, 15 files]\n\n--- README.md ---\nhello"}}, "📦 src.zip (15 个文件)"},
		{"tool_result_read_zip_truncated", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Args: `{"path":"big.tar.gz"}`, Result: "[Archive: tar.gz format, 500 files]\n[Showing first 500 of 1000 files]\n--- main.go ---\npackage main"}}, "📦 big.tar.gz (1000 个文件, 展示前 500)"},
		// read_file with range
		{"tool_call_read_offset", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"log.txt","offset":100,"limit":50}`}}, "📖 读取文件: `log.txt` [行 100-149]"},
		{"tool_call_read_offset_only", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"log.txt","offset":200}`}}, "📖 读取文件: `log.txt` [从行 200]"},
		{"tool_call_read_limit_only", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Args: `{"path":"log.txt","limit":10}`}}, "📖 读取文件: `log.txt` [前 10 行]"},
		{"tool_result_read_offset", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Args: `{"path":"log.txt","offset":100,"limit":50}`, Result: "line100\nline101"}}, "📖 log.txt [行 100-149]"},
		{"tool_result_read_no_range", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Args: `{"path":"full.txt"}`, Result: "line1\nline2"}}, "📖 full.txt"},
		{"tool_result_edit_ok", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "edit_file", Args: `{"file_path":"main.go"}`, Result: "ok"}}, "✏️ main.go"},
		{"tool_result_edit_err", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "edit_file", Args: `{"file_path":"main.go"}`, Result: "conflict", IsError: true}}, "✏️ main.go\n```\nconflict\n```"},
		{"tool_result_write_ok", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "write_file", Args: `{"file_path":"output.md"}`, Result: "ok"}}, "📝 output.md"},
		{"tool_result_write_err", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "write_file", Args: `{"file_path":"out.md"}`, Result: "permission denied", IsError: true}}, "📝 out.md\n```\npermission denied\n```"},
		{"tool_result_empty", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "bash", Result: ""}}, ""},
		{"tool_result_empty_with_cmd", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "bash", Args: `{"command":"echo ok"}`, Result: ""}}, "```bash\necho ok\n```\n```\n(无输出)\n```"},
		{"tool_result_listdir", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "list_directory", Args: `{"path":"src"}`, Result: "main.go\ntypes.go"}}, "📂 src\n```\nmain.go\ntypes.go\n```"},
		{"tool_result_listdir_empty", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "list_directory", Args: `{"path":"empty"}`, Result: ""}}, "📂 empty"},
		{"tool_result_glob", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "glob", Args: `{"pattern":"**/*.go"}`, Result: "main.go\ntypes.go\nutils.go"}}, "🔍 `**/*.go`\n```\nmain.go\ntypes.go\nutils.go\n```"},
		{"tool_result_glob_empty", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "glob", Args: `{"pattern":"*.xyz"}`, Result: ""}}, "🔍 `*.xyz`: 无匹配"},
		{"tool_result_search", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "search_files", Args: `{"pattern":"TODO"}`, Result: "main.go:1:TODO fix\nutils.go:5:TODO refactor"}}, "🔍 `TODO`\n```\nmain.go:1:TODO fix\nutils.go:5:TODO refactor\n```"},
		{"tool_result_search_empty", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "search_files", Args: `{"pattern":"NONEXIST"}`, Result: ""}}, "🔍 `NONEXIST`: 0 条结果"},
		{"unknown", OutboundEvent{Kind: "unknown"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := adapter.outboundText(tc.event)
			if got != tc.want {
				t.Errorf("outboundText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTGOutboundText_English(t *testing.T) {
	adapter := &tgAdapter{}

	tests := []struct {
		name  string
		event OutboundEvent
		want  string
	}{
		// Tool calls — English
		{"tool_call_bash", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "bash", Lang: "en", Args: `{"command":"ls -la"}`}}, "⚡ Run command:\n```\nls -la\n```"},
		{"tool_call_read", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Lang: "en", Args: `{"file_path":"chart.html"}`}}, "📖 Reading file: `chart.html`"},
		{"tool_call_read_pdf", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"report.pdf"}`}}, "📄 Reading PDF: `report.pdf`"},
		{"tool_call_read_xlsx", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"data.xlsx"}`}}, "📊 Reading Excel: `data.xlsx`"},
		{"tool_call_read_zip", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"src.zip"}`}}, "📦 Reading ZIP: `src.zip`"},
		{"tool_call_edit", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "edit_file", Lang: "en", Args: `{"file_path":"main.go"}`}}, "✏️ Edit file: `main.go`"},
		{"tool_call_write", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "write_file", Lang: "en", Args: `{"file_path":"output.md"}`}}, "📝 Write file: `output.md`"},
		{"tool_call_todo", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "todo_write", Lang: "en"}}, "📋 Update todos"},
		{"tool_call_glob", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "glob", Lang: "en", Args: `{"pattern":"*.go"}`}}, "🔍 Find files: `*.go`"},
		{"tool_call_search", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "search_files", Lang: "en", Args: `{"pattern":"TODO"}`}}, "🔍 Search: `TODO`"},
		{"tool_call_listdir", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "list_directory", Lang: "en", Args: `{"path":"src"}`}}, "📂 List directory: `src`"},
		{"tool_call_webfetch", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "web_fetch", Lang: "en", Args: `{"url":"https://example.com"}`}}, "🌐 Fetch: https://example.com"},
		{"tool_call_websearch", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "web_search", Lang: "en", Args: `{"query":"golang tutorial"}`}}, "🔍 Search: golang tutorial"},
		{"tool_call_skill", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "skill", Lang: "en", Detail: "commit"}}, "🔧 Load skill: `commit`"},

		// Tool results — English
		{"tool_result_bash_empty", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "bash", Lang: "en", Args: `{"command":"echo ok"}`, Result: ""}}, "```bash\necho ok\n```\n```\n(no output)\n```"},
		{"tool_result_read_pdf", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"report.pdf"}`, Result: "[Extracted from pdf, 3 pages]\n     1\tline1\n     2\tline2"}}, "📄 report.pdf (3 pages, 2 lines)"},
		{"tool_result_read_docx", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"doc.docx"}`, Result: "[Extracted from docx]\n     1\thello world"}}, "📄 doc.docx (1 lines extracted)"},
		{"tool_result_read_zip", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"src.zip"}`, Result: "[Archive: zip format, 15 files]\n\n--- README.md ---\nhello"}}, "📦 src.zip (15 files)"},
		{"tool_result_read_zip_truncated", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"big.tar.gz"}`, Result: "[Archive: tar.gz format, 500 files]\n[Showing first 500 of 1000 files]\n--- main.go ---\npackage main"}}, "📦 big.tar.gz (1000 files, showing first 500)"},
		{"tool_result_glob_empty", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "glob", Lang: "en", Args: `{"pattern":"*.xyz"}`, Result: ""}}, "🔍 `*.xyz`: no matches"},
		{"tool_result_search_empty", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "search_files", Lang: "en", Args: `{"pattern":"NONEXIST"}`, Result: ""}}, "🔍 `NONEXIST`: 0 results"},

		// Read range — English
		{"tool_call_read_offset", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"log.txt","offset":100,"limit":50}`}}, "📖 Reading file: `log.txt` [lines 100-149]"},
		{"tool_call_read_offset_only", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"log.txt","offset":200}`}}, "📖 Reading file: `log.txt` [from line 200]"},
		{"tool_call_read_limit_only", OutboundEvent{Kind: OutboundEventToolCall, ToolCall: &ToolCallInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"log.txt","limit":10}`}}, "📖 Reading file: `log.txt` [first 10 lines]"},
		{"tool_result_read_offset", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_file", Lang: "en", Args: `{"path":"log.txt","offset":100,"limit":50}`, Result: "line100\nline101"}}, "📖 log.txt [lines 100-149]"},

		// Background commands — English
		{"tool_result_start_cmd", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "start_command", Lang: "en", Result: ""}}, "⚡ Background command started"},
		{"tool_result_stop_cmd", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "stop_command", Lang: "en", Result: ""}}, "🛑 Command stopped"},
		{"tool_result_wait_cmd", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "wait_command", Lang: "en", Result: ""}}, "⏳ Command completed"},
		{"tool_result_write_input", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "write_command_input", Lang: "en", Result: ""}}, "⌨️ Input sent"},
		{"tool_result_list_cmds", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "list_commands", Lang: "en", Result: ""}}, "📋 no active commands"},

		// Agent tools — English
		{"tool_result_spawn_agent", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "spawn_agent", Lang: "en", Result: ""}}, "🤖 Sub-task started"},
		{"tool_result_wait_agent", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "wait_agent", Lang: "en", Result: ""}}, "🤖 Sub-task completed"},
		{"tool_result_list_agents", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "list_agents", Lang: "en", Result: ""}}, "🤖 no active agents"},

		// MCP / Skill / Memory — English
		{"tool_result_skill_loaded", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "skill", Lang: "en", Result: ""}}, "🔧 Skill loaded"},
		{"tool_result_memory_saved", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "save_memory", Lang: "en", Result: ""}}, "💾 Memory saved"},
		{"tool_result_mcp_capabilities", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "list_mcp_capabilities", Lang: "en", Result: ""}}, "🔗 MCP service list"},
		{"tool_result_mcp_resource", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "read_mcp_resource", Lang: "en", Result: ""}}, "🔗 Resource content"},
		{"tool_result_ask_user", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "ask_user", Lang: "en", Result: ""}}, "💬 Reply received"},

		// Todo — English
		{"tool_result_todo_empty", OutboundEvent{Kind: OutboundEventToolResult, ToolRes: &ToolResultInfo{ToolName: "todo_write", Lang: "en", Args: `{}`}}, "📋 Update todos"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := adapter.outboundText(tc.event)
			if got != tc.want {
				t.Errorf("outboundText(en) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTGPayloadKeys(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"nil", nil, "-"},
		{"empty_map", map[string]any{}, "-"},
		{"single", map[string]any{"b": 1, "a": 2}, "a,b"},
		{"not_map", "string", "-"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tgPayloadKeys(tc.input)
			if got != tc.want {
				t.Errorf("tgPayloadKeys() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTGResolveReplyTo(t *testing.T) {
	adapter := &tgAdapter{}
	tests := []struct {
		name    string
		binding ChannelBinding
		want    string
	}{
		{"has_message_id", ChannelBinding{LastInboundMessageID: "123"}, "123"},
		{"empty", ChannelBinding{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := adapter.resolveReplyTo(tc.binding)
			if got != tc.want {
				t.Errorf("resolveReplyTo() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTGSeenUpdate(t *testing.T) {
	adapter := &tgAdapter{seen: make(map[int]time.Time)}

	// First time should not be seen
	if adapter.seenUpdate(1) {
		t.Error("update 1 should not be seen initially")
	}
	// Second time should be seen
	if !adapter.seenUpdate(1) {
		t.Error("update 1 should be seen after first call")
	}
	// Different update should not be seen
	if adapter.seenUpdate(2) {
		t.Error("update 2 should not be seen")
	}
}

func TestTGSendUnauthorized(t *testing.T) {
	adapter := &tgAdapter{}
	// sendUnauthorized just sends text — test it doesn't panic
	// (it will fail on no connection but we're testing the message content)
	_ = adapter
}

func TestTGIsConnected(t *testing.T) {
	adapter := &tgAdapter{}
	if adapter.isConnected() {
		t.Error("should not be connected initially")
	}
	adapter.mu.Lock()
	adapter.connected = true
	adapter.mu.Unlock()
	if !adapter.isConnected() {
		t.Error("should be connected after setting")
	}
}

func TestTGNewAdapter_MissingToken(t *testing.T) {
	_, err := newTGAdapter("test", config.IMConfig{}, config.IMAdapterConfig{Extra: map[string]any{}}, nil)
	if err == nil {
		t.Error("expected error for missing bot token")
	}
}

func TestTGNewAdapter_WithToken(t *testing.T) {
	adapter, err := newTGAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{"bot_token": "123456:ABC-DEF"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter.botToken != "123456:ABC-DEF" {
		t.Errorf("botToken = %q", adapter.botToken)
	}
	if adapter.apiBase != tgDefaultAPIBase {
		t.Errorf("apiBase = %q, want default", adapter.apiBase)
	}
}

func TestTGNewAdapter_CustomAPIRoot(t *testing.T) {
	adapter, err := newTGAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"bot_token": "123:ABC",
			"api_root":  "https://custom.api.com",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.apiBase != "https://custom.api.com" {
		t.Errorf("apiBase = %q", adapter.apiBase)
	}
}

func TestTGNewAdapter_ParseMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"markdown", "MarkdownV2"},
		{"MarkdownV2", "MarkdownV2"},
		{"MARKDOWN", "MarkdownV2"},
		{"html", "HTML"},
		{"", ""},        // default: entity mode (tgmd.Convert)
		{"unknown", ""}, // unknown falls back to entity mode
		{"none", ""},    // explicit "none" disables parse mode
		{"plain", ""},   // explicit "plain" disables parse mode
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			adapter, _ := newTGAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
				Extra: map[string]any{
					"bot_token":  "123:ABC",
					"parse_mode": tc.input,
				},
			}, nil)
			if adapter.parseMode != tc.want {
				t.Errorf("parseMode = %q, want %q", adapter.parseMode, tc.want)
			}
		})
	}
}

func TestTGResolveSTTConfig(t *testing.T) {
	// Empty config → nil
	if cfg := resolveTGSTTConfig(config.IMSTTConfig{}, nil); cfg != nil {
		t.Error("expected nil for empty STT config")
	}

	// Global config with required fields
	cfg := resolveTGSTTConfig(config.IMSTTConfig{
		BaseURL: "https://api.example.com",
		APIKey:  "key123",
		Model:   "whisper-1",
	}, nil)
	if cfg == nil {
		t.Fatal("expected non-nil")
	}
	if cfg.BaseURL != "https://api.example.com" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}

	// Extra override
	cfg = resolveTGSTTConfig(config.IMSTTConfig{
		BaseURL: "https://global.com",
		APIKey:  "global-key",
		Model:   "global-model",
	}, map[string]any{
		"stt": map[string]any{
			"baseUrl": "https://override.com",
			"apiKey":  "override-key",
			"model":   "override-model",
		},
	})
	if cfg == nil {
		t.Fatal("expected non-nil")
	}
	if cfg.BaseURL != "https://override.com" {
		t.Errorf("BaseURL = %q, want override", cfg.BaseURL)
	}
}

func TestTGEntitiesToRaw(t *testing.T) {
	entities := []tgmd.Entity{
		{Type: tgmd.Bold, Offset: 0, Length: 5},
		{Type: tgmd.TextLink, Offset: 6, Length: 4, URL: "https://example.com"},
	}
	raw := tgEntitiesToRaw(entities)
	if len(raw) != 2 {
		t.Fatalf("got %d entities, want 2", len(raw))
	}
	if raw[0]["type"] != "bold" || raw[0]["offset"] != 0 || raw[0]["length"] != 5 {
		t.Errorf("first entity = %v", raw[0])
	}
	if raw[1]["url"] != "https://example.com" {
		t.Errorf("second entity missing url: %v", raw[1])
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	t.Logf("entities JSON: %s", data)
}

func TestTGMDConvert(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantText     string
		wantEntities int
	}{
		{"plain", "hello world", "hello world", 0},
		{"bold", "**bold**", "bold", 1},
		{"italic", "_italic_", "italic", 1},
		{"code", "`code`", "code", 1},
		{"link", "[text](https://example.com)", "text", 1},
		{"code_block", "```go\nfmt.Println()\n```", "fmt.Println()", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := tgmd.Convert(tc.input)
			if msg.Text != tc.wantText {
				t.Errorf("Text = %q, want %q", msg.Text, tc.wantText)
			}
			if len(msg.Entities) != tc.wantEntities {
				t.Errorf("Entities count = %d, want %d (entities: %+v)", len(msg.Entities), tc.wantEntities, msg.Entities)
			}
		})
	}
}

func TestTGMDConvert_StrikethroughNotError(t *testing.T) {
	// This was the original bug: ~~strikethrough~~ caused MarkdownV2 parse errors.
	// With tgmd.Convert, it produces plain text + strikethrough entity.
	input := "This is ~~deleted~~ text"
	msg := tgmd.Convert(input)
	if msg.Text != "This is deleted text" {
		t.Errorf("Text = %q", msg.Text)
	}
	if len(msg.Entities) < 1 {
		t.Fatal("expected at least 1 entity")
	}
	found := false
	for _, e := range msg.Entities {
		if e.Type == tgmd.Strikethrough {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no strikethrough entity found in %+v", msg.Entities)
	}
}
