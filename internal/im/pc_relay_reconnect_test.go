package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// --- test relay server ---

// pcTestRelayServer is a minimal relay provider endpoint for tests: upgrades
// WebSocket connections, sends relay:provider_ready, then drains (which also
// answers client heartbeat pings per RFC6455).
type pcTestRelayServer struct {
	srv      *httptest.Server
	url      string
	upgrades atomic.Int32

	mu      sync.Mutex
	conns   []*websocket.Conn
	writeMu sync.Mutex // gorilla/websocket allows a single concurrent writer per conn
}

func newPCTestRelayServer(t *testing.T) *pcTestRelayServer {
	t.Helper()
	ts := &pcTestRelayServer{}
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/provider", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ts.upgrades.Add(1)
		ts.mu.Lock()
		ts.conns = append(ts.conns, conn)
		ts.mu.Unlock()
		ready, _ := json.Marshal(map[string]string{"type": pcTypeProviderReady})
		ts.writeMu.Lock()
		err = conn.WriteMessage(websocket.TextMessage, ready)
		ts.writeMu.Unlock()
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	ts.srv = httptest.NewServer(mux)
	t.Cleanup(ts.srv.Close)
	t.Cleanup(ts.closeAll)
	ts.url = "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws/provider"
	return ts
}

func (ts *pcTestRelayServer) latestConn() *websocket.Conn {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.conns) == 0 {
		return nil
	}
	return ts.conns[len(ts.conns)-1]
}

// sendFrame writes a server->client message synchronously (single writer).
func (ts *pcTestRelayServer) sendFrame(conn *websocket.Conn, data []byte) error {
	ts.writeMu.Lock()
	defer ts.writeMu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}

// closeAll abruptly drops every server-side connection (simulates relay
// restart / network drop).
func (ts *pcTestRelayServer) closeAll() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, c := range ts.conns {
		_ = c.Close()
	}
	ts.conns = nil
}

func pcWaitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", timeout, msg)
}

// --- Issue #965 problem 1: ReadLoop must not bind to the caller ctx ---

