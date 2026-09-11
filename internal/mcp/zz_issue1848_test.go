package mcp

// #1848 case 1 regression: the #1659 fix claimed to normalize sse -> http
// but only existed in a comment - Start's case matched "sse" (built the
// httpClient, returned nil) while c.transport stayed "sse", so oauth wiring,
// the GET notification stream, and sendRequestUnlocked's switch all missed
// it: every Initialize fell to the stdio default and failed with
// "stdin closed". The normalization must happen at construction.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewClientFromConfigNormalizesSSEToHTTP(t *testing.T) {
	c := NewClientFromConfig(config.MCPServerConfig{
		Name: "sse-server",
		Type: "sse",
		URL:  "https://example.com/sse",
	})
	if c.transport != "http" {
		t.Fatalf("transport = %q, want \"http\" (sse normalized at construction; the send path fell to stdio/\"stdin closed\" before)", c.transport)
	}
	if c.httpClient == nil && c.oauthHandler == nil {
		// transport==http means Start wires the httpClient and oauth is
		// constructible; both were skipped when transport stayed "sse".
		t.Fatal("normalized transport must reach the http wiring paths")
	}
	if c.oauthHandler == nil {
		t.Fatal("oauth handler must be wired for a normalized sse->http server")
	}
}

func TestNewClientFromConfigKeepsOtherTransports(t *testing.T) {
	cases := []struct{ typ, want string }{
		{"stdio", "stdio"},
		{"http", "http"},
		{"ws", "ws"},
		{"", "stdio"},
		{"SSE", "http"}, // case-insensitive resolution then normalized
	}
	for _, c := range cases {
		cl := NewClientFromConfig(config.MCPServerConfig{Name: "x", Type: c.typ, Command: "bin"})
		if cl.transport != c.want {
			t.Errorf("Type %q: transport = %q, want %q", c.typ, cl.transport, c.want)
		}
	}
}
