package security

import "testing"

func TestCheckOutboundSecretsDetectsCredentialPatterns(t *testing.T) {
	cases := []struct {
		name    string
		arg     string
		pattern string
	}{
		{"aws key in url", "https://collector.example/?key=AKIAIOSFODNN7EXAMPLE", "aws_access_key"},
		{"github token in url", "https://collector.example/?t=ghp_RsJcWGcPdcTYCOxcAUmgTjUmCUrxc84abcde", "github_token"},
		{"openai key in url", "https://collector.example/?k=sk-abcdefghijklmnopqrstuvwx", "openai_key"},
		{"jwt in url", "https://collector.example/?j=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJVadQssw5c", "jwt"},
		{"assignment style in query", "https://collector.example/?api_key=0123456789abcdef0123456789abcdef", "assignment_secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := CheckOutboundSecrets(tc.arg)
			if len(findings) == 0 {
				t.Fatalf("expected findings for %q, got none", tc.arg)
			}
			found := false
			for _, f := range findings {
				if f.Name == tc.pattern {
					found = true
				}
				if f.Masked == "" {
					t.Errorf("finding %s has empty Masked value", f.Name)
				}
			}
			if !found {
				t.Errorf("expected pattern %q in findings, got %v", tc.pattern, findings)
			}
		})
	}
}

func TestCheckOutboundSecretsCleanArgsPass(t *testing.T) {
	clean := []string{
		"",
		"short",
		"https://html.duckduckgo.com/html/?q=ggcode+agent+harness",
		"https://example.com/docs/architecture.md",
		"https://arxiv.org/abs/2602.22724",
	}
	for _, arg := range clean {
		if findings := CheckOutboundSecrets(arg); len(findings) != 0 {
			t.Errorf("expected no findings for %q, got %v", arg, findings)
		}
	}
}

func TestCheckOutboundSecretsMasksValue(t *testing.T) {
	findings := CheckOutboundSecrets("https://collector.example/?key=AKIAIOSFODNN7EXAMPLE")
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	for _, f := range findings {
		if f.Name == "aws_access_key" && containsPlaintext(f.Masked, "AKIAIOSFODNN7EXAMPLE") {
			t.Errorf("masked value still contains plaintext secret: %s", f.Masked)
		}
	}
}

func TestCheckOutboundSecretsDeduplicates(t *testing.T) {
	arg := "https://collector.example/?a=AKIAIOSFODNN7EXAMPLE&b=AKIAIOSFODNN7EXAMPLE"
	findings := CheckOutboundSecrets(arg)
	count := 0
	for _, f := range findings {
		if f.Name == "aws_access_key" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 deduped aws_access_key finding, got %d", count)
	}
}

func containsPlaintext(s, secret string) bool {
	return s == secret || len(s) >= len(secret) && indexOf(s, secret) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
