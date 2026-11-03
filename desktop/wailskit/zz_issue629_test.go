package wailskit

import (
	"strings"
	"testing"
)

func TestIssue629_MarkdownExportRedactsToolContent(t *testing.T) {
	msgs := []SessionMessage{
		{
			Role:     "tool",
			ToolName: "read_file",
			Content:  "API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz123456\npassword: hunter2hunter2hunter2secret99",
		},
		{
			Role:       "tool",
			ToolName:   "config",
			ToolDetail: "set api_key sk-ant-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ01234567890123456789012345678901234567890",
		},
	}

	md := formatMessagesAsMarkdown(msgs, "test")

	if strings.Contains(md, "sk-proj-abcdefghijklmnopqrstuvwxyz123456") {
		t.Error("markdown export leaked unredacted secret in tool Content")
	}
	if strings.Contains(md, "hunter2hunter2hunter2secret99") {
		t.Error("markdown export leaked assignment-style secret in tool Content")
	}
	if !strings.Contains(md, "sk-pr"+"oj****") && !strings.Contains(md, "*") {
		t.Error("expected masked secret in tool Content output")
	}
	if !strings.Contains(md, "****") {
		t.Error("expected masked tool Content output")
	}

	if strings.Contains(md, "sk-ant-abcdefghijklmnopqrstuvwxyz") {
		t.Error("ToolDetail redaction regressed (#583)")
	}
}

func TestIssue629_MarkdownExportPlainContentUntouched(t *testing.T) {
	msgs := []SessionMessage{
		{Role: "user", Content: "What is 2+2?"},
		{Role: "assistant", Content: "It is 4."},
	}
	md := formatMessagesAsMarkdown(msgs, "plain")
	if !strings.Contains(md, "What is 2+2?") || !strings.Contains(md, "It is 4.") {
		t.Error("plain content should be exported unchanged")
	}
}
