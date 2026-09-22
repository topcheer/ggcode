package tool

// Windows browser-timeout pins: `chrome --version` (GUI-subsystem binary
// under AV/EDR hooks may never write/exit) and the tab-start handshake
// (fresh-profile Chrome boot) were the only two unbounded waits in the
// browser action chain - every action re-ran them (failed starts are not
// cached), so one hung probe presented as "all browser calls time out".
// These pins hold the two bounded windows.

import (
	"os"
	"strings"
	"testing"
)

func TestBrowserWindowsTimeoutPins(t *testing.T) {
	b, err := os.ReadFile("browser.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	// Pin 1: the version probe must be a CommandContext with a deadline -
	// a plain exec.Command there is the indefinite-hang regression.
	i := strings.Index(src, "func getChromeVersion")
	if i < 0 {
		t.Fatal("getChromeVersion not found")
	}
	body := src[i:]
	if !strings.Contains(body, "exec.CommandContext") || !strings.Contains(body, "WithTimeout") {
		t.Fatal("getChromeVersion must run under a deadline (Windows chrome.exe hang guard)")
	}

	// Pin 2: the first tab Run must be bounded by a timer race, NOT by a
	// WithTimeout wrapper around the Run context. chromedp binds the tab
	// session to the first-Run context; cancelling that wrapper killed the
	// just-booted session and every later action failed with "context
	// canceled" (the 2026-09 regression this pin guards against).
	j := strings.Index(src, "taskCtx, cancel := chromedp.NewContext")
	if j < 0 {
		t.Fatal("tab creation site not found")
	}
	span := src[j : j+3000]
	if !strings.Contains(span, "go func() { startDone <- chromedp.Run(taskCtx) }()") {
		t.Fatal("tab-start chromedp.Run must race a timer goroutine for its bound")
	}
	if !strings.Contains(span, "time.After(60 * time.Second)") {
		t.Fatal("tab-start bound must remain 60s")
	}
	if strings.Contains(span, "WithTimeout(taskCtx") {
		t.Fatal("forbidden: a WithTimeout wrapper on the first Run context kills the session (chromedp docs)")
	}
}
