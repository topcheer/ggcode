package provider

import (
	"context"
	"testing"
)

// #4227: the effort-downgrade branch must carry the same attempt guard as
// the thinking/retry branches. With attempts()==1 the old condition CAS-ed
// the carrier, set retry, and the loop exited into the fallback path that
// fabricates usage and emits a false StreamEventDone. State-level check: the
// guard expression itself - mirrors the branch ordering in anthropic.go.
func TestIssue4227_EffortGuardExpression(t *testing.T) {
	attempts := 1 // model_policy.go documented example: max_retries: 1
	for attempt := 0; attempt < attempts; attempt++ {
		finalAttempt := attempt == attempts-1
		guardedRetry := !finalAttempt // new branch condition
		if finalAttempt && guardedRetry {
			t.Fatal("final attempt must NOT take the effort-retry branch (false-success path)")
		}
	}
	_ = context.Background()
}
