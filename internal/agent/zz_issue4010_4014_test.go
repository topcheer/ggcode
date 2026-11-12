package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue4014PreviewIsRuneSafe(t *testing.T) {
	// #5543: the autopilot deadlock preview moved out of agent.go; scan
	// the stream-loop source files (shape-agnostic file targeting).
	text, err := readStreamLoopSource("agent.go", "run_no_tool_calls.go")
	if err != nil {
		t.Fatalf("read stream-loop source: %v", err)
	}
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
	// #5543: the errorPropagate branch moved out of agent.go; scan the
	// stream-loop source files (shape-agnostic file targeting).
	text, err := readStreamLoopSource("agent.go", "run_tool_call_idx.go")
	if err != nil {
		t.Fatalf("read stream-loop source: %v", err)
	}
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
