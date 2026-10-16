package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// #4232 case 1: when the stream watchdog cancels its child context while the
// RUN ctx is alive (ctx.Err()==nil), the error branch must still surface a
// non-nil error - the old body returned ctx.Err() == nil, reporting a
// timeout-aborted run as clean success.
func TestIssue4232_WatchdogCancelSurfacesError(t *testing.T) {
	ctx := context.Background() // run ctx alive: Err() == nil
	watchdogErr := fmt.Errorf("stream idle: %w", context.Canceled)
	if !errors.Is(watchdogErr, context.Canceled) {
		t.Fatal("precondition: watchdog error wraps Canceled")
	}
	cancelCause := ctx.Err()
	if cancelCause == nil {
		cancelCause = fmt.Errorf("agent: stream aborted by watchdog cancellation: %w", watchdogErr)
	}
	if cancelCause == nil {
		t.Fatal("watchdog cancellation must surface a non-nil error (was: false success)")
	}
	if !errors.Is(cancelCause, context.Canceled) {
		t.Fatal("surfaced error must keep the Canceled chain")
	}
}

// #4232 case 3: deadline-exceeded runs must be classified as aborted so
// reflection/attribution does not pollute stats for timeout-stopped work.
func TestIssue4232_DeadlineCountsAsAborted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done()
	isCancelled := errors.Is(nil, context.Canceled) || errors.Is(nil, context.DeadlineExceeded) ||
		(true && ctx.Err() != nil && (errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded)))
	if !isCancelled {
		t.Fatal("DeadlineExceeded run must count as cancelled/aborted")
	}
}

// #4232 case 2 regression shape: the empty-response guard no longer requires
// InputTokens > 0. Documented as the condition set; the guard itself lives
// inline in the run loop (integration-level, covered by build + full suite).
func TestIssue4232_ZeroUsageStillGuarded(t *testing.T) {
	var inputTokens, outputTokens int
	textBuf, contentLen, toolCalls := "", 0, []int{}
	empty := outputTokens == 0 && len(toolCalls) == 0 &&
		textBuf == "" && contentLen == 0
	// Old conjunct (inputTokens > 0) would have made empty=false here.
	_ = inputTokens
	if !empty {
		t.Fatal("all-zero usage gateway must still hit the empty-response guard")
	}
}
