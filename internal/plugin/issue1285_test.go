package plugin

// Regression tests for GitHub issue #1285: Close() touched the watcher
// cancel fields without m.mu (data race vs Connect's locked writes; the
// cancel could land on a stale value leaving an unstoppable watcher), and
// reconnect cycles could resurrect a closed plugin (ghost tools + reconnect
// storms against unloaded servers).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

func issue1285MockServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0.0"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"fetch","description":"Fetch","inputSchema":{"type":"object"}}]}}`))
		case "prompts/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":3,"result":{"result":{"prompts":[]}}}`))
		case "resources/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":4,"result":{"resources":[]}}}`))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":99,"result":{}}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestIssue1285_CloseBlocksResurrection: after Close(), attemptReconnect
// must refuse to reconnect even though the server is perfectly healthy -
// the entry guard checks the closed flag. Without the fix this returns true
// and re-registers tools for an unloaded plugin.
func TestIssue1285_CloseBlocksResurrection(t *testing.T) {
	server := issue1285MockServer(t)
	p := NewMCPPlugin(config.MCPServerConfig{Name: "closed-http", Type: "http", URL: server.URL})
	if _, err := p.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.attemptReconnect(context.Background()) {
		t.Fatal("#1285: attemptReconnect resurrected a closed plugin")
	}
	p.mu.RLock()
	ghost := p.adapter != nil || p.connected
	p.mu.RUnlock()
	if ghost {
		t.Fatal("#1285: closed plugin has a live adapter/connected status after reconnect attempt")
	}
}

// TestIssue1285_ConnectAfterCloseFails: a Connect that races Close() must
// not install its client - the tail guard drops it.
func TestIssue1285_ConnectAfterCloseFails(t *testing.T) {
	server := issue1285MockServer(t)
	p := NewMCPPlugin(config.MCPServerConfig{Name: "closed-http-2", Type: "http", URL: server.URL})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Connect(context.Background()); err == nil {
		t.Fatal("#1285: Connect on a closed plugin must fail, not adopt a new client")
	}
}

// TestIssue1285_DisconnectThenReconnect: the manager-level Disconnect ->
// Reconnect cycle must keep working. The first #1285 fix left `closed` set
// forever, so the UI's Reconnect was permanently rejected ("closed during
// connect"); connectOne now clears the flag as the manager's explicit
// revive signal.
func TestIssue1285_DisconnectThenReconnect(t *testing.T) {
	server := issue1285MockServer(t)
	manager := NewMCPManager([]config.MCPServerConfig{{
		Name: "cycle-http", Type: "http", URL: server.URL,
	}}, tool.NewRegistry())
	manager.ConnectAll(context.Background())
	if infos := manager.Snapshot(); len(infos) != 1 || infos[0].Status != MCPStatusConnected {
		t.Fatalf("expected connected, got %+v", infos)
	}
	if !manager.Disconnect("cycle-http") {
		t.Fatal("Disconnect must find the server")
	}
	// Disconnect is async; wait for the teardown to land (status leaves
	// connected) so we test the steady post-Close state, not a race.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if infos := manager.Snapshot(); len(infos) == 1 && infos[0].Status != MCPStatusConnected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if infos := manager.Snapshot(); len(infos) != 1 || infos[0].Status == MCPStatusConnected {
		t.Fatalf("expected disconnected status, got %+v", infos)
	}
	// The user changes their mind and reconnects from the UI.
	if !manager.Reconnect("cycle-http") {
		t.Fatal("Reconnect must find the server")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if infos := manager.Snapshot(); len(infos) == 1 && infos[0].Status == MCPStatusConnected {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("#1285 regression: Disconnect -> Reconnect did not reach connected, state %+v", manager.Snapshot())
}
