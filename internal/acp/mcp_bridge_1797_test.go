package acp

import (
	"context"
	"strings"
	"testing"
)

// #1797: ConnectServers must not swallow per-server failures silently -
// one unreachable server yields an aggregate error naming it, while other
// servers still connect (isolation preserved).
func Test1797ConnectServersSurfacesFailures(t *testing.T) {
	m := NewMCPManager(nil)
	servers := []MCPServer{
		{Name: "bad-sse", Type: "sse", URL: "http://127.0.0.1:1/sse"},
		{Name: "bad-stdio", Type: "stdio", Command: "/nonexistent/binary-for-1797"},
	}
	err := m.ConnectServers(context.Background(), servers)
	if err == nil {
		t.Fatal("aggregate failure was swallowed (silent mount)")
	}
	for _, want := range []string{"bad-sse", "bad-stdio"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregate error missing server %q: %v", want, err)
		}
	}
	// No servers at all -> no error.
	if err := m.ConnectServers(context.Background(), nil); err != nil {
		t.Fatalf("empty server list must not error: %v", err)
	}
}
