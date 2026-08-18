//go:build goolm

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// TestIssue716_SSEStreamHeldOpenDoesNotBlock (#716): a spec-compliant
// streamable-HTTP server may keep the SSE response stream open after the
// Response event. The old code drained the body to EOF before parsing, so
// every request (here: initialize) blocked for the full 120s request
// timeout. The client must return as soon as the Response event arrives.
func TestIssue716_SSEStreamHeldOpenDoesNotBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var raw map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		switch raw["method"] {
		case "initialize":
			// Notification first, then the Response, then the stream stays
			// open — exactly the spec-permitted shape the old drain-to-EOF
			// hung on.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, _ := w.(http.Flusher)
			_, _ = io.WriteString(w, "data: "+`{"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			respEvent := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      raw["id"],
				"result": map[string]interface{}{
					"protocolVersion": latestMCPProtocolVersion,
					"capabilities":    map[string]interface{}{"tools": map[string]interface{}{"listChanged": true}},
					"serverInfo":      map[string]interface{}{"name": "mock", "version": "1.0"},
				},
			}
			payload, _ := json.Marshal(respEvent)
			_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			// Hold the stream open until the client goes away — never send EOF.
			<-r.Context().Done()
		case "notifications/initialized":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %v", raw["method"])
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "g716",
		Type: "http",
		URL:  server.URL,
	})
	// The notification rides the initialize response stream — the handler
	// must be installed BEFORE Initialize or it would be dropped (#716's
	// second symptom: notifications discarded, no GET stream to recover).
	notifCh := make(chan string, 1)
	client.SetNotificationHandler(func(method string, _ json.RawMessage) {
		select {
		case notifCh <- method:
		default:
		}
	})
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	start := time.Now()
	initRes, err := client.Initialize(ctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("initialize (stream held open): %v", err)
	}
	if initRes == nil || initRes.ServerInfo.Name != "mock" {
		t.Fatalf("unexpected init result: %+v", initRes)
	}
	// The old code sat here for the full 120s mcpRequestTimeout; anything
	// under 10s proves the response was parsed at the event boundary instead
	// of after EOF.
	if elapsed > 10*time.Second {
		t.Fatalf("initialize blocked %v on a spec-permitted open SSE stream (#716 drain-to-EOF regression)", elapsed)
	}
	select {
	case method := <-notifCh:
		if method != "notifications/tools/list_changed" {
			t.Fatalf("unexpected notification: %q", method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server notification on the response stream was discarded (#716)")
	}
}

// TestIssue716_StreamParserEarlyReturnAndForeignIDSkip: unit-level checks on
// the streaming parser — early return on the matching Response without EOF,
// notification routing, and #597 M1 foreign-id skipping preserved in the
// streaming path.
func TestIssue716_StreamParserEarlyReturnAndForeignIDSkip(t *testing.T) {
	makeClient := func(notifCh chan string) *Client {
		c := &Client{name: "u716", transport: "http"}
		c.SetNotificationHandler(func(method string, _ json.RawMessage) {
			select {
			case notifCh <- method:
			default:
			}
		})
		return c
	}

	t.Run("ours", func(t *testing.T) {
		notifCh := make(chan string, 1)
		c := makeClient(notifCh)
		pr, pw := io.Pipe()
		defer pr.Close()
		go func() {
			// One Write: both events land in the scanner buffer together, so
			// the parser can return after the second blank line without
			// another Read ever blocking on the still-open pipe.
			_, _ = pw.Write([]byte(
				"data: " + `{"jsonrpc":"2.0","method":"notifications/message","params":{}}` + "\n\n" +
					"data: " + `{"jsonrpc":"2.0","id":7,"result":{"ok":true}}` + "\n\n"))
		}()
		id := NewIntID(7)
		reqID := &id
		start := time.Now()
		resp, err := c.streamHTTPSSEResponse(pr, reqID)
		if err != nil {
			t.Fatalf("stream parse: %v", err)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("parser waited for EOF on an open stream (#716)")
		}
		if resp.ID == nil || !responseIDMatches(resp.ID, reqID) {
			t.Fatalf("wrong response id: %+v", resp.ID)
		}
		select {
		case method := <-notifCh:
			if method != "notifications/message" {
				t.Fatalf("unexpected notification: %q", method)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("notification seen on the stream was not routed to the handler (#716)")
		}
	})

	t.Run("foreign-id skipped", func(t *testing.T) {
		c := makeClient(make(chan string, 1))
		pr, pw := io.Pipe()
		defer pr.Close()
		go func() {
			_, _ = pw.Write([]byte(
				"data: " + `{"jsonrpc":"2.0","id":111,"result":{"foreign":true}}` + "\n\n" +
					"data: " + `{"jsonrpc":"2.0","id":222,"result":{"ours":true}}` + "\n\n"))
		}()
		id := NewIntID(222)
		reqID := &id
		resp, err := c.streamHTTPSSEResponse(pr, reqID)
		if err != nil {
			t.Fatalf("stream parse: %v", err)
		}
		if resp.ID == nil || !responseIDMatches(resp.ID, reqID) {
			t.Fatalf("cross-injection: got response id %+v, wanted 222 (#597 M1 in streaming path)", resp.ID)
		}
	})
}
