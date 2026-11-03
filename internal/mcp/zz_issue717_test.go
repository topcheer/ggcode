//go:build goolm

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

type blockingWriteCloser struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingWriteCloser() *blockingWriteCloser {
	return &blockingWriteCloser{closed: make(chan struct{})}
}

func (b *blockingWriteCloser) Write(p []byte) (int, error) {
	<-b.closed
	return 0, fmt.Errorf("write of %d bytes on closed stdin", len(p))
}

func (b *blockingWriteCloser) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestIssue717_WriteDeadlineUnblocksStuckWriter(t *testing.T) {
	oldTimeout := mcpStdioWriteTimeout
	mcpStdioWriteTimeout = 200 * time.Millisecond
	defer func() { mcpStdioWriteTimeout = oldTimeout }()

	bw := newBlockingWriteCloser()
	c := &Client{name: "u717", stdin: bw, transport: "stdio"}

	done := make(chan error, 1)
	go func() { done <- c.writeMessage(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "ping"}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from a write that never completed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("writeMessage stuck forever on a full stdin pipe — no write deadline (#717)")
	}
	select {
	case <-bw.closed:

	default:
		t.Fatal("stdin was not closed after the write deadline fired — Abort unreachable (#717)")
	}

	if err := c.writeMessage(map[string]interface{}{"jsonrpc": "2.0", "id": 2, "method": "ping"}); err == nil {
		t.Fatal("write after abort must fail")
	}
}

func TestIssue717_CloseReachableWhileWriterStuck(t *testing.T) {
	oldTimeout := mcpStdioWriteTimeout
	mcpStdioWriteTimeout = 200 * time.Millisecond
	defer func() { mcpStdioWriteTimeout = oldTimeout }()

	bw := newBlockingWriteCloser()
	c := &Client{name: "c717", stdin: bw, transport: "stdio"}

	writerDone := make(chan error, 1)
	go func() {

		big := strings.Repeat("x", 4*1024*1024)
		writerDone <- c.writeMessage(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]interface{}{"payload": big},
		})
	}()

	time.Sleep(100 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	select {
	case <-closeDone:

	case <-time.After(10 * time.Second):
		t.Fatal("Close() deadlocked behind a writer holding c.mu on a full stdin pipe (#717)")
	}
	select {
	case err := <-writerDone:
		if err == nil {
			t.Fatal("stuck writer must surface an error after abort")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stuck writer never unwound after Close/Abort (#717)")
	}
}

func TestIssue717_CloseWithRealStuckChild(t *testing.T) {
	client := NewClientFromConfig(config.MCPServerConfig{
		Name:    "e717",
		Type:    "stdio",
		Command: "/bin/sleep",
		Args:    []string{"60"},
	})
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Skipf("cannot spawn /bin/sleep: %v", err)
	}

	writerDone := make(chan error, 1)
	go func() {

		big := strings.Repeat("x", 4*1024*1024)
		writerDone <- client.writeMessage(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]interface{}{"payload": big},
		})
	}()
	time.Sleep(300 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() { closeDone <- client.Close() }()
	select {
	case <-closeDone:

	case <-time.After(15 * time.Second):
		t.Fatal("Close() never returned with a real child that stopped reading stdin (#717)")
	}
	select {
	case err := <-writerDone:
		if err == nil {
			t.Fatal("writer on a killed child's stdin must fail")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("writer goroutine never unwound after the child was killed (#717)")
	}

	var _ = json.RawMessage(`{}`)
}
