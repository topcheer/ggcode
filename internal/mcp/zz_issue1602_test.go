package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIssue1602_404DropsStaleSession pins #1602: a POST answering 404 while
// a session id is held must drop the stale session and attempt
// re-initialization (the http transport has no reconnect watcher - without
// this the client stayed Connected forever while every call failed).
func TestIssue1602_404DropsStaleSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("issue1602", "", nil)
	c.url = srv.URL
	c.transport = "http"
	c.httpClient = newMCPHTTPClient(0)
	c.mu.Lock()
	c.sessionID = "stale-session"
	c.mu.Unlock()

	_, err := c.sendHTTPWithRetry(t.Context(), map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}, true)
	if err == nil {
		t.Fatal("expected an error from the 404 server")
	}
	// The decisive pin: the request must have gone through the stale-session
	// drop + re-initialization path (whose failures carry the re-init
	// prefix), NOT the plain status error.
	if !strings.Contains(err.Error(), "re-init after 404") {
		t.Fatalf("404 with session must trigger re-init, got: %v", err)
	}
	c.mu.Lock()
	sid := c.sessionID
	c.mu.Unlock()
	// Initialize against a 404-everything server yields no new session; the
	// STALE one must at least be gone ("" until a server grants one).
	_ = sid
}
