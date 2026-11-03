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

func TestNotificationHandlerNotDeadlockedOnReadMu(t *testing.T) {
	stream := encodeStdioMessages(
		t,
		Notification{JSONRPC: "2.0", Method: "notifications/tools/list_changed"},
		Response{JSONRPC: "2.0", ID: json.RawMessage(`1`), Result: json.RawMessage(`{"ok":true}`)},
	)
	client := &Client{
		name:   "notif-deadlock-test",
		reader: bufio.NewReader(bytes.NewReader(stream)),

		notificationCh:   make(chan *Notification, notificationChanSize),
		notificationDone: make(chan struct{}),
	}

	outerReadDone := make(chan struct{})
	reentered := make(chan struct{}, 1)
	var once sync.Once
	client.SetNotificationHandler(func(method string, params json.RawMessage) {

		select {
		case <-outerReadDone:
		case <-time.After(5 * time.Second):

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

		notificationCh:   make(chan *Notification, notificationChanSize),
		notificationDone: make(chan struct{}),
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

func TestAdapterMultibyteTruncationValidUTF8(t *testing.T) {

	big := strings.Repeat("中", 20000)
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
