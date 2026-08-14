package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestClaudeOAuthFlow_CloseWakesWaiter verifies that Close() unblocks a
// goroutine waiting in WaitForClaudeAuthCode immediately (#295): previously
// Close only shut down the HTTP server, leaving the waiter blocked on
// callbackCh until its context timed out (up to 5 minutes of fake waiting
// when a superseded flow was closed).
func TestClaudeOAuthFlow_CloseWakesWaiter(t *testing.T) {
	flow := &ClaudeOAuthFlow{
		callbackCh: make(chan claudeCallbackResult, 1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, _, err := WaitForClaudeAuthCode(ctx, flow)
		done <- result{err}
	}()

	// Give the waiter a moment to enter the select, then close the flow.
	time.Sleep(50 * time.Millisecond)
	flow.Close()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("waiter should return an error after Close, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForClaudeAuthCode still blocked 2s after Close — waiter must be woken by the cancel signal (#295)")
	}
}

// TestClaudeOAuthFlow_CloseWithRealCallbackWins ensures the cancel signal does
// not clobber an already-delivered callback result: the buffered channel
// (capacity 1) means a pending real result makes the non-blocking cancel send
// a no-op.
func TestClaudeOAuthFlow_CloseWithRealCallbackWins(t *testing.T) {
	flow := &ClaudeOAuthFlow{
		callbackCh: make(chan claudeCallbackResult, 1),
	}
	flow.callbackCh <- claudeCallbackResult{Code: "auth-code-123", IsAutomatic: true}

	flow.Close() // cancel send must be dropped — real result already queued

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code, _, err := WaitForClaudeAuthCode(ctx, flow)
	if err != nil {
		t.Fatalf("real callback result should win over the cancel signal: %v", err)
	}
	if code != "auth-code-123" {
		t.Fatalf("expected real auth code, got %q", code)
	}
}

// TestClaudeOAuthFlow_CloseWithServer verifies Close works on a fully
// initialized flow (with a live callback HTTP server) without panicking and
// still wakes the waiter.
func TestClaudeOAuthFlow_CloseWithServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	flow, err := StartClaudeOAuthFlow(ctx)
	if err != nil {
		t.Skipf("cannot bind local OAuth listener in this environment: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() {
		_, _, werr := WaitForClaudeAuthCode(ctx, flow)
		waitDone <- werr
	}()

	time.Sleep(50 * time.Millisecond)
	flow.Close()

	select {
	case rerr := <-waitDone:
		if rerr == nil {
			t.Fatal("expected error from waiter after Close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiter still blocked after Close on a live flow (#295)")
	}
	_ = http.Server{} // keep net/http import meaningful if server path changes
}
