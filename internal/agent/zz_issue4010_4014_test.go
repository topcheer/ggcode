package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #4014: the autopilot deadlock-preview truncation had to be rune-safe. That
// preview site was removed together with the deterministic no-progress
// terminator (the AI now decides when to stop), so this pin now (a) keeps the
// terminator gone and (b) still requires ToValidUTF8 should a byte-slice
// preview of the agent's text ever be reintroduced.
func TestIssue4014PreviewIsRuneSafe(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)
	if strings.Contains(text, "terminating to avoid deadlock") {
		t.Fatal("the autopilot no-progress force-terminate must stay removed (the AI decides when to stop)")
	}
	anchor := "preview := textBuf"
	idx := strings.Index(text, anchor)
	if idx < 0 {
		return // no preview site anymore - nothing to truncate
	}
	window := text[idx : idx+700]
	if !strings.Contains(window, "ToValidUTF8") {
		t.Fatal("preview truncation must sanitize with strings.ToValidUTF8 (#4014)")
	}

	cjk := strings.Repeat("死锁", 101)
	cut := cjk
	if len(cut) > 200 {
		cut = strings.ToValidUTF8(cjk[:200], "")
	}
	if !utf8.ValidString(cut) {
		t.Fatal("truncated preview is not valid UTF-8")
	}
}
