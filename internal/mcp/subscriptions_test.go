package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// Tests for the MCP 2026-07-28 subscriptions/listen client implementation
// (subscriptions.go). The fake server is the pipe-based NDJSON harness from
// issue994_test.go, extended with per-scenario behavior for the listen
// request: ack-then-graceful-close, legacy -32601 downgrade, and silent
// (cancel path) servers. The client-side read pump mirrors the real stdio
// read loop: readMessage + deliverResponse for responses, processNotification
// for notifications (which is where routeSubscriptionNotification runs).

// subFakeServer is a scripted NDJSON stdio server for subscription tests.
type subFakeServer struct {
	client *Client
	// seen receives every request/notification method the client writes,
	// in order (buffered; non-blocking).
	seen chan string
	// onListen handles a subscriptions/listen request. write marshals and
	// frames one JSON-RPC message to the client.
	onListen func(write func(obj interface{}), reqID json.RawMessage)

	reqRead    *os.File
	respW      *os.File
	cancelPump context.CancelFunc
	pumpDone   chan struct{}
	serverWG   sync.WaitGroup
	mu         sync.Mutex
	closed     bool
}

func newSubFakeServer(t *testing.T, onListen func(write func(obj interface{}), reqID json.RawMessage)) *subFakeServer {
	t.Helper()
	reqRead, reqWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("request pipe: %v", err)
	}
	respRead, respWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("response pipe: %v", err)
	}

	s := &subFakeServer{
		client:   NewClient("sub-fake", "/bin/cat", nil),
		seen:     make(chan string, 32),
		onListen: onListen,
		reqRead:  reqRead,
		respW:    respWrite,
		pumpDone: make(chan struct{}),
	}
	s.client.transport = "stdio"
	s.client.stdin = reqWrite
	s.client.reader = bufio.NewReader(respRead)

	// Server loop: consume client-written NDJSON lines and run the scenario.
	s.serverWG.Add(1)
	go func() {
		defer s.serverWG.Done()
		scanner := bufio.NewScanner(reqRead)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			select {
			case s.seen <- msg.Method:
			default: // don't block the client on a full channel
			}
			switch msg.Method {
			case MethodSubscriptionsListen:
				if s.onListen != nil {
					write := func(obj interface{}) {
						data, err := json.Marshal(obj)
						if err != nil {
							return
						}
						_, _ = respWrite.Write(append(data, '\n'))
					}
					s.onListen(write, msg.ID)
				}
			}
		}
	}()

	// Client-side read pump: mirrors the production stdio read loop
	// (responses → waiters, notifications → processNotification).
	pumpCtx, cancel := context.WithCancel(context.Background())
	s.cancelPump = cancel
	go func() {
		defer close(s.pumpDone)
		for {
			msg, err := s.client.readMessage(pumpCtx)
			if err != nil {
				return
			}
			switch m := msg.(type) {
			case *Notification:
				s.client.processNotification(m)
			case *Response:
				s.client.deliverResponse(m)
			}
		}
	}()

	t.Cleanup(s.Close)
	return s
}

// Close tears the fake server down without deadlocking on pipe reads.
func (s *subFakeServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancelPump()
	_ = s.respW.Close()
	_ = s.reqRead.Close()
	select {
	case <-s.pumpDone:
	case <-time.After(5 * time.Second):
	}
	s.serverWG.Wait()
}

func (s *subFakeServer) seenCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen) // approximation is fine; callers drain instead
}

func (s *subFakeServer) writeFromTest(obj interface{}) error {
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = s.respW.Write(append(data, '\n'))
	return err
}

// ackParams builds the acknowledged-notification params for reqID.
func ackParams(reqID json.RawMessage, agreed string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(
		`{"_meta":{"%s":%s},"notifications":%s}`,
		MetaKeySubscriptionID, reqID, agreed))
}

// TestSubscriptionListenStdioAckThenGraceful is the happy path: the server
// acknowledges the requested filter subset, delivers nothing else, then
// closes the stream gracefully. ListenSubscriptions must return only after
// the ack, the Done channel must close on the terminal response, Err must be
// nil, and the correlation registry must be empty afterwards.
func TestSubscriptionListenStdioAckThenGraceful(t *testing.T) {
	var reqID json.RawMessage
	s := newSubFakeServer(t, func(write func(interface{}), id json.RawMessage) {
		reqID = json.RawMessage(append([]byte(nil), id...))
		write(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  NotificationSubscriptionsAcknowledged,
			"params":  json.RawMessage(ackParams(id, `{"toolsListChanged":true}`)),
		})
		write(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  map[string]interface{}{"resultType": "complete"},
		})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	filter := SubscriptionFilter{ToolsListChanged: true, ResourcesListChanged: true}
	sub, err := s.client.ListenSubscriptions(ctx, filter)
	if err != nil {
		t.Fatalf("ListenSubscriptions: %v", err)
	}
	if !sub.acked.Load() {
		t.Error("subscription should be acked on return")
	}
	if !sub.Agreed().ToolsListChanged {
		t.Error("agreed filter should carry toolsListChanged")
	}

	select {
	case <-sub.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("subscription did not end after graceful server closure")
	}
	if err := sub.Err(); err != nil {
		t.Errorf("graceful closure should yield nil Err, got %v", err)
	}
	// Registry cleanup runs asynchronously (deliverResponse's spawned
	// goroutine), so poll instead of asserting immediately.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.client.subMu.Lock()
		n := len(s.client.subs)
		s.client.subMu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry should drain after stream end, still %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqID == nil {
		t.Error("server never received the listen request")
	}
}

