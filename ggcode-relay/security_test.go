package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelaySecurityClientIPPrefersTrustedProxyHeaders(t *testing.T) {
	cfg := relaySecurityConfig{TrustProxy: true}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "10.0.0.9:4567"
	req.Header.Set("X-Real-IP", "203.0.113.10")

	if got := cfg.clientIP(req); got != "203.0.113.10" {
		t.Fatalf("clientIP() = %q, want %q", got, "203.0.113.10")
	}
}

func TestHandleShareSessionRequiresProxyTLSWhenEnabled(t *testing.T) {
	t.Setenv(shareSecretEnv, "test-secret")

	h := newHubWithSecurity(nil, relaySecurityConfig{
		TrustProxy: true,
		RequireTLS: true,
	})

	req := httptest.NewRequest(http.MethodPost, "/share/session", nil)
	req.RemoteAddr = "10.0.0.9:4567"
	resp := httptest.NewRecorder()
	h.handleShareSession(resp, req)
	if resp.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusUpgradeRequired)
	}

	secureReq := httptest.NewRequest(http.MethodPost, "/share/session", nil)
	secureReq.RemoteAddr = "10.0.0.9:4567"
	secureReq.Header.Set("X-Forwarded-Proto", "https")
	secureReq.Header.Set("X-Real-IP", "198.51.100.7")
	secureResp := httptest.NewRecorder()
	h.handleShareSession(secureResp, secureReq)
	if secureResp.Code != http.StatusOK {
		t.Fatalf("secure status = %d, want %d", secureResp.Code, http.StatusOK)
	}
}

func TestStatsHandlerRequiresAdminByDefault(t *testing.T) {
	h := newHubWithSecurity(nil, relaySecurityConfig{})
	handler := newStatsHandler(h, "secret-token")

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	resp := httptest.NewRecorder()
	handler(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}

	authorizedReq := httptest.NewRequest(http.MethodGet, "/stats", nil)
	authorizedReq.Header.Set("Authorization", "Bearer secret-token")
	authorizedResp := httptest.NewRecorder()
	handler(authorizedResp, authorizedReq)
	if authorizedResp.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", authorizedResp.Code, http.StatusOK)
	}
}

func TestStatsHandlerDisablesPrivateStatsWithoutAdminToken(t *testing.T) {
	h := newHubWithSecurity(nil, relaySecurityConfig{})
	handler := newStatsHandler(h, "")

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	resp := httptest.NewRecorder()
	handler(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}
}
