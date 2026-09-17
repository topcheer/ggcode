package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestMCPManagerConnectAllTimesOutHungServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			// Standalone notification-stream probe (client.go
			// startHTTPNotificationStream); this mock does not offer it.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Method == "initialize" {
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			writeEcho(w, req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0.0"}}`)
			return
		}
		if req.Method == mcp.MethodSubscriptionsListen {
			// MCP 2026-07-28 probe: legacy mock — spec downgrade error.
			w.Header().Set("Content-Type", "application/json")
			echo, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
			})
			_, _ = w.Write(echo)
			return
		}
		t.Fatalf("unexpected method %s", req.Method)
	}))
	defer server.Close()

	manager := NewMCPManager([]config.MCPServerConfig{{
		Name: "hung-http",
		Type: "http",
		URL:  server.URL,
	}}, tool.NewRegistry(), "")
	manager.timeout = 20 * time.Millisecond

	manager.ConnectAll(t.Context())
	infos := manager.Snapshot()
	if len(infos) != 1 {
		t.Fatalf("expected 1 MCP info, got %d", len(infos))
	}
	if infos[0].Status != MCPStatusFailed {
		t.Fatalf("expected failed status, got %s", infos[0].Status)
	}
	if infos[0].Error != "connection timed out" {
		t.Fatalf("expected timeout error, got %q", infos[0].Error)
	}
}

func TestNewMCPManagerUsesLongerTimeoutForStdio(t *testing.T) {
	manager := NewMCPManager(nil, tool.NewRegistry(), "")
	if got := manager.connectTimeoutFor(NewMCPPlugin(config.MCPServerConfig{Type: "stdio"})); got != 2*time.Minute {
		t.Fatalf("expected stdio timeout 2m, got %v", got)
	}
	if got := manager.connectTimeoutFor(NewMCPPlugin(config.MCPServerConfig{Type: "http"})); got != 8*time.Second {
		t.Fatalf("expected http timeout 8s, got %v", got)
	}
}

func TestMCPManagerConnectAllTimesOutHungStdioServer(t *testing.T) {
	command := "sleep"
	args := []string{"60"}
	if runtime.GOOS == "windows" {
		command = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 60"}
	}
	manager := NewMCPManager([]config.MCPServerConfig{{
		Name:    "hung-stdio",
		Type:    "stdio",
		Command: command,
		Args:    args,
	}}, tool.NewRegistry(), "")
	manager.stdioTimeout = 20 * time.Millisecond

	start := time.Now()
	manager.ConnectAll(context.Background())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected stdio timeout quickly, took %v", elapsed)
	}
	infos := manager.Snapshot()
	if len(infos) != 1 || infos[0].Status != MCPStatusFailed {
		t.Fatalf("expected failed stdio status, got %+v", infos)
	}
	if infos[0].Error != "connection timed out" {
		t.Fatalf("expected timeout error, got %q", infos[0].Error)
	}
}

func TestMCPPluginInfoIncludesPromptAndResourceNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			// Standalone notification-stream probe (client.go
			// startHTTPNotificationStream); this mock does not offer it.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeEcho(w, req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0.0"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			writeEcho(w, req.ID, `{"tools":[{"name":"fetch","description":"Fetch","inputSchema":{"type":"object"}}]}`)
		case "prompts/list":
			writeEcho(w, req.ID, `{"prompts":[{"name":"summarize"},{"name":"translate"}]}`)
		case "resources/list":
			writeEcho(w, req.ID, `{"resources":[{"name":"docs"},{"uri":"file:///tmp/readme.md"}]}`)
		case mcp.MethodSubscriptionsListen:
			// MCP 2026-07-28 probe: this mock is legacy — downgrade the
			// client with the spec method-not-found error.
			echo, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
			})
			_, _ = w.Write(echo)
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	p := NewMCPPlugin(config.MCPServerConfig{Name: "rich-http", Type: "http", URL: server.URL})
	if _, err := p.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	info := p.Info()
	if len(info.ToolNames) != 1 || info.ToolNames[0] != "mcp__rich-http__fetch" {
		t.Fatalf("unexpected tools: %v", info.ToolNames)
	}
	if len(info.PromptNames) != 2 || info.PromptNames[0] != "summarize" || info.PromptNames[1] != "translate" {
		t.Fatalf("unexpected prompts: %v", info.PromptNames)
	}
	if len(info.ResourceNames) != 2 || info.ResourceNames[0] != "docs" || info.ResourceNames[1] != "file:///tmp/readme.md" {
		t.Fatalf("unexpected resources: %v", info.ResourceNames)
	}
}

func TestMCPPluginInfoDoesNotBlockWhileConnectIsInFlight(t *testing.T) {
	initialized := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			// Standalone notification-stream probe (client.go
			// startHTTPNotificationStream); this mock does not offer it.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			close(initialized)
			<-release
			writeEcho(w, req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0.0"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			writeEcho(w, req.ID, `{"tools":[]}`)
		case "prompts/list":
			writeEcho(w, req.ID, `{"prompts":[]}`)
		case "resources/list":
			writeEcho(w, req.ID, `{"resources":[]}`)
		case mcp.MethodSubscriptionsListen:
			// MCP 2026-07-28 probe: this mock is legacy — downgrade the
			// client with the spec method-not-found error.
			echo, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
			})
			_, _ = w.Write(echo)
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	p := NewMCPPlugin(config.MCPServerConfig{Name: "slow-http", Type: "http", URL: server.URL})
	connectDone := make(chan error, 1)
	go func() {
		_, err := p.Connect(context.Background())
		connectDone <- err
	}()

	select {
	case <-initialized:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initialize request")
	}

	infoDone := make(chan MCPServerInfo, 1)
	go func() {
		infoDone <- p.Info()
	}()

	select {
	case info := <-infoDone:
		if info.Name != "slow-http" {
			t.Fatalf("unexpected info: %+v", info)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected Info to return while connect is still in flight")
	}

	close(release)
	if err := <-connectDone; err != nil {
		t.Fatal(err)
	}
}

func TestMCPManagerPromptAndResourceAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			// Standalone notification-stream probe (client.go
			// startHTTPNotificationStream); this mock does not offer it.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeEcho(w, req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0.0"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			writeEcho(w, req.ID, `{"tools":[]}`)
		case "prompts/list":
			writeEcho(w, req.ID, `{"prompts":[{"name":"summarize"}]}`)
		case "resources/list":
			writeEcho(w, req.ID, `{"resources":[{"uri":"docs"}]}`)
		case "prompts/get":
			writeEcho(w, req.ID, `{"description":"Prompt","messages":[{"role":"user","content":{"type":"text","text":"hello prompt"}}]}`)
		case "resources/read":
			writeEcho(w, req.ID, `{"contents":[{"uri":"docs","mimeType":"text/plain","text":"hello resource"}]}`)
		case mcp.MethodSubscriptionsListen:
			// MCP 2026-07-28 probe: this mock is legacy — downgrade the
			// client with the spec method-not-found error.
			echo, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
			})
			_, _ = w.Write(echo)
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	manager := NewMCPManager([]config.MCPServerConfig{{
		Name: "rich-http",
		Type: "http",
		URL:  server.URL,
	}}, tool.NewRegistry(), "")
	manager.ConnectAll(context.Background())

	prompt, err := manager.GetPrompt(context.Background(), "rich-http", "summarize", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt.Messages) != 1 || prompt.Messages[0].Text != "hello prompt" {
		t.Fatalf("unexpected prompt result: %+v", prompt)
	}
	resource, err := manager.ReadResource(context.Background(), "rich-http", "docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0].Text != "hello resource" {
		t.Fatalf("unexpected resource result: %+v", resource)
	}
}

func TestMCPManagerInstallAddsServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			// Standalone notification-stream probe (client.go
			// startHTTPNotificationStream); this mock does not offer it.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeEcho(w, req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0.0"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			writeEcho(w, req.ID, `{"tools":[{"name":"fetch","description":"Fetch","inputSchema":{"type":"object"}}]}`)
		case "prompts/list":
			writeEcho(w, req.ID, `{"prompts":[]}`)
		case "resources/list":
			writeEcho(w, req.ID, `{"resources":[]}`)
		case mcp.MethodSubscriptionsListen:
			// MCP 2026-07-28 probe: this mock is legacy — downgrade the
			// client with the spec method-not-found error.
			echo, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
			})
			_, _ = w.Write(echo)
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	manager := NewMCPManager(nil, tool.NewRegistry(), "")
	if err := manager.Install(context.Background(), config.MCPServerConfig{
		Name: "fetcher",
		Type: "http",
		URL:  server.URL,
	}); err != nil {
		t.Fatal(err)
	}

	infos := manager.Snapshot()
	if len(infos) != 1 || infos[0].Name != "fetcher" || infos[0].Status != MCPStatusConnected {
		t.Fatalf("unexpected MCP snapshot after install: %+v", infos)
	}
}

func TestMCPManagerUninstallRemovesServerAndTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet {
			// Standalone notification-stream probe (client.go
			// startHTTPNotificationStream); this mock does not offer it.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeEcho(w, req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0.0"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			writeEcho(w, req.ID, `{"tools":[{"name":"fetch","description":"Fetch","inputSchema":{"type":"object"}}]}`)
		case "prompts/list":
			writeEcho(w, req.ID, `{"prompts":[]}`)
		case "resources/list":
			writeEcho(w, req.ID, `{"resources":[]}`)
		case mcp.MethodSubscriptionsListen:
			// MCP 2026-07-28 probe: this mock is legacy — downgrade the
			// client with the spec method-not-found error.
			echo, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
			})
			_, _ = w.Write(echo)
		default:
			t.Fatalf("unexpected method %s", req.Method)
		}
	}))
	defer server.Close()

	registry := tool.NewRegistry()
	manager := NewMCPManager(nil, registry, "")
	if err := manager.Install(context.Background(), config.MCPServerConfig{
		Name: "fetcher",
		Type: "http",
		URL:  server.URL,
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("mcp__fetcher__fetch"); !ok {
		t.Fatal("expected MCP tool to be registered before uninstall")
	}

	if !manager.Uninstall("fetcher") {
		t.Fatal("expected uninstall to remove fetcher")
	}
	if len(manager.Snapshot()) != 0 {
		t.Fatalf("expected no MCP servers after uninstall, got %+v", manager.Snapshot())
	}
	if _, ok := registry.Get("mcp__fetcher__fetch"); ok {
		t.Fatal("expected MCP tool to be unregistered after uninstall")
	}
}

// writeEcho frames a JSON-RPC success payload echoing the request id. The
// client assigns request ids dynamically (and since the subscriptions/listen
// probe occupies one slot, hardcoded ids in mocks no longer line up), so
// every mock response must echo the actual request id instead.
func writeEcho(w http.ResponseWriter, id *mcp.ID, result string) {
	resp, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      *mcp.ID         `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Result:  json.RawMessage(result),
	})
	if err != nil {
		return
	}
	_, _ = w.Write(resp)
}
