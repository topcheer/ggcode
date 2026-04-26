package auth

import (
	"testing"
)

// ---------------------------------------------------------------------------
// A2A Auth type tests
// ---------------------------------------------------------------------------

func TestA2AOAuth2ConfigCreation(t *testing.T) {
	cfg := A2AOAuth2Config{
		ClientID:     "test-client",
		AuthorizeURL: "https://example.com/authorize",
		TokenURL:     "https://example.com/token",
		Scopes:       []string{"openid", "profile"},
	}
	if cfg.ClientID != "test-client" {
		t.Errorf("expected test-client, got %s", cfg.ClientID)
	}
	if len(cfg.Scopes) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(cfg.Scopes))
	}
}

func TestPKCETokenSerialization(t *testing.T) {
	token := &PKCEToken{
		AccessToken:  "abc123",
		RefreshToken: "refresh456",
		TokenType:    "Bearer",
	}
	if token.AccessToken != "abc123" {
		t.Errorf("expected abc123, got %s", token.AccessToken)
	}
	if token.TokenType != "Bearer" {
		t.Errorf("expected Bearer, got %s", token.TokenType)
	}
}

func TestMTLSConfigBuildMissingFiles(t *testing.T) {
	cfg := &MTLSConfig{
		CertFile: "/nonexistent/server.crt",
		KeyFile:  "/nonexistent/server.key",
		CAFile:   "/nonexistent/ca.crt",
	}
	_, err := cfg.BuildTLSConfig()
	if err == nil {
		t.Error("expected error for missing cert files")
	}
}

func TestNewTokenValidator(t *testing.T) {
	v, err := NewTokenValidator("test-client", "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v == nil {
		t.Fatal("expected non-nil validator")
	}
}

func TestStrVal(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected string
	}{
		{"hello", "hello"},
		{nil, ""},
		{123, "123"},
		{"", ""},
	}
	for _, tt := range tests {
		got := strVal(tt.input)
		if got != tt.expected {
			t.Errorf("strVal(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
