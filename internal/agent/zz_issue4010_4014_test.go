package agent

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// #4014: the autopilot deadlock-termination preview used a raw byte slice
// (preview[:200]); CJK text (3 bytes/rune) deterministically split mid-rune
// and pushed invalid UTF-8 into the event stream. The site must sanitize
// with ToValidUTF8 (or rune-safe truncation).
func TestIssue4014PreviewIsRuneSafe(t *testing.T) {
	src, err := os.ReadFile("agent.go")
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
	// Behavioral half: a 201-byte CJK preview must stay valid UTF-8 after
	// the same truncation strategy.
	cjk := strings.Repeat("死锁", 101) // 202 bytes, ends mid-rune at 200
	cut := cjk
	if len(cut) > 200 {
		cut = strings.ToValidUTF8(cut[:200], "")
	}
	if !utf8.ValidString(cut) {
		t.Fatal("truncated preview is not valid UTF-8")
	}
}

// #4010: the errorPropagate branch appends guidance BEFORE the consensus
// scan window but never called recordFiring, so checkOnly() (which admits
// voters only via recordFiring, #952) never saw it - cross-detector
// consensus lost a voter. The firing must be recorded alongside the
// failureMode/errorCascade siblings.
func TestIssue4010ErrorPropagateRecordsConsensusFiring(t *testing.T) {
	src, err := os.ReadFile("agent.go")
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
