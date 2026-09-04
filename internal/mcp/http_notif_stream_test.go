package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// The standalone GET SSE stream must deliver server-initiated notifications
// (e.g. tools/list_changed) while the client is idle - no in-flight POST.
func TestHTTPNotificationStreamDelivers(t *testing.T) {
	got := make(chan string, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming unsupported", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n"))
			flusher.Flush()
			// Hold the stream open; the client reads events as they arrive.
			<-r.Context().Done()
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
			w.Header().Set("Mcp-Session-Id", "notif-stream")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"` + latestMCPProtocolVersion + `","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"mock","version":"1.0"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "notif", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.SetNotificationHandler(func(method string, _ json.RawMessage) {
		select {
		case got <- method:
		default:
		}
	})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	select {
	case m := <-got:
		if m != "notifications/tools/list_changed" {
			t.Fatalf("unexpected notification method %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification from standalone GET stream never arrived")
	}
}

// A server that answers GET with 405 does not offer the standalone stream
// (spec-allowed); the client must disable it permanently instead of
// retry-hammering.
func TestHTTPNotificationStream405Disables(t *testing.T) {
	var getCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getCalls++
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
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"` + latestMCPProtocolVersion + `","capabilities":{},"serverInfo":{"name":"mock","version":"1.0"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{Name: "n405", Type: "http", URL: server.URL})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client.httpNotifDisabled.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !client.httpNotifDisabled.Load() {
		t.Fatal("405 must permanently disable the notification stream")
	}
	// Give any would-be retry loop a moment, then assert no hammering.
	time.Sleep(300 * time.Millisecond)
	if getCalls > 2 {
		t.Fatalf("stream retried after 405: %d GET calls", getCalls)
	}
}

// sleepUntilClosed must return false promptly once the client is closed,
// even mid-backoff.
func TestHTTPSleepUntilClosed(t *testing.T) {
	c := &Client{}
	go func() {
		time.Sleep(100 * time.Millisecond)
		c.closed.Store(true)
	}()
	start := time.Now()
	if c.sleepUntilClosed(30 * time.Second) {
		t.Fatal("expected false once closed")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("close not noticed promptly: %s", elapsed)
	}
}
