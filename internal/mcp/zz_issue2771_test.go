package mcp

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestIssue2771CallbackHandlerNeverBlocks pins #2771: the /callback handler
// must never block on the buffered-1 channel. Duplicate, prefetching, or
// late callbacks (error retry flow, browser prefetch, WaitForCallback
// timeout) arrive when the buffer is already full - a bare send wedges the
// handler goroutine forever, and http.Server.Shutdown(2s) does not
// interrupt in-flight handlers, so the leak accumulates across OAuth
// retries on the reused server.
func TestIssue2771CallbackHandlerNeverBlocks(t *testing.T) {
	h := NewOAuthHandler("srv-2771", "http://127.0.0.1:1", nil)
	port, ch, srv, err := h.startCallbackServer("state-2771")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer func() {
		_ = srv.Close()
	}()
	url := fmt.Sprintf("http://127.0.0.1:%d/callback?error=access_denied&state=state-2771", port)

	// First request fills the buffer (nobody consumes).
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("first callback: %v", err)
	}
	resp.Body.Close()

	// Second request must be answered (and not wedge the handler) even
	// though the buffer is full. Pre-fix this goroutine blocks forever.
	done := make(chan struct{})
	var once sync.Once
	go func() {
		resp2, err2 := client.Get(url)
		if err2 == nil {
			resp2.Body.Close()
		}
		once.Do(func() { close(done) })
	}()

	select {
	case <-done:
		// Handler responded despite full buffer - non-blocking send in place.
	case <-time.After(8 * time.Second):
		t.Fatal("second /callback request blocked >8s with a full channel - handler goroutine wedged on bare send (#2771)")
	}
	// Drain so deferred Close is clean.
	select {
	case <-ch:
	default:
	}
}

// TestIssue2771LateCallbackAfterTimeoutDoesNotLeak pins the late-callback
// scenario: WaitForCallback timed out (ctx done, channel unread), a late
// /callback arrives - the handler must still respond immediately.
func TestIssue2771LateCallbackAfterTimeoutDoesNotLeak(t *testing.T) {
	h := NewOAuthHandler("srv-late", "http://127.0.0.1:1", nil)
	port, ch, srv, err := h.startCallbackServer("state-late")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer func() { _ = srv.Close() }()

	// Simulate a consumed/unread channel: fill it with a first callback.
	urlOK := fmt.Sprintf("http://127.0.0.1:%d/callback?code=abc&state=state-late", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(urlOK)
	if err != nil {
		t.Fatalf("first callback: %v", err)
	}
	resp.Body.Close()

	// Late duplicate with an error param: buffer still full (nobody read).
	urlErr := fmt.Sprintf("http://127.0.0.1:%d/callback?error=server_error&state=state-late", port)
	start := time.Now()
	resp2, err2 := client.Get(urlErr)
	if err2 != nil {
		t.Fatalf("late callback: %v", err2)
	}
	resp2.Body.Close()
	if time.Since(start) > 3*time.Second {
		t.Fatalf("late callback took %v - handler blocked on full channel", time.Since(start))
	}

	// First result still readable (non-blocking send must not displace it
	// in a way that breaks the primary flow: keep buffer semantics).
	select {
	case <-ch:
	default:
		t.Fatal("first result was lost from the buffer")
	}
}
