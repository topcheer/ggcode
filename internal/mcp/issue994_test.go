package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func stdioFakeServer(t *testing.T) (*Client, func()) {
	t.Helper()

	reqRead, reqWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("request pipe: %v", err)
	}
	respRead, respWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("response pipe: %v", err)
	}

	client := &Client{
		name:      "stdio-fake",
		transport: "stdio",
		stdin:     reqWrite,
		reader:    bufio.NewReader(respRead),
	}

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		scanner := bufio.NewScanner(reqRead)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var req struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(line, &req); err != nil {
				continue
			}
			resp, _ := json.Marshal(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]interface{}{"echo": req.ID},
			})
			respWrite.Write(append(resp, '\n'))
		}
	}()

	cleanup := func() {
		reqWrite.Close()
		reqRead.Close()
		respWrite.Close()
		respRead.Close()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			t.Error("fake stdio server did not exit")
		}
	}
	return client, cleanup
}

func TestIssue994StdioConcurrentFastResponses(t *testing.T) {
	client, cleanup := stdioFakeServer(t)
	defer cleanup()

	const n = 24
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
			defer rcancel()
			var out struct {
				Echo int64 `json:"echo"`
			}
			if err := client.sendRequest(rctx, "test/concurrent", map[string]interface{}{}, &out); err != nil {
				errs <- fmt.Errorf("req %d: %w", i, err)
				return
			}
			if out.Echo == 0 {
				errs <- fmt.Errorf("req %d: response misrouted (echo=0)", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

type errStdin struct{}

func (errStdin) Write(p []byte) (int, error) { return 0, errors.New("synthetic stdin failure") }
func (errStdin) Close() error                { return nil }

func TestIssue994StdioWriteFailureNoWaiterLeak(t *testing.T) {
	client := &Client{
		name:      "stdio-write-fail",
		transport: "stdio",
		stdin:     errStdin{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id := NewIntID(1)
	req := Request{JSONRPC: "2.0", Method: "test/method", Params: json.RawMessage(`{}`), ID: &id}
	if _, err := client.sendRequestUnlocked(req, ctx); err == nil {
		t.Fatal("expected write error from failing stdin")
	}

	client.mu.Lock()
	leaked := len(client.waiters)
	client.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("waiter leaked after write failure: %d entries remain", leaked)
	}
}

func TestIssue994SendWSUnlockedNilConnNoPanic(t *testing.T) {
	c := NewClient("ws-nil", "ws", nil)
	if c.wsConn != nil {
		t.Fatal("precondition: fresh ws client should have nil wsConn")
	}
	id := NewIntID(1)
	req := Request{JSONRPC: "2.0", Method: "test/method", Params: json.RawMessage(`{}`), ID: &id}
	resp, err := c.sendWSUnlocked(context.Background(), req)
	if err == nil {
		t.Fatalf("expected error for nil wsConn, got resp=%v", resp)
	}
	if c.wsConn != nil {
		t.Fatal("wsConn must remain nil")
	}

	c.mu.Lock()
	leaked := len(c.waiters)
	c.mu.Unlock()
	if leaked != 0 {
		t.Fatalf("waiter leaked after failed ws write: %d entries remain", leaked)
	}
}

func TestIssue994RespondToServerRequestWSNilConnNoPanic(t *testing.T) {
	c := NewClient("ws-nil", "ws", nil)
	id := NewIntID(7)
	req := &Request{JSONRPC: "2.0", Method: "sampling/createMessage", ID: &id}
	if err := c.respondToServerRequestWS(req); err == nil {
		t.Fatal("expected error for nil wsConn, not nil")
	}
}
