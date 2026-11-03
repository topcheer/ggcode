package tool

import (
	"os"
	"strings"
	"testing"
)

func TestBrowserEdgeFallbackPaths(t *testing.T) {
	src, err := os.ReadFile("browser.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	for _, p := range []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	} {
		if !strings.Contains(body, p) {
			t.Errorf("Edge install location missing from Windows lookup: %s", p)
		}
	}
	if !strings.Contains(body, strings.Join([]string{"AppData", "Local", "Microsoft", "Edge", "Application", "msedge.exe"}, `\`)) &&
		!strings.Contains(body, `AppData\Local\Microsoft\Edge\Application\msedge.exe`) {
		t.Error("per-user Edge location missing from Windows lookup")
	}

	firstEdge := strings.Index(body, `Microsoft\Edge\Application\msedge.exe`)
	if firstEdge < 0 {
		t.Fatal("no Edge path found at all")
	}
	lastChrome := strings.LastIndex(body, `Google\Chrome\Application\chrome.exe`)
	if lastChrome > firstEdge {
		t.Errorf("Chrome must precede Edge in the Windows lookup (Chrome at %d, Edge at %d)", lastChrome, firstEdge)
	}

	windowsHint := `winget install Microsoft.Edge to restore it`
	if !strings.Contains(body, windowsHint) {
		t.Error("windows not-found hint should mention the Edge fallback")
	}
}
