package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/config"
)

func TestWebSocketConcurrentRequestsRoutedByWaiter(t *testing.T) {
	upgrader := websocket.Upgrader{}
	type pending struct {
		id  *ID
		arg string
	}
	var mu sync.Mutex
	var pendingCalls []pending
	var answered bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req Request
			if err := json.Unmarshal(payload, &req); err != nil {
				continue
			}
			if req.Method != "tools/call" {
				continue
			}
			var params CallToolParams
			_ = json.Unmarshal(req.Params, &params)
			mu.Lock()
			pendingCalls = append(pendingCalls, pending{id: req.ID, arg: params.Arguments["name"].(string)})
			n := len(pendingCalls)
			calls := append([]pending(nil), pendingCalls...)
			mu.Unlock()
			if n < 2 {

				continue
			}

			mu.Lock()
			if answered {
				mu.Unlock()
				continue
			}
			answered = true
			mu.Unlock()
			for i := len(calls) - 1; i >= 0; i-- {
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      calls[i].id,
					"result": map[string]any{
						"content": []map[string]any{{"type": "text", "text": "resp-" + calls[i].arg}},
					},
				})
			}
		}
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "ws-523",
		Type: "ws",
		URL:  "ws" + strings.TrimPrefix(server.URL, "http"),
	})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	type outcome struct {
		text string
		err  error
	}
	done := make(chan outcome, 2)
	for _, arg := range []string{"alpha", "beta"} {
		go func(name string) {
			res, err := client.CallTool(ctx, "echo", map[string]interface{}{"name": name})
			if err != nil {
				done <- outcome{err: err}
				return
			}
			if len(res.Content) != 1 {
				done <- outcome{err: errUnexpectedShape(len(res.Content))}
				return
			}
			done <- outcome{text: res.Content[0].Text}
		}(arg)
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case out := <-done:
			if out.err != nil {
				t.Fatalf("concurrent request failed (dropped-response regression, #523 Bug A): %v", out.err)
			}
			if out.text != "resp-alpha" && out.text != "resp-beta" {
				t.Fatalf("unexpected response text %q", out.text)
			}
			if got[out.text] {
				t.Fatalf("duplicate response text %q — one response was consumed twice", out.text)
			}
			got[out.text] = true
		case <-time.After(30 * time.Second):
			t.Fatal("concurrent WS request did not finish in time — response for one caller was dropped (false 120s timeout, #523 Bug A)")
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected both callers answered, got %v", got)
	}
}

func errUnexpectedShape(n int) error {
	return fmt.Errorf("unexpected content length %d", n)
}

func TestHTTPCloseNotBlockedByInflightRequest(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "http-523",
		Type: "http",
		URL:  server.URL,
	})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reqCancel()
	reqDone := make(chan error, 1)
	go func() {
		_, err := client.CallTool(reqCtx, "hang", nil)
		reqDone <- err
	}()

	time.Sleep(200 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case <-closed:

	case <-time.After(2 * time.Second):
		t.Fatal("Close() blocked behind an in-flight HTTP request (#523 Bug B)")
	}

	select {
	case <-reqDone:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request did not unwind after Close()")
	}
}

func TestHTTPConcurrentRequestsRunInParallel(t *testing.T) {
	var inFlight, peak int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		w.Header().Set("Content-Type", "application/json")

		body, _ := io.ReadAll(r.Body)
		var env struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(body, &env)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(env.ID) + `,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	defer server.Close()

	client := NewClientFromConfig(config.MCPServerConfig{
		Name: "http-par-523",
		Type: "http",
		URL:  server.URL,
	})
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.CallTool(ctx, "slow", nil); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent request failed: %v", err)
	}
	if got := atomic.LoadInt32(&peak); got < 2 {
		t.Fatalf("requests were serialized (peak in-flight=%d, want 2) — c.mu still spans the HTTP roundtrip (#523 Bug B)", got)
	}
}
