package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// g69Server: minimal fake MCP HTTP server answering the initialize handshake
// and then replying -32602 (Invalid params) to every tasks/get and
// tasks/result - the SEP-2663 stable-extension signal for an expired,
// TTL-deleted, or unknown task.
func g69Server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req mcpTestServerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			sseResult(w, req.ID, map[string]any{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]any{"tasks": map[string]any{}},
				"serverInfo":      map[string]any{"name": "g69-fake", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tasks/get", "tasks/result":
			sa144RPCError(w, req.ID, -32602, "Invalid params: unknown or expired task")
		default:
			sa144RPCError(w, req.ID, -32601, "method not found: "+req.Method)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// G1: a -32602 on tasks/result must surface as an explicit expiry, not a
// bare "Invalid params" that reads like a client bug.
func TestG69TaskResultExpiredClassification(t *testing.T) {
	srv := g69Server(t)
	c := NewClientFromConfig(config.MCPServerConfig{Name: "g69", Type: "http", URL: srv.URL})
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := c.TaskResult(ctx, "t-gone")
	if err == nil {
		t.Fatal("expected error for -32602 tasks/result")
	}
	if !strings.Contains(err.Error(), "expired or deleted") {
		t.Fatalf("want explicit expiry wording, got: %v", err)
	}
}

// G1: the same classification applies when the poll loop (tasks/get) hits
// an expired task mid-await.
func TestG69PollExpiredClassification(t *testing.T) {
	srv := g69Server(t)
	c := NewClientFromConfig(config.MCPServerConfig{Name: "g69b", Type: "http", URL: srv.URL})
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	c.taskPollOverride = time.Millisecond
	// Bypass tools/call (server answers -32601 there) and drive the poll
	// loop directly via GetTask, which is what awaitTaskLoop calls.
	_, err := c.GetTask(ctx, "t-gone")
	if err == nil || !strings.Contains(err.Error(), "expired or deleted") {
		t.Fatalf("want explicit expiry wording from tasks/get, got: %v", err)
	}
}

// G2: stable-extension field names (ttlMs/pollIntervalMs) must decode into
// the canonical fields; old names keep winning when both are present.
func TestG69TaskStableFieldNamesDecode(t *testing.T) {
	var modern Task
	if err := json.Unmarshal([]byte(`{"taskId":"t-1","status":"working","ttlMs":30000,"pollIntervalMs":250}`), &modern); err != nil {
		t.Fatal(err)
	}
	if modern.TTL != 30000 || modern.PollInterval != 250 {
		t.Fatalf("ttlMs/pollIntervalMs not decoded: %+v", modern)
	}
	if got := modern.pollInterval(); got != 250*time.Millisecond {
		t.Fatalf("poll hint not honored: %v", got)
	}

	var legacy Task
	if err := json.Unmarshal([]byte(`{"taskId":"t-2","status":"working","ttl":5000,"pollInterval":900}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.TTL != 5000 || legacy.PollInterval != 900 {
		t.Fatalf("legacy ttl/pollInterval regression: %+v", legacy)
	}

	var both Task
	if err := json.Unmarshal([]byte(`{"taskId":"t-3","status":"working","ttl":7000,"ttlMs":99000,"pollInterval":800,"pollIntervalMs":99000}`), &both); err != nil {
		t.Fatal(err)
	}
	if both.TTL != 7000 || both.PollInterval != 800 {
		t.Fatalf("old names must win when both present: %+v", both)
	}
}
