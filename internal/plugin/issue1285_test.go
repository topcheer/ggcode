package plugin

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

func TestIssue1285_DisconnectThenReconnect(t *testing.T) {
	server := issue1285MockServer(t)
	manager := NewMCPManager([]config.MCPServerConfig{{
		Name: "cycle-http", Type: "http", URL: server.URL,
	}}, tool.NewRegistry(), "")
	manager.ConnectAll(context.Background())
	if infos := manager.Snapshot(); len(infos) != 1 || infos[0].Status != MCPStatusConnected {
		t.Fatalf("expected connected, got %+v", infos)
	}
	if !manager.Disconnect("cycle-http") {
		t.Fatal("Disconnect must find the server")
	}

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

	if !manager.Reconnect("cycle-http") {
		t.Fatal("Reconnect must find the server")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if infos := manager.Snapshot(); len(infos) == 1 && infos[0].Status == MCPStatusConnected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("#1285 regression: Disconnect -> Reconnect did not reach connected, state %+v", manager.Snapshot())
}
