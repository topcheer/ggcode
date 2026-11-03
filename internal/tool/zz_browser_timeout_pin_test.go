package tool

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

	i := strings.Index(src, "func getChromeVersion")
	if i < 0 {
		t.Fatal("getChromeVersion not found")
	}
	body := src[i:]
	if !strings.Contains(body, "exec.CommandContext") || !strings.Contains(body, "WithTimeout") {
		t.Fatal("getChromeVersion must run under a deadline (Windows chrome.exe hang guard)")
	}

	j := strings.Index(src, "taskCtx, cancel := chromedp.NewContext")
	if j < 0 {
		t.Fatal("tab creation site not found")
	}
	span := src[j : j+3000]

	if !strings.Contains(span, `startDone <- chromedp.Do(taskCtx)`) || !strings.Contains(span, "safego.Go(") {
		t.Fatal("tab-start chromedp.Do must race a timer goroutine (safego-wrapped) for its bound")
	}
	if !strings.Contains(span, "time.After(60 * time.Second)") {
		t.Fatal("tab-start bound must remain 60s")
	}
	if strings.Contains(span, "WithTimeout(taskCtx") {
		t.Fatal("forbidden: a WithTimeout wrapper on the first Run context kills the session (chromedp docs)")
	}
}
