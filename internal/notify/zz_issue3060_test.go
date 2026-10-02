package notify

import (
	"strings"
	"testing"
)

// #3060-C1: a body containing quotes/backslashes must produce a valid
// AppleScript double-quoted literal - no unescaped quote may remain inside.
func TestIssue3060_AppleScriptEscape(t *testing.T) {
	evil := "done\" with title \"evil\n\\backslash"
	esc := appleScriptEscape(evil)
	// Removing escaped-quote sequences must leave no raw quote, and no lone
	// backslash that AppleScript would misread.
	trimmed := strings.ReplaceAll(esc, `\"`, "")
	if strings.Contains(trimmed, `"`) {
		t.Fatalf("unescaped quote in body literal: %q", esc)
	}
	if !strings.Contains(esc, `\\backslash`) {
		t.Fatalf("backslash must be doubled: %q", esc)
	}
}
