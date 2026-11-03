package main

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2781OIDCPlaceholderCheckParity(t *testing.T) {
	data, err := os.ReadFile("root.go")
	if err != nil {
		t.Fatalf("read root.go: %v", err)
	}
	src := string(data)

	n := strings.Count(src, "AUTH0_TENANT") + strings.Count(src, "AZURE_TENANT")

	if n < 4 {
		t.Fatalf("root.go mentions preset placeholders %d times - want >= 4 (oauth2 + oidc blocks both carrying the #1503 fail-fast; pre-fix only the oauth2 block had it, #2781)", n)
	}

	oidcIdx := strings.Index(src, `fmt.Errorf("a2a oidc: no issuer available`)
	if oidcIdx < 0 {
		t.Fatal("oidc issuer empty-check not found in root.go")
	}
	window := src[oidcIdx : oidcIdx+900]
	if !strings.Contains(window, "AUTH0_TENANT") {
		t.Fatal("oidc block lacks the preset-placeholder fail-fast check after its issuer empty-check (#2781) - placeholder issuers start the server and every request 401s silently")
	}
}
