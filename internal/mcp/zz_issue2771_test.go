package mcp

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

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

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("first callback: %v", err)
	}
	resp.Body.Close()

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

	case <-time.After(8 * time.Second):
		t.Fatal("second /callback request blocked >8s with a full channel - handler goroutine wedged on bare send (#2771)")
	}

	select {
	case <-ch:
	default:
	}
}

func TestIssue2771LateCallbackAfterTimeoutDoesNotLeak(t *testing.T) {
	h := NewOAuthHandler("srv-late", "http://127.0.0.1:1", nil)
	port, ch, srv, err := h.startCallbackServer("state-late")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer func() { _ = srv.Close() }()

	urlOK := fmt.Sprintf("http://127.0.0.1:%d/callback?code=abc&state=state-late", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(urlOK)
	if err != nil {
		t.Fatalf("first callback: %v", err)
	}
	resp.Body.Close()

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

	select {
	case <-ch:
	default:
		t.Fatal("first result was lost from the buffer")
	}
}
