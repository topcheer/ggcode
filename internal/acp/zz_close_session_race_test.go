package acp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestCloseSessionConcurrentCloseNoRace verifies closeSession reads the
// transport via transportSnapshot: Close() nils c.transport under c.mu, and
// EnsureReady's cleanup path can call closeSession concurrently with a
// user-initiated Close() (e.g. delegate tool teardown). The bare read that
// preceded the snapshot raced the write in Close (client.go:588).
// Note: Go evaluates `sessionID == "" || ...` left-to-right, but the guard
// still reads the transport field, so this exercises the exact access.
func TestCloseSessionConcurrentCloseNoRace(t *testing.T) {
	c := &Client{transport: NewTransport(&bytes.Buffer{}, &bytes.Buffer{})}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = c.Close()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200000; i++ {
			c.closeSession("")
		}
		close(stop)
	}()
	wg.Wait()
}

// TestPollForTokenZeroExpiresInFallsBack verifies the expires_in guard: an
// endpoint that omits expires_in decodes to ExpiresIn == 0, which used to
// fire time.After(0) and fail the flow with "device code expired" before the
// first poll ever ran. It must now fall back to the RFC 8628 default and
// keep polling: the token endpoint below grants immediately, so the flow
// must succeed.
func TestPollForTokenZeroExpiresInFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"bearer"}`))
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	tok, err := ah.pollForToken(context.Background(), &DeviceCodeResponse{
		DeviceCode: "dc",
		Interval:   1,
		ExpiresIn:  0, // endpoint omitted expires_in
	})
	if err != nil {
		t.Fatalf("ExpiresIn=0 with instantly-granting token endpoint: expected success, got %v", err)
	}
	if tok != "tok" {
		t.Fatalf("expected token %q, got %q", "tok", tok)
	}
}