// TestSubscriptionLegacyServerDowngrade pins the -32601 contract: the error
// surfaces to the caller, EnableModernSubscriptions reports false, and —
// critically — the unsupported state latches so a legacy server never
// receives a second subscriptions/listen.
func TestSubscriptionLegacyServerDowngrade(t *testing.T) {
	listens := 0
	var mu sync.Mutex
	s := newSubFakeServer(t, func(write func(interface{}), id json.RawMessage) {
		mu.Lock()
		listens++
		mu.Unlock()
		write(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]interface{}{"code": -32601, "message": "Method not found"},
		})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if s.client.EnableModernSubscriptions(ctx) {
		t.Fatal("legacy server must not be reported as supported")
	}
	mu.Lock()
	if listens != 1 {
		mu.Unlock()
		t.Fatalf("first attempt should hit the wire exactly once, saw %d", listens)
	}
	mu.Unlock()

	// Latched: no second wire attempt for the auto path.
	if s.client.EnableModernSubscriptions(ctx) {
		t.Fatal("downgrade must latch")
	}
	mu.Lock()
	got := listens
	mu.Unlock()
	if got != 1 {
		t.Errorf("latched downgrade should not re-call the server, saw %d", got)
	}
	if s.client.subListenState.Load() != subStateUnsupported {
		t.Error("subListenState should be unsupported after -32601")
	}

	// A direct ListenSubscriptions still surfaces the raw -32601 error.
	var rpcErr *Error
	_, err := s.client.ListenSubscriptions(ctx, SubscriptionFilter{ToolsListChanged: true})
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Errorf("expected -32601 error surface, got %v", err)
	}
}

// TestSubscriptionCancelSendsCancelledNotification covers the client-side
// MUST: cancelling (here via the ack-wait context deadline) sends
// notifications/cancelled with the listen request id and fully unwinds the
// subscription (registry cleanup, Done closed).
func TestSubscriptionCancelSendsCancelledNotification(t *testing.T) {
	s := newSubFakeServer(t, nil) // silent server: never acks

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := s.client.ListenSubscriptions(ctx, SubscriptionFilter{ToolsListChanged: true})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}

	// The cancelled notification must reach the wire.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-s.seen:
			if m == "notifications/cancelled" {
				s.client.subMu.Lock()
				n := len(s.client.subs)
				s.client.subMu.Unlock()
				if n != 0 {
					t.Errorf("registry should be empty after cancel, got %d", n)
				}
				return
			}
		case <-deadline:
			t.Fatal("notifications/cancelled never reached the server")
		}
	}
}

