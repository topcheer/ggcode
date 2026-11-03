package image

import (
	"strings"
	"testing"
)

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

	if !strings.Contains(script, "ConvertTo-Json") {
		t.Fatal("ListDisplays script lost its ConvertTo-Json output")
	}
	for _, field := range []string{"index", "is_primary", "width", "height", "x", "y"} {
		if !strings.Contains(script, field+" = ") {
			t.Fatalf("ListDisplays script lost the %q field", field)
		}
	}
}

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

func TestWindowsDpiSnippetHandlesFalseReturn(t *testing.T) {
	snippet := windowsDpiAwarenessSnippet
	if !strings.Contains(snippet, "if (-not") {
		t.Fatal("DPI snippet must check the context API's boolean return (#1754)")
	}
	if !strings.Contains(snippet, "SetProcessDPIAware()") {
		t.Fatal("legacy fallback must remain")
	}
}
