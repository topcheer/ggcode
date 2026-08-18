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

// blockingWriteCloser simulates a child that stopped reading stdin: writes
// block until Close is called (as they would on a full pipe whose read end
// disappears when the child is killed).
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

// TestIssue717_WriteDeadlineUnblocksStuckWriter (#717): a stdin write that
// never completes (child stopped reading) used to block forever while the
// caller held c.mu. The deadline path must fire, abort the transport, and
// return an error.
func TestIssue717_WriteDeadlineUnblocksStuckWriter(t *testing.T) {
	oldTimeout := mcpStdioWriteTimeout
	mcpStdioWriteTimeout = 200 * time.Millisecond
	defer func() { mcpStdioWriteTimeout = oldTimeout }()

	bw := newBlockingWriteCloser()
	c := &Client{name: "u717", stdin: bw, transport: "stdio"}

	// writeMessage takes c.mu and must give it back when the write stalls.
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
		// Abort closed stdin as part of unwinding the stuck writer.
	default:
		t.Fatal("stdin was not closed after the write deadline fired — Abort unreachable (#717)")
	}
	// The lock must be free again: another write can acquire and fail fast
	// (client now closed).
	if err := c.writeMessage(map[string]interface{}{"jsonrpc": "2.0", "id": 2, "method": "ping"}); err == nil {
		t.Fatal("write after abort must fail")
	}
}

// TestIssue717_CloseReachableWhileWriterStuck (#717): the old Close()
// locked c.mu before calling Abort(); with a writer parked on a full pipe
// while holding c.mu, Close() deadlocked forever. Close must now tear the
// transport down first and return promptly.
func TestIssue717_CloseReachableWhileWriterStuck(t *testing.T) {
	oldTimeout := mcpStdioWriteTimeout
	mcpStdioWriteTimeout = 200 * time.Millisecond
	defer func() { mcpStdioWriteTimeout = oldTimeout }()

	bw := newBlockingWriteCloser()
	c := &Client{name: "c717", stdin: bw, transport: "stdio"}

	writerDone := make(chan error, 1)
	go func() {
		// Large frame: bigger than any pipe buffer, so the write genuinely
		// cannot complete.
		big := strings.Repeat("x", 4*1024*1024)
		writerDone <- c.writeMessage(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]interface{}{"payload": big},
		})
	}()
	// Give the writer a moment to grab c.mu and stall inside Write.
	time.Sleep(100 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	select {
	case <-closeDone:
		// Close returned despite the stuck writer — regression fixed.
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

// TestIssue717_CloseWithRealStuckChild: end-to-end variant — a real child
// process that never reads stdin (/bin/sleep), a real full pipe, and Close()
// which must stay reachable and kill the child.
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
		// ~4MB frame: far beyond any platform pipe buffer, so the write
		// blocks until the child dies.
		big := strings.Repeat("x", 4*1024*1024)
		writerDone <- client.writeMessage(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]interface{}{"payload": big},
		})
	}()
	time.Sleep(300 * time.Millisecond) // let the writer stall on the full pipe

	closeDone := make(chan error, 1)
	go func() { closeDone <- client.Close() }()
	select {
	case <-closeDone:
		// Success: Abort killed the child, the writer got EPIPE, c.mu was
		// released, Close finished.
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
	// The notification payload marshals fine — keep json imported even if
	// assertions above are trimmed in the future.
	var _ = json.RawMessage(`{}`)
}
