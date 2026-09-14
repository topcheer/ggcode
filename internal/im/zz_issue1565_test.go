package im

// #1565 case B: PRIVMSG pacing is per-connection - two concurrent
// pacePrivMsg acquisitions must be spaced at least ~the inter-message
// delay apart (the old per-call `sent` flags let concurrent Sends
// interleave freely and Twitch drops over-budget messages silently).
// Case C: exit_plan_mode falls back to tr.Detail DIRECTLY (it is the
// display string, never JSON) instead of the dead extractArgValue call.

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestIssue1565PacePrivMsgSpacesConcurrentSends(t *testing.T) {
	a := &twitchAdapter{nick: "bot"}
	a.lastPrivMsgAt = time.Now().Add(-time.Minute) // cold
	var wg sync.WaitGroup
	got := make([]time.Time, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := a.pacePrivMsg(context.Background()); err != nil {
				t.Errorf("pace: %v", err)
			}
			got[i] = time.Now()
		}(i)
	}
	wg.Wait()
	first, second := got[0], got[1]
	if second.Before(first) {
		first, second = second, first
	}
	if gap := second.Sub(first); gap < 1200*time.Millisecond {
		t.Fatalf("concurrent acquisitions must be spaced ~1500ms, got %v", gap)
	}
}

func TestIssue1565ExitPlanDetailFallback(t *testing.T) {
	tr := &ToolResultInfo{
		ToolName: "exit_plan_mode",
		Detail:   "Plan: refactor the adapter layer",
	}
	if got := formatToolResultText(tr); got != "Plan: refactor the adapter layer" {
		t.Fatalf("plan notification must surface Detail directly, got %q", got)
	}
	// Args still wins when present.
	tr2 := &ToolResultInfo{
		ToolName: "exit_plan_mode",
		Args:     `{"plan":"from-args"}`,
		Detail:   "display text",
	}
	if got := formatToolResultText(tr2); got != "from-args" {
		t.Fatalf("Args plan must win, got %q", got)
	}
}
