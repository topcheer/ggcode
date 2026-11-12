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

func TestIssue4010ErrorPropagateRecordsConsensusFiring(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)
	anchor := "if propGuidance := a.errorPropagate.recordResult"
	idx := strings.Index(text, anchor)
	if idx < 0 {
		t.Fatal("errorPropagate branch not found")
	}
	window := text[idx : idx+900]
	fireAt := strings.Index(window, `recordFiring("Error Propagation"`)
	appendAt := strings.Index(window, "appendGuidance(&result, propGuidance)")
	if fireAt < 0 {
		t.Fatal("errorPropagate branch must recordFiring for cross-detector consensus (#4010)")
	}
	if appendAt < 0 || fireAt > appendAt {
		t.Fatal("recordFiring must accompany the guidance append (firing-before-append order)")
	}
}
