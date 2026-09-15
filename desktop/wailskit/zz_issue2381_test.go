package wailskit

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/security"
)

// #2381: the Markdown export must give user/assistant message bodies the
// SAME protection the JSON export and the MD tool branch already apply -
// RedactForDisplay + 2000-rune truncation. Before the fix, a pasted
// sk-ant-... key landed verbatim in shared .md files while the JSON
// export masked it.
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
	// Redaction must actually run (masked form present), not drop the line.
	if !strings.Contains(out, "sk-a") {
		t.Fatalf("expected masked key prefix in output, got:\n%s", out)
	}
	_ = security.RedactForDisplay // keep import honest if assertions change
}

// Truncation parity: bodies longer than 2000 runes are clamped with the
// same marker as the tool branch (rune-boundary safe, #301).
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
	// Rune-boundary sanity for multibyte content.
	cjk := strings.Repeat("密", 1500) // 4500 bytes, 1500 runes - under the rune budget but over 2000 bytes
	out2 := formatMessagesAsMarkdown([]SessionMessage{{Role: "assistant", Content: cjk}}, "t")
	if !strings.Contains(out2, "密") {
		t.Fatal("CJK content vanished from the export")
	}
}
