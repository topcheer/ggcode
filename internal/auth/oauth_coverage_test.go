package auth

import (
	"context"
	"testing"
)

func TestBuildTLSConfig_MissingCert(t *testing.T) {
	cfg := &MTLSConfig{
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}
	_, err := cfg.BuildTLSConfig()
	if err == nil {
		t.Error("expected error for missing cert files")
	}
}

func TestExchangeCodeForToken_BadURL(t *testing.T) {
	cfg := A2AOAuth2Config{
		TokenURL: "http://127.0.0.1:1/token",
		ClientID: "test",
	}
	_, err := exchangeCodeForToken(context.Background(), cfg, "code", "redirect", "verifier")
	if err == nil {
		t.Error("expected error for unreachable URL")
	}
}

func TestPollDeviceToken_BadURL(t *testing.T) {
	cfg := A2AOAuth2Config{
		TokenURL: "http://127.0.0.1:1/token",
		ClientID: "test",
	}
	_, err := pollDeviceToken(context.Background(), cfg, "device-code")
	if err == nil {
		t.Error("expected error for unreachable URL")
	}
}

func TestValidateJWT_EmptyToken(t *testing.T) {
	tv, err := NewTokenValidator("test-client", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewTokenValidator error: %v", err)
	}
	_, err = tv.validateJWT(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty token")
	}
}

func TestValidateJWT_InvalidToken(t *testing.T) {
	tv, err := NewTokenValidator("test-client", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewTokenValidator error: %v", err)
	}
	_, err = tv.validateJWT(context.Background(), "not.a.valid-token")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestRefreshJWKS_BadURL(t *testing.T) {
	tv, err := NewTokenValidator("test-client", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewTokenValidator error: %v", err)
	}
	err = tv.refreshJWKS(context.Background())
	if err == nil {
		t.Error("expected error for unreachable JWKS URL")
	}
}

func TestGetPublicKey_BadURL(t *testing.T) {
	tv, err := NewTokenValidator("test-client", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewTokenValidator error: %v", err)
	}
	_, err = tv.getPublicKey(context.Background(), "some-kid")
	if err == nil {
		t.Error("expected error for unreachable JWKS URL")
	}
}

func TestNewTokenValidator_EmptyIssuer(t *testing.T) {
	tv, err := NewTokenValidator("test-client", "")
	if err != nil {
		t.Logf("NewTokenValidator('') error (expected): %v", err)
	}
	_ = tv // may be nil
}
