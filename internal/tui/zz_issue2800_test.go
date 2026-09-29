package tui

// #2800 source pins: aborting an in-progress edit with Esc must fully leave
// custom-add mode, and the `e` key-edit entry must never run while a stale
// customMode flag is set. The bug: Esc cleared only editingField, so
// customMode lingered with residual name/url inputs; a later `e` edit on an
// existing target was hijacked into the custom "key" branch of
// handleStreamPanelEnter - appending a bogus target (or failing "All fields
// required") instead of saving the edited key.

import (
	"os"
	"strings"
	"testing"
)

func streamPanelSrc2800(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("stream_panel.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIssue2800EscResetsCustomMode(t *testing.T) {
	src := streamPanelSrc2800(t)
	start := strings.Index(src, "case \"esc\":")
	if start < 0 {
		t.Fatal("esc branch not found")
	}
	end := strings.Index(src[start:], "case \"up\"")
	if end < 0 {
		t.Fatal("esc branch end not found")
	}
	esc := src[start : start+end]
	if !strings.Contains(esc, "p.customMode = false") {
		t.Error("esc edit-abort must reset p.customMode (#2800)")
	}
}

func TestIssue2800KeyEditEntryClearsCustomMode(t *testing.T) {
	src := streamPanelSrc2800(t)
	start := strings.Index(src, "case \"e\": // edit")
	if start < 0 {
		t.Fatal("e edit branch not found")
	}
	end := strings.Index(src[start:], "case \"d\":")
	if end < 0 {
		t.Fatal("e edit branch end not found")
	}
	e := src[start : start+end]
	if !strings.Contains(e, "p.customMode = false") {
		t.Error("e key-edit entry must clear stale customMode (#2800)")
	}
}

func TestIssue2800NoopUrlSelfAssignRemoved(t *testing.T) {
	src := streamPanelSrc2800(t)
	if strings.Contains(src, "p.urlInput.SetValue(p.urlInput.Value())") {
		t.Error("no-op url self-assignment residue should be gone (#2800)")
	}
}
