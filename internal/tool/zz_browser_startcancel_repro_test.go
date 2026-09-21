//go:build integration

package tool

// Regression test for the Windows "every browser call fails with context
// canceled" report (2026-09): commit 3bfd71be wrapped the first
// chromedp.Run in a WithTimeout and cancelled that wrapper immediately
// after Run returned. chromedp binds the tab session's run loop to the
// context passed to the first Run (attachTarget -> go c.Target.run(ctx)),
// so startCancel() killed the just-booted browser session while the
// browser process stayed alive - every subsequent action failed with
// "context canceled". chromedp's own docs warn against a timeout on the
// first Run call for exactly this reason.
//
// The test boots a REAL browser and runs two sequential evaluate actions
// on the blank tab (no network involved - immune to proxy/site flakiness):
// the bug fails the first or second action with "context canceled".
// Skipped when no Chrome/Chromium/Edge is installed (CI). Integration tag:
// real-browser behavioral tests live here, not in the default suite.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestBrowserTabSurvivesStartupBound(t *testing.T) {
	if findChromeExecutable() == "" {
		t.Skip("no Chrome/Chromium/Edge executable available")
	}

	b := NewBrowser()
	// Full teardown: doCloseSession only closes the tab; the allocator's
	// browser process outlives it and leaks a headless Chrome per run.
	defer b.Close()

	runEvaluate := func(tag string) string {
		t.Helper()
		input, _ := json.Marshal(map[string]interface{}{
			"action":      "evaluate",
			"description": "startcancel repro: " + tag,
			"expression":  "6 * 7",
		})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		res, err := b.Execute(ctx, input)
		if err != nil {
			t.Fatalf("evaluate %s: %v", tag, err)
		}
		if res.IsError {
			t.Fatalf("evaluate %s failed (startup-bound regression if 'context canceled'): %s", tag, res.Content)
		}
		return res.Content
	}

	// First action boots the browser (the buggy startCancel fired here).
	if got := runEvaluate("first"); got == "" {
		t.Fatalf("first evaluate returned empty content")
	}
	// Second action on the SAME tab is where the killed session shows up.
	if got := runEvaluate("second"); got == "" {
		t.Fatalf("second evaluate returned empty content")
	}
}
