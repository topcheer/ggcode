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

	// Pin 2: the first tab Run must be wrapped in a bounded window - an
	// unbounded handshake hangs the whole action when Chrome is half-alive.
	j := strings.Index(src, "taskCtx, cancel := chromedp.NewContext")
	if j < 0 {
		t.Fatal("tab creation site not found")
	}
	span := src[j : j+2000]
	if !strings.Contains(span, "startRunCtx, startCancel := context.WithTimeout(taskCtx") {
		t.Fatal("tab-start chromedp.Run must run under a startup timeout window")
	}
}
