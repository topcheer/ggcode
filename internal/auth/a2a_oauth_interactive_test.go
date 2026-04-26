//go:build manual

package auth

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Interactive OAuth2 + PKCE flow test (requires human interaction)
// ---------------------------------------------------------------------------

// TestInteractive_GitHubPKCEFlow performs a real GitHub OAuth2 + PKCE flow.
// This test opens a browser for user authorization and waits for the callback.
//
// Prerequisites:
//   - Run with: go test -tags=integration -run TestInteractive_GitHubPKCEFlow -v -timeout 5m
//   - A browser is available on the machine
//   - You have a GitHub account to authorize
//
// This test is NOT automated — it requires human interaction (browser login).
func TestInteractive_GitHubPKCEFlow(t *testing.T) {
	preset := ResolveProviderPreset("github")
	if preset == nil {
		t.Fatal("GitHub preset not found")
	}

	cfg := A2AOAuth2Config{
		ClientID:     preset.DefaultClientID,
		AuthorizeURL: preset.AuthorizeURL,
		TokenURL:     preset.TokenURL,
		Scopes:       preset.DefaultScopes,
	}

	if cfg.ClientID == "" {
		t.Skip("No default client_id for GitHub preset")
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════\n")
	fmt.Fprintf(os.Stderr, "  GitHub OAuth2 + PKCE Interactive Test\n")
	fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  This test will:\n")
	fmt.Fprintf(os.Stderr, "  1. Open your browser to GitHub login\n")
	fmt.Fprintf(os.Stderr, "  2. Ask you to authorize the app\n")
	fmt.Fprintf(os.Stderr, "  3. Receive the callback with auth code\n")
	fmt.Fprintf(os.Stderr, "  4. Exchange code + PKCE verifier for token\n")
	fmt.Fprintf(os.Stderr, "  5. Verify the access token\n")
	fmt.Fprintf(os.Stderr, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	token, err := StartPKCEFlow(ctx, cfg)
	if err != nil {
		t.Fatalf("PKCE flow failed: %v", err)
	}

	// Verify token
	if token.AccessToken == "" {
		t.Fatal("expected non-empty access token")
	}
	if token.TokenType == "" {
		t.Error("expected non-empty token type")
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════\n")
	fmt.Fprintf(os.Stderr, "  ✅ OAuth2 + PKCE Flow Successful!\n")
	fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Token type:    %s\n", token.TokenType)
	fmt.Fprintf(os.Stderr, "  Scope:         %s\n", token.Scope)
	fmt.Fprintf(os.Stderr, "  Access token:  %s...%s\n", token.AccessToken[:8], token.AccessToken[len(token.AccessToken)-4:])
	if !token.Expiry.IsZero() {
		fmt.Fprintf(os.Stderr, "  Expires:       %s\n", token.Expiry.Format(time.RFC3339))
	}
	if token.RefreshToken != "" {
		fmt.Fprintf(os.Stderr, "  Refresh token: %s...%s\n", token.RefreshToken[:8], token.RefreshToken[len(token.RefreshToken)-4:])
	}
	fmt.Fprintf(os.Stderr, "\n")

	t.Logf("GitHub OAuth2 token obtained: type=%s scope=%s", token.TokenType, token.Scope)
}

// TestInteractive_GitHubDeviceFlow performs a real GitHub Device Authorization flow.
// This test displays a code and URL for the user to visit.
//
// Prerequisites:
//   - Run with: go test -tags=integration -run TestInteractive_GitHubDeviceFlow -v -timeout 5m
//   - You have a GitHub account to authorize
func TestInteractive_GitHubDeviceFlow(t *testing.T) {
	preset := ResolveProviderPreset("github")
	if preset == nil {
		t.Fatal("GitHub preset not found")
	}

	cfg := A2AOAuth2Config{
		ClientID:     preset.DefaultClientID,
		AuthorizeURL: preset.DeviceAuthURL, // device authorization endpoint
		TokenURL:     preset.TokenURL,
		Scopes:       preset.DefaultScopes,
	}

	if cfg.ClientID == "" {
		t.Skip("No default client_id for GitHub preset")
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════\n")
	fmt.Fprintf(os.Stderr, "  GitHub Device Authorization Flow Test\n")
	fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════\n")
	fmt.Fprintf(os.Stderr, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	token, err := StartDeviceFlow(ctx, cfg)
	if err != nil {
		t.Fatalf("Device flow failed: %v", err)
	}

	if token.AccessToken == "" {
		t.Fatal("expected non-empty access token")
	}

	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════\n")
	fmt.Fprintf(os.Stderr, "  ✅ Device Flow Successful!\n")
	fmt.Fprintf(os.Stderr, "═══════════════════════════════════════════════\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  Token type:    %s\n", token.TokenType)
	fmt.Fprintf(os.Stderr, "  Scope:         %s\n", token.Scope)
	fmt.Fprintf(os.Stderr, "  Access token:  %s...%s\n", token.AccessToken[:8], token.AccessToken[len(token.AccessToken)-4:])
	fmt.Fprintf(os.Stderr, "\n")

	t.Logf("GitHub device flow token obtained: type=%s scope=%s", token.TokenType, token.Scope)
}
