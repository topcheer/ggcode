package notify

import (
	"strings"
	"testing"
)

func TestIssue3060_AppleScriptEscape(t *testing.T) {
	evil := "done\" with title \"evil\n\\backslash"
	esc := appleScriptEscape(evil)

	trimmed := strings.ReplaceAll(esc, `\"`, "")
	if strings.Contains(trimmed, `"`) {
		t.Fatalf("unescaped quote in body literal: %q", esc)
	}
	if !strings.Contains(esc, `\\backslash`) {
		t.Fatalf("backslash must be doubled: %q", esc)
	}
}
