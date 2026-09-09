//go:build goolm

package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestWellKnownProbeHitReturnsOAuthImmediately (#1806): the probe-hit path
// had zero forward coverage - every existing test tolerates the well-known
// GET with a 404. When the server declares OAuth protection via
// well-known metadata, an anonymous client must get OAuthRequiredError
// immediately, with NO request POST (no 401 roundtrip).
func TestWellKnownProbeHitReturnsOAuthImmediately(t *testing.T) {
	var posts int32
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + "http://127.0.0.1:1" + `","authorization_endpoint":"http://127.0.0.1:1/authorize"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer authServer.Close()

	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"resource":"` + "http://example-rs" + `","authorization_servers":["` + authServer.URL + `"]}`))
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + "http://example-rs" + `","authorization_endpoint":"` + authServer.URL + `/authorize"}`))
		default:
			// The JSON-RPC POST must never happen on the probe-hit path.
			atomic.AddInt32(&posts, 1)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer resource.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "probe-hit",
		Type: "http",
		URL:  resource.URL,
	})
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err := client.Initialize(ctx)
	if err == nil {
		t.Fatal("expected OAuthRequiredError from the well-known probe hit, got nil")
	}
	var oauthErr *OAuthRequiredError
	if !errors.As(err, &oauthErr) {
		t.Fatalf("expected *OAuthRequiredError (possibly wrapped), got %T: %v", err, err)
	}
	if oauthErr.Handler == nil {
		t.Fatal("OAuthRequiredError must carry the handler so the flow can start")
	}
	if n := atomic.LoadInt32(&posts); n != 0 {
		t.Fatalf("probe hit must skip the POST entirely (no 401 roundtrip), saw %d", n)
	}
}
