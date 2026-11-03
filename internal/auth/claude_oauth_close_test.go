package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

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

func TestClaudeOAuthFlow_CloseWithRealCallbackWins(t *testing.T) {
	flow := &ClaudeOAuthFlow{
		callbackCh: make(chan claudeCallbackResult, 1),
	}
	flow.callbackCh <- claudeCallbackResult{Code: "auth-code-123", IsAutomatic: true}

	flow.Close()

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
	_ = http.Server{}
}
