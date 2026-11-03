package acp

import "testing"

func Test1786SSENormalization(t *testing.T) {
	for _, typ := range []string{"sse", "SSE", "streamable-http", "streamable_http", "https"} {
		cfg := acpMCPServerToConfig(MCPServer{Name: "x", Type: typ, URL: "http://h/sse"})
		if cfg.Type != "http" {
			t.Fatalf("type %q must normalize to http, got %q", typ, cfg.Type)
		}
	}
	if c := acpMCPServerToConfig(MCPServer{Name: "x", Type: "stdio", Command: "run"}); c.Type != "stdio" {
		t.Fatalf("stdio must stay stdio, got %q", c.Type)
	}
	if c := acpMCPServerToConfig(MCPServer{Name: "x", Type: "http", URL: "u"}); c.Type != "http" {
		t.Fatalf("http must stay http, got %q", c.Type)
	}
	if c := acpMCPServerToConfig(MCPServer{Name: "x", Type: "ws", URL: "u"}); c.Type != "ws" {
		t.Fatalf("ws must stay ws, got %q", c.Type)
	}
}
