package wailskit

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/security"
)

func TestMarkdownExportRedactsUserAssistantBodies(t *testing.T) {
	msgs := []SessionMessage{
		{Role: "user", Content: "my key is sk-ant-abcdefghijklmnopqrstuvwxyz1234567890 please check"},
		{Role: "assistant", Content: "ok, noted API_KEY=abcdefghijklmnopqrstuvWXYZ12345"},
	}
	out := formatMessagesAsMarkdown(msgs, "t")
	if strings.Contains(out, "sk-ant-abcdefghijklmnopqrstuvwxyz1234567890") {
		t.Fatal("user body leaked an unredacted API key into the Markdown export")
	}
	if strings.Contains(out, "API_KEY=abcdefghijklmnopqrstuvWXYZ12345") {
		t.Fatal("assistant body leaked an unredacted api_key assignment into the Markdown export")
	}

	if !strings.Contains(out, "sk-a") {
		t.Fatalf("expected masked key prefix in output, got:\n%s", out)
	}
	_ = security.RedactForDisplay
}

func TestMarkdownExportTruncatesLongBodies(t *testing.T) {
	long := strings.Repeat("a", 5000)
	msgs := []SessionMessage{{Role: "user", Content: long}}
	out := formatMessagesAsMarkdown(msgs, "t")
	if strings.Contains(out, strings.Repeat("a", 2500)) {
		t.Fatal("user body beyond the 2000-rune clamp leaked into the export")
	}
	if !strings.Contains(out, "... (truncated)") {
		t.Fatalf("expected truncation marker, got tail:\n%s", out[len(out)-80:])
	}

	cjk := strings.Repeat("密", 1500)
	out2 := formatMessagesAsMarkdown([]SessionMessage{{Role: "assistant", Content: cjk}}, "t")
	if !strings.Contains(out2, "密") {
		t.Fatal("CJK content vanished from the export")
	}
}