// TestPCRelayReadLoopSurvivesCallerCtxCancel verifies that cancelling the ctx
// passed to Connect (as runtime sendWithTimeout does the moment Send returns)
// does NOT kill the resident ReadLoop. Old behavior: the loop exited after
// processing the first post-cancel inbound message, so the second frame was
// never delivered.
func TestPCRelayReadLoopSurvivesCallerCtxCancel(t *testing.T) {
	ts := newPCTestRelayServer(t)

	client := newPCRelayClient(ts.url, "test-provider")
	frames := make(chan string, 4)
	client.onFrame = func(sessionID string, envelope *pcEncryptedEnvelope) {
		frames <- sessionID
	}
	defer client.Dispose()

	ctx, cancel := context.WithCancel(context.Background())
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	cancel() // simulate request-scoped ctx dying right after first Send

	for i := 0; i < 2; i++ {
		sid := fmt.Sprintf("sess-%d", i)
		frame, _ := json.Marshal(pcRelayFrame{
			Type:      pcTypeRelayFrame,
			SessionID: sid,
		})
		conn := ts.latestConn()
		if conn == nil {
			t.Fatal("no server-side connection")
		}
		if err := ts.sendFrame(conn, frame); err != nil {
			t.Fatalf("server write frame %d: %v", i, err)
		}
		select {
		case got := <-frames:
			if got != sid {
				t.Fatalf("frame %d: got session %q, want %q", i, got, sid)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("frame %d not received after caller ctx cancel — ReadLoop died with the request-scoped ctx", i)
		}
	}
}

// --- Issue #965 problem 2: disconnect must be observed and recovered ---

// TestPCAdapterHandleRelayDisconnectedClearsClientAndPublishes verifies the
// ReadLoop-exit callback: the dead client is cleared (ensureConnected no
// longer short-circuits on a stale pointer) and "disconnected" is published
// to the manager.
func TestPCAdapterHandleRelayDisconnectedClearsClientAndPublishes(t *testing.T) {
	mgr := NewManager()
	adapter := &pcAdapter{name: "pc-test", manager: mgr, providerID: "p1"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // adapter stopped: must suppress the background reconnect loop
	adapter.ctx = ctx

	dead := &pcRelayClient{}
	adapter.mu.Lock()
	adapter.client = dead
	adapter.mu.Unlock()

	adapter.handleRelayDisconnected(dead, "peer dropped")

	if got := adapter.currentClient(); got != nil {
		t.Fatalf("expected dead client to be cleared, got %v", got)
	}
	mgr.mu.RLock()
	state, ok := mgr.adapters[adapter.name]
	mgr.mu.RUnlock()
	// "reconnecting" (not terminal "disconnected") because the manager's
	// terminal-state guard drops all updates once status == disconnected;
	// the adapter intends to recover, so the state must stay mutable.
	if !ok || state.Status != "reconnecting" || state.Healthy {
		t.Fatalf("expected published reconnecting state, got ok=%v state=%+v", ok, state)
	}

	// A disconnect from a client that is not the installed one must be a
	// no-op (e.g. a failed Connect candidate or an already-replaced client).
	adapter.handleRelayDisconnected(&pcRelayClient{}, "stale event")
}

// TestPCAdapterReconnectsAfterRelayDrop verifies end-to-end recovery: when
// the relay drops the connection, the adapter clears the dead client,
// reconnects with a fresh client (using the reconnect backoff loop), and
// Close() tears the client down.
func TestPCAdapterReconnectsAfterRelayDrop(t *testing.T) {
	ts := newPCTestRelayServer(t)
	mgr := NewManager()
	adapter := &pcAdapter{
		name:         "pc-reconnect",
		manager:      mgr,
		providerID:   "p1",
		relayBaseURL: ts.url,
		sessionTTLMs: 3600000,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter.Start(ctx)
	defer adapter.Close()

	if err := adapter.ensureConnected(context.Background()); err != nil {
		t.Fatalf("initial connect: %v", err)
	}
	first := adapter.currentClient()
	if first == nil {
		t.Fatal("no client after initial connect")
	}
	if got := ts.upgrades.Load(); got != 1 {
		t.Fatalf("expected 1 upgrade after initial connect, got %d", got)
	}

	// Drop the server side: ReadLoop exits -> adapter clears client ->
	// background reconnect loop establishes a new connection.
	ts.closeAll()

	pcWaitFor(t, 5*time.Second, func() bool {
		c := adapter.currentClient()
		return c != nil && c != first
	}, "adapter did not replace the dead relay client")
	pcWaitFor(t, 5*time.Second, func() bool { return ts.upgrades.Load() >= 2 },
		"reconnect loop did not dial a second connection")

	mgr.mu.RLock()
	state := mgr.adapters[adapter.name]
	mgr.mu.RUnlock()
	if state.Status != "connected" || !state.Healthy {
		t.Fatalf("expected connected state after reconnect, got %+v", state)
	}
}

// --- Issue #965 problem 3: concurrent ensureConnected must not double-dial ---

// TestPCAdapterEnsureConnectedConcurrentSingleConnection races N concurrent
// ensureConnected calls and asserts exactly one WebSocket connection is
// established (run under -race; the old RLock-check/Lock-write window let
// every caller dial its own connection and leak the losers).
func TestPCAdapterEnsureConnectedConcurrentSingleConnection(t *testing.T) {
	ts := newPCTestRelayServer(t)
	adapter := &pcAdapter{
		name:         "pc-concurrent",
		providerID:   "p1",
		relayBaseURL: ts.url,
		sessionTTLMs: 3600000,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter.Start(ctx)
	defer adapter.Close()

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = adapter.ensureConnected(context.Background())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("ensureConnected %d failed: %v", i, err)
		}
	}
	if got := ts.upgrades.Load(); got != 1 {
		t.Fatalf("expected exactly 1 websocket upgrade, got %d (TOCTOU double-connect)", got)
	}
	if adapter.currentClient() == nil {
		t.Fatal("no client installed after concurrent connect")
	}
}

// TestPCRelayErrorCarriesCodeToPendingWaiter pins #1229: a relay:error
// reply must deliver the relay's code/message to the waiting caller, not a
// closed channel that reads as the fixed (and misleading) "disposed" text.
func TestPCRelayErrorCarriesCodeToPendingWaiter(t *testing.T) {
	c := newPCRelayClient("ws://unused", "test-provider")
	ch := make(chan *pcCreateResult, 1)
	c.mu.Lock()
	c.pendingCreates["req-1"] = ch
	c.mu.Unlock()

	data := `{"type":"relay:error","requestId":"req-1","code":"session_limit_exceeded","message":"too many sessions"}`
	if err := c.handleMessage([]byte(data)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	select {
	case r := <-ch:
		if r == nil || r.err == nil {
			t.Fatalf("expected error result, got %#v", r)
		}
		if !strings.Contains(r.err.Error(), "session_limit_exceeded") || !strings.Contains(r.err.Error(), "too many sessions") {
			t.Errorf("relay error must carry code+message, got: %v", r.err)
		}
	default:
		t.Fatal("no result delivered to pending waiter")
	}
	c.mu.Lock()
	_, stillPending := c.pendingCreates["req-1"]
	c.mu.Unlock()
	if stillPending {
		t.Error("pending entry must be removed after error delivery")
	}
}

// TestPCRelayErrorNoRequestIDBroadcasts pins #1229's amplifier: a relay
// error without requestId must fail ALL pending waiters instead of leaving
// them to the 30s timeout.
func TestPCRelayErrorNoRequestIDBroadcasts(t *testing.T) {
	c := newPCRelayClient("ws://unused", "test-provider")
	chC := make(chan *pcCreateResult, 1)
	chR := make(chan *pcRenewResult, 1)
	c.mu.Lock()
	c.pendingCreates["req-a"] = chC
	c.pendingRenewals["req-b"] = chR
	c.mu.Unlock()

	data := `{"type":"relay:error","code":"quota_exceeded","message":"provider quota exhausted"}`
	if err := c.handleMessage([]byte(data)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	select {
	case r := <-chC:
		if r == nil || r.err == nil || !strings.Contains(r.err.Error(), "quota_exceeded") {
			t.Errorf("create waiter must receive broadcast error, got %#v", r)
		}
	default:
		t.Fatal("create waiter starved")
	}
	select {
	case r := <-chR:
		if r == nil || r.err == nil || !strings.Contains(r.err.Error(), "quota_exceeded") {
			t.Errorf("renew waiter must receive broadcast error, got %#v", r)
		}
	default:
		t.Fatal("renew waiter starved")
	}
}
