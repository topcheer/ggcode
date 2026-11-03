package mcp

import (
	"bufio"
	"context"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestIssue643_CloseAfterCrashRunsAbort(t *testing.T) {
	c := NewClient("crash-server", "true", nil)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	select {
	case <-c.ProcessExit():
	case <-time.After(5 * time.Second):
		t.Fatal("processExit not closed; test server did not exit")
	}
	if !c.IsClosed() {
		t.Fatal("client should be marked closed after process exit")
	}

	done := func() <-chan struct{} {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.notificationDone
	}()
	if done == nil {
		t.Fatal("notificationDone should be initialized by the constructor")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	select {
	case <-done:

	case <-time.After(2 * time.Second):
		t.Fatal("notificationDone not closed after Close() on a crashed client — notification goroutine leaks")
	}
}

func TestIssue643_CloseAfterCrashTwiceIdempotent(t *testing.T) {
	c := NewClient("crash-server-2", "true", nil)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	select {
	case <-c.ProcessExit():
	case <-time.After(5 * time.Second):
		t.Fatal("processExit not closed")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	c.Abort()
}

func TestIssue643_NDJSONLineLengthBounded(t *testing.T) {

	huge := strings.NewReader("{" + strings.Repeat("a", maxNDJSONLineLength+1024) + "}\n")
	r := bufio.NewReaderSize(huge, 4096)
	_, err := readBoundedLine(r, maxNDJSONLineLength)
	if err == nil {
		t.Fatal("expected error for line exceeding maxNDJSONLineLength")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}

	legit := bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
	line, err := readBoundedLine(legit, maxNDJSONLineLength)
	if err != nil {
		t.Fatalf("legit line failed: %v", err)
	}
	if !strings.HasSuffix(string(line), "\n") {
		t.Fatal("line should retain its trailing newline")
	}
}

func TestIssue643_ReadMessageUsesBoundedNDJSON(t *testing.T) {
	c := &Client{name: "ndjson"}
	c.reader = bufio.NewReader(strings.NewReader(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}` + "\n"))
	msg, err := c.readMessage(context.Background())
	if err != nil {
		t.Fatalf("readMessage failed: %v", err)
	}
	resp, ok := msg.(*Response)
	if !ok {
		t.Fatalf("expected *Response, got %T", msg)
	}
	if string(resp.ID) != "7" {
		t.Fatalf("unexpected id %s", resp.ID)
	}
}

func countNotificationGoroutines() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	count := 0
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		if strings.Contains(line, "mcp.client.notifications") {
			count++
		}
	}
	return count
}

func TestIssue643_NoNotificationGoroutineLeak(t *testing.T) {

	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := countNotificationGoroutines()

	const cycles = 3
	for i := 0; i < cycles; i++ {
		c := NewClient("leak-server", "true", nil)
		if err := c.Start(context.Background()); err != nil {
			t.Fatalf("Start %d failed: %v", i, err)
		}

		c.processNotification(&Notification{JSONRPC: "2.0", Method: "notifications/test"})
		select {
		case <-c.ProcessExit():
		case <-time.After(5 * time.Second):
			t.Fatalf("cycle %d: processExit not closed", i)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("cycle %d Close: %v", i, err)
		}
	}

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	after := countNotificationGoroutines()
	if after > before {
		t.Fatalf("notification worker goroutines leaked: before=%d after=%d (each crash-reconnect must release its worker)", before, after)
	}
}

var _ io.Reader = (*strings.Reader)(nil)
