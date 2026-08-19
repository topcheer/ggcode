package main

import (
	"context"
	"testing"
	"time"
)

// Regression guard for #746: perCallTimeout must return a fresh context with
// a full budget and cancel the previous one, so each probe API call
// (CountTokens / Chat / ChatStream) gets its own timeoutSec budget instead
// of sharing one per-endpoint ctx that lets a slow Chat misreport Stream.
func TestPerCallTimeoutFreshBudgetAndCancelsPrior(t *testing.T) {
	prior, priorCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer priorCancel()

	gotCtx, gotCancel := perCallTimeout(prior, priorCancel, 20)
	defer gotCancel()

	if err := prior.Err(); err != context.Canceled {
		t.Errorf("prior context must be cancelled, got %v", err)
	}
	dl, ok := gotCtx.Deadline()
	if !ok {
		t.Fatal("new context must carry a deadline")
	}
	// Fresh budget: remaining time must be well above the 10s prior budget
	// and close to the full 20s.
	if remain := time.Until(dl); remain < 19*time.Second {
		t.Errorf("new context budget = %v, want ~20s (full per-call budget)", remain)
	}
	if err := gotCtx.Err(); err != nil {
		t.Errorf("new context must be live, got %v", err)
	}
}