// TestRouteSubscriptionNotificationGating pins the MUST-gating matrix:
//   - ack control messages are consumed and mark the subscription acked;
//   - unknown/unacked subscription IDs are dropped (consumed);
//   - valid correlated traffic falls through to legacy dispatch;
//   - legacy traffic without _meta falls through untouched.
func TestRouteSubscriptionNotificationGating(t *testing.T) {
	c := NewClient("gating", "/bin/cat", nil)
	cancelCtx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	id := NewIntID(42)
	sub := &Subscription{
		client:     c,
		id:         &id,
		ackCh:      make(chan struct{}),
		done:       make(chan struct{}),
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
		requested:  SubscriptionFilter{ToolsListChanged: true},
	}
	c.addSubscription(subscriptionIDKey(sub.id), sub)

	// 1. Ack is consumed and acks the subscription.
	if !c.routeSubscriptionNotification(&Notification{
		Method: NotificationSubscriptionsAcknowledged,
		Params: ackParams(json.RawMessage(`42`), `{}`),
	}) {
		t.Error("ack must be consumed")
	}
	select {
	case <-sub.ackCh:
	default:
		t.Error("ack channel should be closed after ack")
	}
	select {
	case <-sub.done:
		t.Error("ack must not end the subscription")
	default:
	}

	// 2. Unknown subscription id → dropped.
	if !c.routeSubscriptionNotification(&Notification{
		Method: "notifications/tools/list_changed",
		Params: json.RawMessage(`{"_meta":{"` + MetaKeySubscriptionID + `":999}}`),
	}) {
		t.Error("unknown subscription id must be consumed (dropped)")
	}

	// 3. Valid correlated traffic → NOT consumed, forwarded to legacy path.
	if c.routeSubscriptionNotification(&Notification{
		Method: "notifications/tools/list_changed",
		Params: json.RawMessage(`{"_meta":{"` + MetaKeySubscriptionID + `":42}}`),
	}) {
		t.Error("valid correlated traffic must fall through to legacy dispatch")
	}

	// 4. Legacy traffic without _meta → untouched.
	if c.routeSubscriptionNotification(&Notification{
		Method: "notifications/message",
		Params: json.RawMessage(`{"level":"info","data":"hi"}`),
	}) {
		t.Error("legacy traffic must fall through")
	}

	// 5. String-form subscription ids normalize the same way.
	idStr := NewStringID("abc")
	subStr := &Subscription{
		client:     c,
		id:         &idStr,
		ackCh:      make(chan struct{}),
		done:       make(chan struct{}),
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}
	c.addSubscription(subscriptionIDKey(subStr.id), subStr)
	// Unacked subscription ⇒ MUST drop; ack it first, then expect fall-through.
	if !c.routeSubscriptionNotification(&Notification{
		Method: NotificationSubscriptionsAcknowledged,
		Params: ackParams(json.RawMessage(`"abc"`), `{}`),
	}) {
		t.Error("ack for string id must be consumed")
	}
	if c.routeSubscriptionNotification(&Notification{
		Method: "notifications/resources/updated",
		Params: json.RawMessage(`{"_meta":{"` + MetaKeySubscriptionID + `": "abc"},"uri":"file:///x"}`),
	}) {
		t.Error("string-id correlated traffic must fall through")
	}

	// 6. Malformed ack params are consumed, not forwarded.
	if !c.routeSubscriptionNotification(&Notification{
		Method: NotificationSubscriptionsAcknowledged,
		Params: json.RawMessage(`{"no":"meta"}`),
	}) {
		t.Error("malformed ack must be consumed")
	}
}

// TestSubscriptionFilterMissingFrom checks the ack-diff reporting logic.
func TestSubscriptionFilterMissingFrom(t *testing.T) {
	req := SubscriptionFilter{
		ToolsListChanged:      true,
		PromptsListChanged:    true,
		ResourceSubscriptions: []string{"file:///a", "file:///b"},
	}
	agreed := SubscriptionFilter{
		ToolsListChanged:      true,
		ResourceSubscriptions: []string{"file:///a"},
	}
	got := req.missingFrom(agreed)
	want := []string{"promptsListChanged", "resourceSubscriptions:file:///b"}
	if len(got) != len(want) {
		t.Fatalf("missingFrom = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("missingFrom[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if (SubscriptionFilter{}).missingFrom(agreed) != nil {
		t.Error("empty request should grant nothing missing")
	}
}

// TestSubscriptionEmptyFilterRejected guards the degenerate listen call.
func TestSubscriptionEmptyFilterRejected(t *testing.T) {
	c := NewClient("empty-filter", "/bin/cat", nil)
	if _, err := c.ListenSubscriptions(context.Background(), SubscriptionFilter{}); err == nil {
		t.Fatal("empty filter must be rejected client-side")
	}
}

// TestSubscriptionWSUnsupported documents the transport limitation.
func TestSubscriptionWSUnsupported(t *testing.T) {
	c := NewClient("ws-sub", "/bin/cat", nil)
	c.transport = "ws"
	_, err := c.ListenSubscriptions(context.Background(), SubscriptionFilter{ToolsListChanged: true})
	if err == nil || !isUnsupportedTransportErr(err) {
		t.Fatalf("ws transport must be rejected, got %v", err)
	}
}

func isUnsupportedTransportErr(err error) bool {
	type transportErr interface{ error }
	_ = transportErr(nil)
	return err != nil && containsStr(err.Error(), "unsupported on transport")
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestSubscriptionAbortClosesAll pins the teardown contract: Abort ends every
// open subscription with a terminal error so Wait-style consumers unblock
// even though the server never closed the stream.
func TestSubscriptionAbortClosesAll(t *testing.T) {
	var ackSend func(write func(interface{}), id json.RawMessage)
	s := newSubFakeServer(t, nil) // handler wired below (needs s)
	ackSend = func(write func(interface{}), id json.RawMessage) {
		// Ack but never close — the stream stays open until Abort.
		_ = s.writeFromTest(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  NotificationSubscriptionsAcknowledged,
			"params":  json.RawMessage(ackParams(id, `{}`)),
		})
	}
	s.onListen = ackSend

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sub, err := s.client.ListenSubscriptions(ctx, SubscriptionFilter{ToolsListChanged: true})
	if err != nil {
		t.Fatalf("ListenSubscriptions: %v", err)
	}
	s.client.Abort()
	select {
	case <-sub.Done():
		if sub.Err() == nil {
			t.Error("abort teardown should carry a terminal error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subscription did not end after Abort")
	}
}
