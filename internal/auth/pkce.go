package auth

// GenerateCodeVerifier creates a cryptographically random code verifier for PKCE.
func GenerateCodeVerifier() (string, error) { return generateCodeVerifier() }

// GenerateCodeChallenge creates a S256 code challenge from a code verifier.
func GenerateCodeChallenge(verifier string) string { return generateCodeChallenge(verifier) }

// GenerateState creates a random state parameter for OAuth.
func GenerateState() (string, error) { return generateState() }
