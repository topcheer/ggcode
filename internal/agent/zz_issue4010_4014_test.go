package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue4014PreviewIsRuneSafe(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)
	anchor := "preview := textBuf"
	idx := strings.Index(text, anchor)
	if idx < 0 {
		t.Fatal("autopilot deadlock preview site not found")
	}
	window := text[idx : idx+700]
	if !strings.Contains(window, "ToValidUTF8") {
		t.Fatal("preview truncation must sanitize with strings.ToValidUTF8 (#4014)")
	}

	cjk := strings.Repeat("死锁", 101)
	cut := cjk
	if len(cut) > 200 {
		cut = strings.ToValidUTF8(cut[:200], "")
	}
	if !utf8.ValidString(cut) {
		t.Fatal("truncated preview is not valid UTF-8")
	}
}
