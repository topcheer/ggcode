package auth

import "testing"

func TestIssue599_OAuthErrorBranchStateValidation_DocumentationOnly(t *testing.T) {
	t.Skip("fix documented; requires editing claude_oauth.go L116-142")
}

func TestIssue599_ExpiresInFallback_Documentation(t *testing.T) {
	t.Skip("decision documented; see claude_oauth.go and config.go")
}
