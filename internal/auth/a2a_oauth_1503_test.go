package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIntrospectionFromDiscovery verifies #1503: validateOpaqueToken must
// read the introspection endpoint from the issuer's OIDC discovery document
// (introspection_endpoint, RFC 7662 §2.1) instead of guessing it from the
// issuer URL by string surgery.
func TestIntrospectionFromDiscovery(t *testing.T) {
	var introspectHits, discoveryHits int
	var lastToken string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
			discoveryHits++
			w.Header().Set("Content-Type", "application/json")
			// The endpoint deliberately does NOT follow any guessable
			// derivation from the issuer: a suffix-swap or append would
			// never produce this path.
			_, _ = w.Write([]byte(`{"introspection_endpoint":"` + srvURL(r) + `/oauth2/v1/inspect/tokens","jwks_uri":"` + srvURL(r) + `/jwks"}`))
		case strings.HasSuffix(r.URL.Path, "/inspect/tokens"):
			introspectHits++
			lastToken = r.FormValue("token")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":true,"sub":"user-123"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	v, err := NewTokenValidator("client-a", srv.URL)
	if err != nil {
		t.Fatalf("NewTokenValidator: %v", err)
	}
	claims, err := v.ValidateToken(context.Background(), "opaque-secret-token")
	if err != nil {
		t.Fatalf("ValidateToken (opaque): %v", err)
	}
	if got, _ := claims["sub"].(string); got != "user-123" {
		t.Fatalf("sub = %v, want user-123", claims["sub"])
	}
	if introspectHits != 1 {
		t.Fatalf("introspection endpoint hits = %d, want 1", introspectHits)
	}
	if lastToken != "opaque-secret-token" {
		t.Fatalf("introspection received token %q", lastToken)
	}
	if discoveryHits == 0 {
		t.Fatal("discovery document was never consulted")
	}

	// Second call must use the cached endpoint: discovery fetched once.
	if _, err := v.ValidateToken(context.Background(), "opaque-secret-token"); err != nil {
		t.Fatalf("ValidateToken second call: %v", err)
	}
	if discoveryHits != 1 {
		t.Fatalf("discovery hits after cache = %d, want 1 (endpoint should be cached)", discoveryHits)
	}
}

// TestIntrospectionFallbackGuessedURL verifies the fallback path: when the
// issuer has no discovery document (or it lacks introspection_endpoint),
// the legacy derivation is used (suffix-swap /token -> /introspect).
func TestIntrospectionFallbackGuessedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			// Discovery exists but has no introspection_endpoint -> fallback.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + srvURL(r) + `"}`))
		case "/v1/introspect":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":true,"sub":"fb-user"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Issuer ending in /token exercises the suffix-swap branch after the
	// discovery lookup returns nothing usable.
	v, err := NewTokenValidator("client-b", srv.URL+"/v1/token")
	if err != nil {
		t.Fatalf("NewTokenValidator: %v", err)
	}
	claims, err := v.ValidateToken(context.Background(), "opaque-fb")
	if err != nil {
		t.Fatalf("ValidateToken fallback: %v", err)
	}
	if got, _ := claims["sub"].(string); got != "fb-user" {
		t.Fatalf("sub = %v, want fb-user", claims["sub"])
	}
}

// srvURL reconstructs the server base URL from an incoming request (the
// httptest server URL is not visible inside the handler closure at config
// time in all Go versions used here).
func srvURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
