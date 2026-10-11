package mcp

// #3791 companion tests.
// A: Close must clear the callback wiring so a SECOND OAuth attempt on the
//    same handler rebuilds the callback server (pre-fix: srv stayed
//    non-nil, prepareCallbackServer short-circuited, and the retry flow
//    blocked forever on a dead callback channel with a stale CSRF state).
// B: SupportsDeviceFlow / StartDeviceFlow must honor a server's RFC 8414
//    device_authorization_endpoint advertisement, not just the hardcoded
//    well-known map (pre-fix: metadata-only servers were judged
//    unsupported and headless users lost the device-flow fallback).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssue3791A_CloseClearsCallbackWiring(t *testing.T) {
	h := NewOAuthHandler("test", "https://example.com", nil)
	if err := h.prepareCallbackServer(); err != nil {
		t.Fatalf("prepare callback server: %v", err)
	}
	h.mu.Lock()
	firstSrv := h.callbackSrv
	firstCh := h.callbackCh
	firstState := h.state.state
	h.mu.Unlock()
	if firstSrv == nil || firstCh == nil || firstState == "" {
		t.Fatal("precondition: callback wiring must be populated")
	}

	h.Close()

	h.mu.Lock()
	srvGone := h.callbackSrv == nil
	chGone := h.callbackCh == nil
	stateGone := h.state.state == ""
	h.mu.Unlock()
	if !srvGone || !chGone {
		t.Fatalf("Close must clear callback wiring for rebuild, srv=%v ch=%v", !srvGone, !chGone)
	}
	if !stateGone {
		t.Fatal("Close must drop the spent CSRF state (per-flow nonce must not be reused)")
	}

	// The retry path: a second prepare must REBUILD instead of
	// short-circuiting on the stale pointer (pre-fix: returned nil error
	// while nothing listened on the port).
	if err := h.prepareCallbackServer(); err != nil {
		t.Fatalf("second prepareCallbackServer after Close must rebuild: %v", err)
	}
	h.mu.Lock()
	secondState := h.state.state
	h.mu.Unlock()
	if secondState == "" || secondState == firstState {
		t.Fatalf("second flow must mint a fresh CSRF state, got %q (first %q)", secondState, firstState)
	}
	h.Close()
}

func TestIssue3791B_DeviceFlowFromMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"device_code":"dc","user_code":"UC-1","verification_uri":"https://verify.example","expires_in":300,"interval":1}`))
	}))
	defer srv.Close()

	h := NewOAuthHandler("test", "https://example.com", nil)
	h.mu.Lock()
	h.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{
			Issuer:                      "https://issuer.example.com",
			DeviceAuthorizationEndpoint: srv.URL,
		},
		clientRegistration: &ClientRegistration{ClientID: "cid"},
	}
	h.mu.Unlock()
	if !h.SupportsDeviceFlow() {
		t.Fatal("metadata-advertised device_authorization_endpoint must enable device flow")
	}
	// End-to-end through StartDeviceFlow: the metadata endpoint must be
	// RESOLVED and used (pre-fix this failed with "no device code endpoint"
	// because only the hardcoded well-known map was consulted).
	resp, err := h.StartDeviceFlow(t.Context(), nil)
	if err != nil {
		t.Fatalf("StartDeviceFlow via metadata endpoint: %v", err)
	}
	if resp.DeviceCode != "dc" || resp.UserCode != "UC-1" {
		t.Fatalf("unexpected device flow response: %+v", resp)
	}

	// No advertisement and not well-known: still unsupported.
	h2 := NewOAuthHandler("test2", "https://other.example.com", nil)
	h2.mu.Lock()
	h2.state = &oauthState{
		authorizationServerMeta: &AuthorizationServerMetadata{Issuer: "https://plain.example.com"},
	}
	h2.mu.Unlock()
	if h2.SupportsDeviceFlow() {
		t.Fatal("server without device advertisement must stay unsupported")
	}
	_, err = h2.StartDeviceFlow(t.Context(), nil)
	if err == nil || !strings.Contains(err.Error(), "no client_id") {
		// h2 has no registration: the client_id gate must fire BEFORE
		// endpoint resolution - a plain no-endpoint error would mean the
		// gate order changed silently.
		t.Fatalf("h2 StartDeviceFlow err = %v, want no-client_id gate", err)
	}
}
