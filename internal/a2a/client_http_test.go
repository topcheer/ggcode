//go:build integration_local

package a2a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientRPCReturnsHTTPErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	_, err := client.ListTasks(context.Background(), "", 10)
	if err == nil {
		t.Fatal("expected list tasks error")
	}
	if !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected clear HTTP error, got %v", err)
	}
}

// TestDeclaredAPIKeyNameHonored pins #1458-A: a card declaring
// {"type":"apiKey","name":"X-Goog-Api-Key","location":"header"} must get
// its declared header - the old path always sent X-API-Key and the third
// party RPC failed 401 after 'successful' negotiation.
func TestDeclaredAPIKeyNameHonored(t *testing.T) {
	var gotHeader string
	var gotVal string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = "X-Goog-Api-Key"
		gotVal = r.Header.Get("X-Goog-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret-key")
	c.mu.Lock()
	c.authMethod = "apiKey"
	c.apiKeyName = "X-Goog-Api-Key"
	c.apiKeyIn = "header"
	c.mu.Unlock()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{}`))
	c.setAuth(req)
	if req.Header.Get("X-Goog-Api-Key") != "secret-key" {
		t.Fatalf("declared header not honored: %q", req.Header.Get("X-Goog-Api-Key"))
	}
	if req.Header.Get("X-API-Key") != "" {
		t.Fatal("legacy X-API-Key leaked alongside declared header")
	}
	_ = gotHeader
	_ = gotVal
}
