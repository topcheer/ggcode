package a2a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientCustomKeyHeaderStrippedOnCrossHostRedirect(t *testing.T) {
	var gotCustom, gotDefault string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustom = r.Header.Get("X-Custom-Key")
		gotDefault = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"id":"t1","status":{"state":"completed"}}}`))
	}))
	defer target.Close()

	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/agent.json" {
			_, _ = w.Write([]byte(`{"name":"src","url":"` + r.Host + `","securitySchemes":{"k":{"type":"apiKey","in":"header","name":"X-Custom-Key"}},"security":[{"k":[]}]}`))
			return
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer src.Close()

	c := NewClient(src.URL, "secret-key-1234")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := c.Discover(ctx); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if err := c.NegotiateAuth(); err != nil {
		t.Fatalf("negotiate auth: %v", err)
	}
	if c.AuthMethod() != "apiKey" {
		t.Fatalf("expected apiKey auth, got %q", c.AuthMethod())
	}
	if _, err := c.SendMessage(ctx, "", "hi"); err != nil {
		t.Logf("SendMessage returned err (non-essential for this regression): %v", err)
	}
	if gotCustom != "" {
		t.Fatalf("card-declared X-Custom-Key leaked to redirect target host: %q", gotCustom)
	}
	if gotDefault != "" {
		t.Fatalf("X-API-Key leaked to redirect target host: %q", gotDefault)
	}
}
