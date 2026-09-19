package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// TestIssue2572_ProviderIsLocalBaseURLIPv6 pins #2572: the provider-side
// copy of isLocalBaseURL must agree with the #906-fixed tui copy - every
// IPv6 loopback form is local. The old IndexByte(':') host extraction
// truncated '[::1]:11434' to '[' and made both ::1 branches dead code.
func TestIssue2572_ProviderIsLocalBaseURLIPv6(t *testing.T) {
	local := []string{
		"http://[::1]:11434", // bracketed + port (Ollama default listen)
		"http://[::1]",
		"[::1]",
		"http://::1:11434", // bare IPv6 + port (SplitHostPort rejects; last-colon strip)
		"http://localhost:11434",
		"http://127.0.0.1:11434",
	}
	for _, u := range local {
		if !isLocalBaseURL(u) {
			t.Errorf("want local, got remote: %q", u)
		}
	}
	for _, u := range []string{"http://api.ollama.com", "http://[2001:db8::1]:8080", "https://example.com"} {
		if isLocalBaseURL(u) {
			t.Errorf("want remote, got local: %q", u)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countingBody struct {
	io.ReadCloser
	closed chan int
}

func (b *countingBody) Close() error {
	select {
	case b.closed <- 1:
	default:
	}
	return b.ReadCloser.Close()
}

// TestIssue2574_PaginationClosesLastBody pins #2574: the gemini discovery
// pagination loop used a registration-time defer bound to page 1's body -
// every multi-page exit leaked the latest page. The closure defer now
// closes whatever body is current at return.
func TestIssue2574_PaginationClosesLastBody(t *testing.T) {
	const pageSize = 2
	page := 0
	closed := make(chan int, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := page
		page++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if cur == 0 {
			_, _ = w.Write([]byte(`{"models":[{"name":"m1"},{"name":"m2"}],"nextPageToken":"tok1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"m3"}]}`))
	}))
	defer srv.Close()

	resolved := &config.ResolvedEndpoint{Protocol: "gemini", APIKey: "k", BaseURL: srv.URL}
	client := &http.Client{Timeout: 5 * time.Second, Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err == nil {
			resp.Body = &countingBody{ReadCloser: resp.Body, closed: closed}
		}
		return resp, err
	})}
	_, _ = discoverModelsFromURL(context.Background(), client, srv.URL, resolved)
	// Both page bodies must be closed (the old defer closed only page 1).
	deadline := time.After(2 * time.Second)
	got := 0
	for got < 2 {
		select {
		case <-closed:
			got++
		case <-deadline:
			t.Fatalf("closed bodies = %d, want 2", got)
		}
	}
}
