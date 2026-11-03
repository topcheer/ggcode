package acp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

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

func TestPollForTokenZeroExpiresInFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"bearer"}`))
	}))
	defer srv.Close()

	ah := &AuthHandler{accessTokenURL: srv.URL}
	tok, err := ah.pollForToken(context.Background(), &DeviceCodeResponse{
		DeviceCode: "dc",
		Interval:   1,
		ExpiresIn:  0,
	})
	if err != nil {
		t.Fatalf("ExpiresIn=0 with instantly-granting token endpoint: expected success, got %v", err)
	}
	if tok != "tok" {
		t.Fatalf("expected token %q, got %q", "tok", tok)
	}
}
