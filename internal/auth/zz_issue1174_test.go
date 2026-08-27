package auth

// Tests for issue #1174: an unknown provider name in the A2A OAuth2/OIDC
// config must produce an explicit error instead of silently dropping the
// whole auth block (which previously downgraded the server to public
// default-API-key auth with no warning).

import (
	"strings"
	"testing"
)

func TestResolveA2AAuthUnknownProviderReturnsError(t *testing.T) {
	// Typo'd provider name (the scenario from issue #1174)
	authorizeURL, tokenURL, clientID, scopes, err := ResolveA2AAuth("gihub", "my-client", "https://idp.example.com", "read")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error should mention 'unknown provider', got: %v", err)
	}
	if !strings.Contains(err.Error(), "gihub") {
		t.Errorf("error should echo the offending provider name, got: %v", err)
	}
	// All resolved values must be empty on the error path
	if authorizeURL != "" || tokenURL != "" || clientID != "" || scopes != "" {
		t.Errorf("expected empty outputs on error, got auth=%q token=%q clientID=%q scopes=%q",
			authorizeURL, tokenURL, clientID, scopes)
	}
}

func TestResolveA2AAuthUnknownProviderDropsNothingWhenFixed(t *testing.T) {
	// Same inputs with a correct provider name must resolve instead of error
	authorizeURL, _, clientID, _, err := ResolveA2AAuth("github", "my-client", "", "")
	if err != nil {
		t.Fatalf("valid provider should resolve, got error: %v", err)
	}
	if authorizeURL == "" || clientID != "my-client" {
		t.Errorf("user-supplied client_id must be preserved, got authURL=%q clientID=%q", authorizeURL, clientID)
	}
}

func TestResolveA2AAuthUnknownProviderListsKnownNames(t *testing.T) {
	_, _, _, _, err := ResolveA2AAuth("gogole", "", "", "")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	for _, known := range []string{"github", "google", "auth0", "azure"} {
		if !strings.Contains(err.Error(), known) {
			t.Errorf("error should list known provider %q, got: %v", known, err)
		}
	}
}

func TestResolveA2AAuthEmptyProviderStillMeansNoAuth(t *testing.T) {
	// provider == "" with no client_id/issuer_url keeps meaning "no auth
	// configured" (documented pre-existing behavior, not an error)
	authorizeURL, _, clientID, _, err := ResolveA2AAuth("", "", "", "")
	if err != nil {
		t.Fatalf("empty provider with empty fields must not error, got: %v", err)
	}
	if authorizeURL != "" || clientID != "" {
		t.Errorf("expected empty resolution, got authURL=%q clientID=%q", authorizeURL, clientID)
	}
}
