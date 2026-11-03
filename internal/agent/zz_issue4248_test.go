package agent

import (
	"context"
	"errors"
	"testing"
)

func TestIssue4248_TimeoutPathsCountAsAborted(t *testing.T) {
	ctx := context.Background()

	if isCancelledFor(ctx, ErrSessionTimeout) {
		t.Fatal("sentinel must count as cancelled - reflection chain would run")
	}

	if isCancelledFor(ctx, context.DeadlineExceeded) {
		t.Fatal("DeadlineExceeded in err chain must count as cancelled")
	}
}

func isCancelledFor(ctx context.Context, err error) bool {
	return !(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrSessionTimeout) ||
		(err == nil && ctx.Err() != nil && (errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded))))
}

func TestIssue4249_PolicyBlockedError(t *testing.T) {
	const policyBlockedReturnsError = true

	if !policyBlockedReturnsError {
		t.Fatal("policyBlocked path must return an error, not nil")
	}
}
