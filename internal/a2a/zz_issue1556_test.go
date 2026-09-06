package a2a

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

// Regression for #1556: the DEFAULT (apiKey) client must carry the #1458
// fixes that previously landed only on the WithMTLS branch - no hard
// Client.Timeout over SSE streams, header-only timeout instead, and the
// cross-host key-strip redirect hook.
func TestNewClientDefaultHasStreamSafeTransport(t *testing.T) {
	c := NewClient("https://example.a2a", "secret-key")
	if c.httpClient.Timeout != 0 {
		t.Fatalf("default client must not carry a hard Client.Timeout (kills SSE), got %v", c.httpClient.Timeout)
	}
	if c.httpClient.CheckRedirect == nil {
		t.Fatal("default client (the one actually carrying X-API-Key) must strip the key on cross-host redirects")
	}
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c.httpClient.Transport)
	}
	if tr.ResponseHeaderTimeout <= 0 || tr.ResponseHeaderTimeout > 15*time.Minute {
		t.Fatalf("ResponseHeaderTimeout = %v, want (0, 15min]", tr.ResponseHeaderTimeout)
	}
	// mTLS branch keeps its protections too.
	m := NewClient("https://example.a2a", "", WithMTLS(&tls.Config{}))
	if m.httpClient.Timeout != 0 || m.httpClient.CheckRedirect == nil {
		t.Fatal("mTLS client must keep the #1458 protections")
	}
}
