package provider

// #4729 regression tests: connection timeouts must retry in place (per-attempt
// deadline renewal + lifted attempt cap / backoff budget for timeout-class
// errors), the periodic notice fires every 3rd timeout retry, and the
// friendly-error wrapper keeps the error chain alive for UserFacingErrorLang.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeTimeoutNetErr is a net.Error whose Timeout() reports true.
type fakeTimeoutNetErr struct{}

func (fakeTimeoutNetErr) Error() string   { return "connection timed out (fake)" }
func (fakeTimeoutNetErr) Timeout() bool   { return true }
func (fakeTimeoutNetErr) Temporary() bool { return true }

// fakeNoTimeoutNetErr is a net.Error that is NOT a timeout (refused-class).
type fakeNoTimeoutNetErr struct{}

func (fakeNoTimeoutNetErr) Error() string   { return "connection refused (fake)" }
func (fakeNoTimeoutNetErr) Timeout() bool   { return false }
func (fakeNoTimeoutNetErr) Temporary() bool { return true }

func TestIssue4729_IsTimeoutClassError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"net timeout", fakeTimeoutNetErr{}, true},
		{"wrapped net timeout", fmt.Errorf("post: %w", fakeTimeoutNetErr{}), true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline", fmt.Errorf("post: %w", context.DeadlineExceeded), true},
		{"string connection timed out", errors.New("Post \"https://api\": Connection Timed Out."), true},
		{"string context deadline exceeded", errors.New("Post: context deadline exceeded"), true},
		{"refused is not timeout class", fakeNoTimeoutNetErr{}, false},
		{"string refused", errors.New("dial tcp: connection refused"), false},
		{"generic 500", errors.New("status code: 500"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTimeoutClassError(tc.err); got != tc.want {
				t.Fatalf("isTimeoutClassError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIssue4729_TimeoutRetryNoticeCadence(t *testing.T) {
	err := fmt.Errorf("post: %w", context.DeadlineExceeded)
	for attempt, wantDue := range map[int]bool{
		0: false, // retry 1
		1: false, // retry 2
		2: true,  // retry 3 -> notice
		3: false, // retry 4
		4: false, // retry 5
		5: true,  // retry 6 -> notice
		8: true,  // retry 9 -> notice
	} {
		got := TimeoutRetryNotice(err, attempt)
		if (got != "") != wantDue {
			t.Fatalf("attempt %d (retry %d): notice due=%v, got %q", attempt, attempt+1, wantDue, got)
		}
		if wantDue && !strings.Contains(got, fmt.Sprintf("Timeout retry %d", attempt+1)) {
			t.Fatalf("notice %q missing retry number %d", got, attempt+1)
		}
	}
	// Non-timeout errors never produce a notice.
	if got := TimeoutRetryNotice(errors.New("status code: 503"), 2); got != "" {
		t.Fatalf("non-timeout notice should be empty, got %q", got)
	}
}

func TestIssue4729_RetryWithBackoffCtxTimeoutRetriesInPlace(t *testing.T) {
	// A timeout-class failure must keep retrying BEYOND maxAttempts: the old
	// loop returned at i == maxAttempts-1 and the run died after one retry.
	calls := 0
	origSleep := retrySleep
	retrySleep = func(context.Context, time.Duration) error { return nil } // no real sleeping in tests
	defer func() { retrySleep = origSleep }()

	const succeedAfter = 5
	err := retryWithBackoffCtx(context.Background(), func() error {
		calls++
		if calls >= succeedAfter {
			return nil
		}
		return fmt.Errorf("post: %w", context.DeadlineExceeded)
	}, 1, true) // cap of 1, in-place on: old code returned after the FIRST timeout
	if err != nil {
		t.Fatalf("expected in-place retry to succeed, got %v", err)
	}
	if calls != succeedAfter {
		t.Fatalf("calls = %d, want %d", calls, succeedAfter)
	}
}

func TestIssue4729_RetryWithBackoffCtxNonTimeoutStillCapped(t *testing.T) {
	// Non-timeout retryable errors (5xx) keep the normal attempt cap.
	calls := 0
	err := retryWithBackoffCtx(context.Background(), func() error {
		calls++
		return errors.New("status code: 503")
	}, 2, false)
	if err == nil {
		t.Fatal("expected capped failure")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (cap respected)", calls)
	}
}

func TestIssue4729_WrapFriendlyKeepsChain(t *testing.T) {
	// DeadlineExceeded chain survives the friendly wrapper.
	inner := fmt.Errorf("dial tcp: %w", context.DeadlineExceeded)
	w := WrapFriendly(inner)
	if w.Error() != FriendlyError(inner) {
		t.Fatalf("wrapped text %q != FriendlyError %q", w.Error(), FriendlyError(inner))
	}
	if !errors.Is(w, context.DeadlineExceeded) {
		t.Fatal("errors.Is(DeadlineExceeded) lost through WrapFriendly")
	}
	// net.Error chain survives the friendly wrapper.
	innerNet := fmt.Errorf("post: %w", fakeTimeoutNetErr{})
	w2 := WrapFriendly(innerNet)
	var netErr net.Error
	if !errors.As(w2, &netErr) {
		t.Fatal("errors.As(net.Error) lost through WrapFriendly")
	}
	if !netErr.Timeout() {
		t.Fatal("net.Error.Timeout() lost through WrapFriendly")
	}
	if WrapFriendly(nil) != nil {
		t.Fatal("WrapFriendly(nil) must be nil")
	}
}

func TestIssue4729_UserFacingTimeoutStringMapped(t *testing.T) {
	// String-form "connection timed out" (relay that destroyed the typed
	// chain) must map to the network branch, not the blind-spot fallback.
	msg := UserFacingErrorLang(errors.New(`Post "https://api.example.com/v1": connection timed out`), "en")
	if !strings.Contains(msg, "connect") && !strings.Contains(msg, "network") {
		t.Fatalf("expected network-branch mapping, got blind-spot text: %q", msg)
	}
	zh := UserFacingErrorLang(errors.New(`Post "https://api.example.com/v1": connection timed out`), "zh")
	if !strings.Contains(zh, "无法连接") {
		t.Fatalf("expected zh network mapping, got %q", zh)
	}
}
