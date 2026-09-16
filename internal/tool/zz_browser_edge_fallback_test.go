package tool

// Windows Edge-fallback pins: Edge is the same Chromium engine with full
// CDP support and ships on virtually every Windows machine - a user who
// never installed Chrome still gets a working browser tool. Chrome stays
// ahead of Edge in the lookup order (a deliberate Chrome install wins;
// Edge is the zero-install floor).

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

	// All three canonical Edge install locations are registered.
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

	// Ordering: every Chrome path must appear before the first Edge path -
	// the lookup falls back to Edge only when no Chrome exists.
	firstEdge := strings.Index(body, `Microsoft\Edge\Application\msedge.exe`)
	if firstEdge < 0 {
		t.Fatal("no Edge path found at all")
	}
	lastChrome := strings.LastIndex(body, `Google\Chrome\Application\chrome.exe`)
	if lastChrome > firstEdge {
		t.Errorf("Chrome must precede Edge in the Windows lookup (Chrome at %d, Edge at %d)", lastChrome, firstEdge)
	}

	// The not-found hint must tell Windows users Edge works too (assert on
	// the source literal - chromeNotFoundHelp() branches on runtime.GOOS
	// and this test also runs on darwin/linux CI).
	windowsHint := `winget install Microsoft.Edge to restore it`
	if !strings.Contains(body, windowsHint) {
		t.Error("windows not-found hint should mention the Edge fallback")
	}
}
