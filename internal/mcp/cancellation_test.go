//go:build goolm

package mcp

// MCP cancellation protocol conformance (spec 2025-03-26,
// https://modelcontextprotocol.io/specification/2025-03-26/basic/utilities/cancellation):
//
//  1. When the client abandons an in-flight request (caller deadline,
//     user interrupt, per-request timeout), it MUST emit a
//     notifications/cancelled notification referencing the outstanding
//     request id, so the server can stop processing and free resources.
//  2. The initialize request MUST NOT be cancelled by clients.
//
// Late responses racing the notification are spec-permitted: receivers
// ignore unknown or already-completed request IDs, so the test also
// exercises the client tolerating a tools/call response that arrives after
// it gave up.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// sseResult writes a JSON-RPC response as a streamable-HTTP SSE event —
// the wire shape the #716 tests established for this client's HTTP
// transport.
func sseResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	resp := map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result}
	payload, _ := json.Marshal(resp)
	_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// mcpTestServerRequest is the decoded wire request used by the handlers.
type mcpTestServerRequest struct {
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
	Params json.RawMessage `json:"params"`
}

func TestMCPCancellation_NotifiesServerOnGiveUp(t *testing.T) {
	var mu sync.Mutex
	var callID json.RawMessage
	cancelled := make(chan struct {
		RequestID json.RawMessage `json:"requestId"`
		Reason    string          `json:"reason"`
	}, 4)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req mcpTestServerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			sseResult(w, req.ID, map[string]interface{}{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "mock", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/call":
			mu.Lock()
			callID = req.ID
			mu.Unlock()
			// Long-running tool; the client gives up long before this
			// responds. The late response races the cancellation
			// notification and must not disturb the client.
			time.Sleep(2 * time.Second)
			sseResult(w, req.ID, map[string]interface{}{
				"content": []interface{}{map[string]string{"type": "text", "text": "done"}},
			})
		case "notifications/cancelled":
			var params struct {
				RequestID json.RawMessage `json:"requestId"`
				Reason    string          `json:"reason"`
			}
			_ = json.Unmarshal(req.Params, &params)
			select {
			case cancelled <- params:
			default:
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "cancel-giveup",
		Type: "http",
		URL:  server.URL,
	})
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	callCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := client.CallTool(callCtx, "slow", nil); err == nil {
		t.Fatal("expected tools/call to fail after the 300ms deadline")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("CallTool blocked %v past its 300ms deadline", elapsed)
	}

	select {
	case params := <-cancelled:
		mu.Lock()
		want := callID
		mu.Unlock()
		if !bytes.Equal(params.RequestID, want) {
			t.Fatalf("cancelled requestId %s does not match the outstanding request id %s", params.RequestID, want)
		}
		if params.Reason == "" {
			t.Fatal("expected a non-empty cancellation reason for logs")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never received notifications/cancelled after the client gave up")
	}
}

func TestMCPCancellation_InitializeNeverCancelled(t *testing.T) {
	gotCancelled := make(chan json.RawMessage, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req mcpTestServerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			// Slow handshake; the client deadline expires mid-flight.
			time.Sleep(1500 * time.Millisecond)
			sseResult(w, req.ID, map[string]interface{}{
				"protocolVersion": latestMCPProtocolVersion,
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "mock", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "notifications/cancelled":
			var params struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			_ = json.Unmarshal(req.Params, &params)
			select {
			case gotCancelled <- params.RequestID:
			default:
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "cancel-init",
		Type: "http",
		URL:  server.URL,
	})
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	initCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := client.Initialize(initCtx); err == nil {
		t.Fatal("expected initialize to fail after the 300ms deadline")
	}

	// The spec forbids clients from cancelling initialize: no
	// notifications/cancelled may be emitted for it.
	select {
	case id := <-gotCancelled:
		t.Fatalf("initialize was cancelled in violation of the spec (requestId %s)", id)
	case <-time.After(1 * time.Second):
	}
}
