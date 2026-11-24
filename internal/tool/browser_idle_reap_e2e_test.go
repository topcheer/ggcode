//go:build integration && !windows

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/chromedp/cdproto/page"
	chromedp "github.com/chromedp/chromedp"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// chromeBrowsersOwnedBy counts headless Chrome *browser* processes (not
// helpers) whose parent is pid - i.e. launched by this test process.
func chromeBrowsersOwnedBy(t *testing.T, pid int) int {
	t.Helper()
	out, err := exec.Command("ps", "-eo", "ppid=,args=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(fields[0])
		if err != nil || ppid != pid {
			continue
		}
		args := strings.Join(fields[1:], " ")
		if strings.Contains(args, "Chrome") && strings.Contains(args, "--headless") && !strings.Contains(args, "--type=") {
			n++
		}
	}
	return n
}

func waitChromeBrowsersOwnedBy(t *testing.T, pid, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if chromeBrowsersOwnedBy(t, pid) == want {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// End to end on the real path: a browser the agent never closed is shut down
// by the idle reaper - the Chrome process is gone, not just forgotten.
func TestBrowserIdleReapKillsChromeProcess(t *testing.T) {
	if findChromeExecutable() == "" {
		t.Skip("Chrome/Chromium not available")
	}
	pid := os.Getpid()
	if n := chromeBrowsersOwnedBy(t, pid); n != 0 {
		t.Skipf("precondition: this test process already owns %d chrome browser(s)", n)
	}

	b := NewBrowser()
	defer b.Close()

	// The lifecycle under test is process teardown, not network reachability:
	// on a loaded machine (or a flaky network) the first page load can exceed
	// the navigate timeout, so give it a few attempts before skipping.
	var res Result
	var err error
	loaded := false
	for attempt := 1; attempt <= 3 && !loaded; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		input, _ := json.Marshal(map[string]interface{}{
			"action": "navigate", "url": "https://example.com/", "wait_timeout": 60,
			"description": fmt.Sprintf("idle reap e2e: navigate (attempt %d)", attempt),
		})
		res, err = b.Execute(ctx, input)
		cancel()
		loaded = err == nil && !res.IsError
		if !loaded {
			t.Logf("navigate attempt %d failed: err=%v res=%s", attempt, err, res.Content)
			time.Sleep(2 * time.Second)
		}
	}
	if !loaded {
		t.Skip("could not load a page in this environment; lifecycle assertion needs one successful navigate")
	}
	if !waitChromeBrowsersOwnedBy(t, pid, 1, 15*time.Second) {
		t.Fatal("expected exactly one headless Chrome browser owned by this process after navigate")
	}

	// Simulate the profile having been idle past the threshold.
	b.mu.Lock()
	b.profiles["default"].lastUsed = time.Now().Add(-defaultBrowserIdleTimeout - time.Minute)
	b.mu.Unlock()

	b.reapIdleBrowserProfiles(time.Now())

	if !waitChromeBrowsersOwnedBy(t, pid, 0, 15*time.Second) {
		t.Fatalf("idle reaper must shut the Chrome process down (still %d alive)", chromeBrowsersOwnedBy(t, pid))
	}
	b.mu.Lock()
	left := len(b.profiles)
	b.mu.Unlock()
	if left != 0 {
		t.Fatalf("profile map must be empty after the reap, got %d", left)
	}
}

// A profile whose Chrome process died (crash, OOM kill, operator cleanup) must
// not brick the browser tool until restart: getSession drops the dead profile
// and starts a fresh browser once.
func TestBrowserSessionSelfHealsAfterBrowserDeath(t *testing.T) {
	if findChromeExecutable() == "" {
		t.Skip("Chrome/Chromium not available")
	}
	b := NewBrowser()
	defer b.Close()

	deadCtx, deadCancel := context.WithCancel(context.Background())
	deadCancel() // what chromedp leaves behind when the process is gone
	deadTabCtx, deadTabCancel := context.WithCancel(context.Background())
	deadTabCancel()
	dead := &browserProfile{
		allocCtx:    deadCtx,
		allocCancel: func() {},
		tabs:        map[string]*browserTab{"default": {ctx: deadTabCtx, cancel: deadTabCancel}},
		lastUsed:    time.Now(),
	}
	b.profiles["default"] = dead

	tab, err := b.getSession("default", "default", nil)
	if err != nil {
		t.Fatalf("getSession must recover from a dead browser process, got: %v", err)
	}
	if tab == nil || tab.ctx.Err() != nil {
		t.Fatal("expected a live tab on the fresh browser")
	}
	b.mu.Lock()
	fresh := b.profiles["default"]
	b.mu.Unlock()
	if fresh == dead {
		t.Fatal("dead profile must be replaced, not reused")
	}
	if n := chromeBrowsersOwnedBy(t, os.Getpid()); n != 1 {
		t.Fatalf("expected exactly one fresh Chrome browser, got %d", n)
	}
}

// After an action the headless tab is frozen (no timers / rAF / animation ->
// the SwiftShader GPU process goes idle instead of spinning at 100% CPU per
// core); the next action resumes it first.
func TestBrowserFreezesHeadlessTabAfterAction(t *testing.T) {
	if findChromeExecutable() == "" {
		t.Skip("Chrome/Chromium not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><title>freeze</title><script>window.c=0;setInterval(()=>window.c++,50)</script>"))
	}))
	defer srv.Close()

	b := NewBrowser()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := b.Execute(ctx, mustJSON(t, map[string]interface{}{"action": "navigate", "url": srv.URL, "description": "freeze e2e"}))
	if err != nil || res.IsError {
		t.Fatalf("navigate failed: err=%v res=%+v", err, res)
	}
	b.mu.Lock()
	tab := b.profiles["default"].tabs["default"]
	b.mu.Unlock()

	// read returns the page counter; ok=false means the page did not run JS
	// within the deadline, i.e. it is frozen.
	read := func() (c float64, ok bool) {
		ctx, cancel := context.WithTimeout(tab.ctx, 2*time.Second)
		defer cancel()
		v, err := chromedp.Run(ctx, chromedp.Evaluate[float64]("window.c"))
		if err != nil {
			return 0, false
		}
		return v, true
	}
	time.Sleep(300 * time.Millisecond)
	c1, ok1 := read()
	time.Sleep(400 * time.Millisecond)
	c2, ok2 := read()
	if ok1 && ok2 && c2 > c1 {
		t.Fatalf("timer must not advance while frozen: c1=%v c2=%v", c1, c2)
	}

	b.setProfileLifecycle("default", page.SetWebLifecycleStateStateActive)
	time.Sleep(500 * time.Millisecond)
	c3, ok3 := read()
	if !ok3 {
		t.Fatal("resumed page must run JS again")
	}
	if ok1 && c3 <= c2 {
		t.Fatalf("timer must advance after resume: c2=%v c3=%v", c2, c3)
	}
}
