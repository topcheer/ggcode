package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// MCP spec 2025-06-18 (PR #548) requires every HTTP request after initialize
// to carry the negotiated protocol version in the MCP-Protocol-Version header.
// These tests pin that contract on both HTTP surfaces: the JSON-RPC POST path
// and the standalone GET SSE notification stream.

// captureHeaders records the MCP-Protocol-Version header seen per JSON-RPC
// method (and on GET requests) so tests can assert spec compliance.
type captureHeaders struct {
	mu     sync.Mutex
	byName map[string]string // method (or "GET") -> header value
}

func (c *captureHeaders) record(name, val string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName[name] = val
}

func (c *captureHeaders) get(name string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.byName[name]
	return v, ok
}

// newVersionHeaderTestServer starts a mock MCP HTTP server that answers
// initialize (negotiating the given version), notifications/initialized,
// and tools/list, while capturing the MCP-Protocol-Version header of every
// request. GET requests open a short-lived SSE stream.
func newVersionHeaderTestServer(t *testing.T, negotiatedVersion string, cap *captureHeaders) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// OAuth well-known probes hit the same mux; only the bare root
			// path is the MCP standalone SSE stream.
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			cap.record("GET", r.Header.Get("MCP-Protocol-Version"))
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming unsupported", http.StatusInternalServerError)
				return
			}
			flusher.Flush()
			<-r.Context().Done()
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		cap.record(req.Method, r.Header.Get("MCP-Protocol-Version"))
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "ver-hdr-test")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"` + negotiatedVersion + `","capabilities":{"tools":{}},"serverInfo":{"name":"mock","version":"1.0"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":99,"error":{"code":-32601,"message":"method not found"}}`))
		}
	})
	return httptest.NewServer(mux)
}

// The initialize request itself must NOT carry the header (there is no
// negotiated version yet); every subsequent POST must carry exactly the
// negotiated version.
func TestProtocolVersionHeaderAfterInitialize(t *testing.T) {
	cap := &captureHeaders{byName: map[string]string{}}
	server := newVersionHeaderTestServer(t, latestMCPProtocolVersion, cap)
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "verhdr", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// NOTE: no explicit ctx timeout - sandbox HTTP can take seconds/request;
	// the harness timeout bounds the test.
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	if v, ok := cap.get("initialize"); ok && v != "" {
		t.Errorf("initialize request must omit MCP-Protocol-Version (no version negotiated yet), got %q", v)
	}
	if v, ok := cap.get("tools/list"); !ok {
		t.Fatal("tools/list request was not captured")
	} else if v != latestMCPProtocolVersion {
		t.Errorf("tools/list MCP-Protocol-Version = %q, want %q", v, latestMCPProtocolVersion)
	}
	if v, ok := cap.get("notifications/initialized"); !ok {
		t.Fatal("notifications/initialized request was not captured")
	} else if v != latestMCPProtocolVersion {
		t.Errorf("notifications/initialized MCP-Protocol-Version = %q, want %q", v, latestMCPProtocolVersion)
	}
}

// When the server negotiates an older supported version, subsequent requests
// must carry THAT version, not the client's latest.
func TestProtocolVersionHeaderNegotiatedDown(t *testing.T) {
	const old = "2025-03-26"
	cap := &captureHeaders{byName: map[string]string{}}
	server := newVersionHeaderTestServer(t, old, cap)
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "verhdr-old", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.NegotiatedVersion(); got != old {
		t.Fatalf("negotiated version = %q, want %q", got, old)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	if v, ok := cap.get("tools/list"); !ok {
		t.Fatal("tools/list request was not captured")
	} else if v != old {
		t.Errorf("tools/list MCP-Protocol-Version = %q, want negotiated %q", v, old)
	}
}

// The standalone GET SSE stream is a "subsequent request" too and must carry
// the negotiated version header.
func TestProtocolVersionHeaderOnSSEGetStream(t *testing.T) {
	cap := &captureHeaders{byName: map[string]string{}}
	server := newVersionHeaderTestServer(t, latestMCPProtocolVersion, cap)
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "verhdr-sse", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if v, ok := cap.get("GET"); ok {
			if v != latestMCPProtocolVersion {
				t.Errorf("SSE GET MCP-Protocol-Version = %q, want %q", v, latestMCPProtocolVersion)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("standalone GET SSE stream never opened")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
