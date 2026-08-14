package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestNotificationHandlerNotDeadlockedOnReadMu (fix #255): a notification
// handler that re-enters the client (e.g. refreshTools → ListTools → a new
// readResponseWithCancel acquiring readMu) must not deadlock against the read
// loop that dispatched the notification. Previously the handler ran
// synchronously inside readResponse while readMu was held: the re-entrant
// read blocked on readMu, the request timed out, and Abort() killed the stdio
// process — taking down every in-flight request.
func TestNotificationHandlerNotDeadlockedOnReadMu(t *testing.T) {
	stream := encodeStdioMessages(
		t,
		Notification{JSONRPC: "2.0", Method: "notifications/tools/list_changed"},
		Response{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"ok":true}`)},
	)
	client := &Client{
		name:   "notif-deadlock-test",
		reader: bufio.NewReader(bytes.NewReader(stream)),
	}

	// Handler simulates the mcp_loader refresh path: it issues a follow-up
	// read that would need readMu. Deliberately blocked until we confirm the
	// read loop finished the outer read (i.e. handler is NOT on its stack).
	outerReadDone := make(chan struct{})
	reentered := make(chan struct{}, 1)
	var once sync.Once
	client.SetNotificationHandler(func(method string, params json.RawMessage) {
		// Wait until the outer readResponse has returned, proving the handler
		// is running on its own goroutine and the read loop released readMu.
		select {
		case <-outerReadDone:
		case <-time.After(5 * time.Second):
			// The outer read cannot finish while the handler blocks it if the
			// handler were (bug) invoked synchronously inside the read loop.
		}
		once.Do(func() { close(reentered) })
	})

	resp, err := client.readResponse(context.Background(), nil, nil)
	close(outerReadDone)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Result) != `{"ok":true}` {
		t.Fatalf("unexpected response: %s", string(resp.Result))
	}
	select {
	case <-reentered:
	case <-time.After(5 * time.Second):
		t.Fatal("notification handler never ran; dispatch goroutine not started")
	}
}

// TestNotificationOrderPreservedAsyncDispatch (fix #255): notifications are
// delivered to the handler in arrival order despite asynchronous dispatch.
func TestNotificationOrderPreservedAsyncDispatch(t *testing.T) {
	stream := encodeStdioMessages(
		t,
		Notification{JSONRPC: "2.0", Method: "notifications/a"},
		Notification{JSONRPC: "2.0", Method: "notifications/b"},
		Notification{JSONRPC: "2.0", Method: "notifications/c"},
		Response{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"ok":true}`)},
	)
	client := &Client{
		name:   "notif-order-test",
		reader: bufio.NewReader(bytes.NewReader(stream)),
	}

	received := make(chan string, 3)
	client.SetNotificationHandler(func(method string, params json.RawMessage) {
		received <- method
	})

	if _, err := client.readResponse(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	var got []string
	for len(got) < 3 {
		select {
		case m := <-received:
			got = append(got, m)
		case <-deadline:
			t.Fatalf("timed out waiting for notifications; got %v", got)
		}
	}
	want := []string{"notifications/a", "notifications/b", "notifications/c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("notification order: got %v, want %v", got, want)
		}
	}
}

// TestAdapterMultibyteTruncationValidUTF8 (fix #262): truncating a >50KB
// result made of multi-byte runes must still produce valid UTF-8. The old
// byte-slice cut split Chinese characters into invalid sequences.
func TestAdapterMultibyteTruncationValidUTF8(t *testing.T) {
	// 3-byte runes: ensure the 50KB boundary lands mid-rune with high
	// probability; 60000 bytes of "中" = 20000 runes.
	big := strings.Repeat("中", 20000) // 60000 bytes > 51200 cap
	if len(big) <= 50*1024 {
		t.Fatalf("test content must exceed cap: %d bytes", len(big))
	}
	caller := &mockCaller{
		result: &CallToolResult{Content: []ToolContent{{Type: "text", Text: big}}},
	}
	adapter := NewAdapter("trunc-srv", caller, []ToolDefinition{
		{Name: "dump", Description: "dump"},
	})
	registry := tool.NewRegistry()
	if err := adapter.RegisterTools(registry); err != nil {
		t.Fatal(err)
	}
	regTool, ok := registry.Get("mcp__trunc-srv__dump")
	if !ok {
		t.Fatal("tool not registered")
	}
	res, err := regTool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	if !utf8.ValidString(res.Content) {
		t.Errorf("truncated content is not valid UTF-8 (len=%d)", len(res.Content))
	}
	if !strings.Contains(res.Content, "[... MCP result truncated") {
		t.Error("truncation marker missing")
	}
}
