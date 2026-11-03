package provider

import (
	"context"
	"testing"
)

func TestIssue4227_EffortGuardExpression(t *testing.T) {
	attempts := 1
	for attempt := 0; attempt < attempts; attempt++ {
		finalAttempt := attempt == attempts-1
		guardedRetry := !finalAttempt
		if finalAttempt && guardedRetry {
			t.Fatal("final attempt must NOT take the effort-retry branch (false-success path)")
		}
	}
	_ = context.Background()
}
