package image

import (
	"strings"
	"testing"
)

// TestWindowsListDisplaysScriptSetsDpiAwarenessBeforeQuery guards #976:
// the display-listing script must declare DPI awareness before it queries
// AllScreens, otherwise the reported bounds are virtualized 96-DPI logical
// coordinates on scaled displays and downstream Region captures (computed
// from DisplayInfo) are mispositioned relative to the DPI-aware capture
// scripts.
func TestWindowsListDisplaysScriptSetsDpiAwarenessBeforeQuery(t *testing.T) {
	script := buildWindowsListDisplaysScript()

	dpiIdx := strings.Index(script, "SetProcessDPIAware")
	if dpiIdx < 0 {
		t.Fatal("ListDisplays script is missing the SetProcessDPIAware preamble")
	}
	screensIdx := strings.Index(script, "AllScreens")
	if screensIdx < 0 {
		t.Fatal("ListDisplays script no longer queries AllScreens")
	}
	if dpiIdx > screensIdx {
		t.Fatalf("DPI awareness must be declared before AllScreens is queried (dpi=%d, screens=%d); "+
			"querying first returns virtualized 96-DPI coordinates that cannot be corrected afterwards", dpiIdx, screensIdx)
	}

	// The JSON contract consumed by ListDisplays must be preserved.
	if !strings.Contains(script, "ConvertTo-Json") {
		t.Fatal("ListDisplays script lost its ConvertTo-Json output")
	}
	for _, field := range []string{"index", "is_primary", "width", "height", "x", "y"} {
		if !strings.Contains(script, field+" = ") {
			t.Fatalf("ListDisplays script lost the %q field", field)
		}
	}
}

// TestWindowsDpiAwarenessSnippetIsShared guards the single source of truth:
// both the capture scripts and the display-listing script must use
// windowsDpiAwarenessSnippet so their coordinate spaces cannot drift apart
// (#976).
func TestWindowsDpiAwarenessSnippetIsShared(t *testing.T) {
	snippet := windowsDpiAwarenessSnippet
	if !strings.Contains(snippet, "SetProcessDPIAware()") {
		t.Fatal("DPI snippet must call SetProcessDPIAware()")
	}
	if !strings.Contains(snippet, "| Out-Null") {
		t.Fatal("DPI snippet should swallow the SetProcessDPIAware return value")
	}
	if strings.HasSuffix(strings.TrimRight(snippet, "\n"), "'") {
		t.Fatal("DPI snippet appears malformed: unterminated here-string")
	}
}
