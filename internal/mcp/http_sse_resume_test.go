package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestHTTPNotifStreamResumabilityLastEventID(t *testing.T) {
	var lastSeenHeader atomic.Value
	var getCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
			return
		}
		if getCalls.Add(1) == 1 {

			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("id: 7\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n"))
			return
		}

		lastSeenHeader.Store(r.Header.Get("Last-Event-ID"))
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "resume", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if res := client.readHTTPNotifStreamOnce(context.Background()); res != httpNotifDrop {
		t.Fatalf("first read: want httpNotifDrop, got %v", res)
	}
	if got := client.lastEventIDSnapshot(); got != "7" {
		t.Fatalf("cursor after first stream: want \"7\", got %q", got)
	}
	if res := client.readHTTPNotifStreamOnce(context.Background()); res != httpNotifUnsupported {
		t.Fatalf("second read: want httpNotifUnsupported, got %v", res)
	}
	if got, _ := lastSeenHeader.Load().(string); got != "7" {
		t.Fatalf("reconnect must send Last-Event-ID: 7, got %q", got)
	}
}

func TestStreamHTTPSSEResponseRecordsEventID(t *testing.T) {
	c := &Client{name: "post-cursor", headers: map[string]string{}}
	body := "id: 42\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"
	resp, err := c.streamHTTPSSEResponse(strings.NewReader(body), nil)
	if err != nil {
		t.Fatalf("streamHTTPSSEResponse: %v", err)
	}
	if resp == nil {
		t.Fatal("expected a response")
	}
	if got := c.lastEventIDSnapshot(); got != "42" {
		t.Fatalf("cursor from POST stream: want \"42\", got %q", got)
	}
}

func TestNewSessionResetsLastEventID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {

			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "fresh-session")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"` + latestMCPProtocolVersion + `","capabilities":{},"serverInfo":{"name":"mock","version":"1.0"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "reset", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.mu.Lock()
	client.httpNotifLastEventID = "stale"
	client.sessionID = "old-session"
	client.mu.Unlock()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.lastEventIDSnapshot(); got != "" {
		t.Fatalf("new session must reset the cursor, got %q", got)
	}
}

func TestIDlessStreamKeepsEmptyCursor(t *testing.T) {
	c := &Client{name: "idless", headers: map[string]string{}}
	c.noteSSEEventID("")
	if got := c.lastEventIDSnapshot(); got != "" {
		t.Fatalf("empty id must be ignored, got %q", got)
	}
	c.noteSSEEventID("3")
	if got := c.lastEventIDSnapshot(); got != "3" {
		t.Fatalf("cursor should track ids, got %q", got)
	}
}
