//go:build integration

package tool

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

	if got := runEvaluate("first"); got == "" {
		t.Fatalf("first evaluate returned empty content")
	}

	if got := runEvaluate("second"); got == "" {
		t.Fatalf("second evaluate returned empty content")
	}
}
