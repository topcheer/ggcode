package main

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2781OIDCPlaceholderCheckParity pins #2781: the a2a OIDC config
// block must fail fast on unfilled preset placeholders (AUTH0_TENANT /
// AZURE_TENANT) exactly like the parallel OAuth2 block does (#1503). The
// pre-fix OIDC block only checked for an empty issuer: a preset without a
// filled tenant resolved to a placeholder URL, the server started clean,
// and every JWKS fetch hit NXDOMAIN - silent per-request 401s. A source
// parity probe (same approach as #2774): the placeholder check must appear
// in BOTH config blocks.
func TestIssue2781OIDCPlaceholderCheckParity(t *testing.T) {
	data, err := os.ReadFile("root.go")
	if err != nil {
		t.Fatalf("read root.go: %v", err)
	}
	src := string(data)

	// Both the oauth2 and the oidc block must reject placeholder issuers.
	n := strings.Count(src, "AUTH0_TENANT") + strings.Count(src, "AZURE_TENANT")
	// Two checks x two placeholder names in the Contains disjunction, plus
	// the #1503 explanatory comment above the oauth2 block (names in prose).
	if n < 4 {
		t.Fatalf("root.go mentions preset placeholders %d times - want >= 4 (oauth2 + oidc blocks both carrying the #1503 fail-fast; pre-fix only the oauth2 block had it, #2781)", n)
	}

	// The oidc block specifically: find its issuer empty-check, then the
	// placeholder check must follow before NewTokenValidator.
	oidcIdx := strings.Index(src, `fmt.Errorf("a2a oidc: no issuer available`)
	if oidcIdx < 0 {
		t.Fatal("oidc issuer empty-check not found in root.go")
	}
	window := src[oidcIdx : oidcIdx+900]
	if !strings.Contains(window, "AUTH0_TENANT") {
		t.Fatal("oidc block lacks the preset-placeholder fail-fast check after its issuer empty-check (#2781) - placeholder issuers start the server and every request 401s silently")
	}
}
