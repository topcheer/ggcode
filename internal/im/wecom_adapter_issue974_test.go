package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wecomIssue974Gateway runs an httptest server that upgrades to WebSocket and
// answers every outbound frame with an ack echoing its req_id, using the
// (errcode, errmsg) produced by respond. The adapter is wired to the server
// side directly (no dial), so Send() exercises the full
// writeAndAwaitAck -> dispatchPayload ack routing path deterministically.
func wecomIssue974Gateway(t *testing.T, respond func(cmd string, frame map[string]any) (int, string, bool)) (*wecomAdapter, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var frame map[string]any
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			cmd, _ := frame["cmd"].(string)
			if code, msg, ok := respond(cmd, frame); ok {
				headers, _ := frame["headers"].(map[string]any)
				reqID, _ := headers["req_id"].(string)
				ack := map[string]any{
					"cmd":     cmd,
					"headers": map[string]any{"req_id": reqID},
					"body":    map[string]any{"errcode": code, "errmsg": msg},
				}
				if err := conn.WriteJSON(ack); err != nil {
					return
				}
			}
		}
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial fake gateway: %v", err)
	}

	a := &wecomAdapter{
		name:        "test",
		seen:        map[string]time.Time{},
		replyReqIDs: map[string]string{},
		ackTimeout:  2 * time.Second,
	}
	a.mu.Lock()
	a.ws = clientConn
	a.connected = true
	a.mu.Unlock()

	// Mirror connectAndServe's read loop: feed every server frame into
	// dispatchPayload so ack routing is exercised exactly as in production.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			_, msgBytes, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			var payload map[string]any
			if json.Unmarshal(msgBytes, &payload) != nil {
				continue
			}
			a.dispatchPayload(context.Background(), payload)
		}
	}()

	cleanup := func() {
		clientConn.Close()
		srv.Close()
		select {
		case <-readerDone:
		case <-time.After(2 * time.Second):
		}
	}
	return a, cleanup
}

// TestWeComSendErrcodeRejected is the #974 medium fix: an async server
// rejection (here: 45009 rate limit) must surface as a Send error instead of
// being silently dropped after a successful WriteJSON.
func TestWeComSendErrcodeRejected(t *testing.T) {
	a, cleanup := wecomIssue974Gateway(t, func(cmd string, frame map[string]any) (int, string, bool) {
		if cmd == wecomCmdSend {
			return 45009, "api freq out of limit", true
		}
		return 0, "", false
	})
	defer cleanup()

	err := a.Send(context.Background(), ChannelBinding{ChannelID: "chat1"},
		OutboundEvent{Kind: OutboundEventText, Text: "hello"})
	if err == nil {
		t.Fatal("errcode 45009 rejection must surface as an error")
	}
	if !strings.Contains(err.Error(), "45009") {
		t.Fatalf("error should carry the errcode, got %v", err)
	}
}

// TestWeComSendAckSuccess verifies the happy path still succeeds: errcode 0
// ack resolves the pending waiter with nil.
func TestWeComSendAckSuccess(t *testing.T) {
	a, cleanup := wecomIssue974Gateway(t, func(cmd string, frame map[string]any) (int, string, bool) {
		if cmd == wecomCmdSend {
			return 0, "ok", true
		}
		return 0, "", false
	})
	defer cleanup()

	if err := a.Send(context.Background(), ChannelBinding{ChannelID: "chat1"},
		OutboundEvent{Kind: OutboundEventText, Text: "hello"}); err != nil {
		t.Fatalf("acked send must succeed, got %v", err)
	}
}

// TestWeComSendNoAckTimesOut: when the server never answers, the pending
// waiter must time out and return an error instead of blocking forever.
func TestWeComSendNoAckTimesOut(t *testing.T) {
	a, cleanup := wecomIssue974Gateway(t, func(string, map[string]any) (int, string, bool) {
		return 0, "", false // never ack
	})
	defer cleanup()
	a.mu.Lock()
	a.ackTimeout = 300 * time.Millisecond
	a.mu.Unlock()

	start := time.Now()
	err := a.Send(context.Background(), ChannelBinding{ChannelID: "chat1"},
		OutboundEvent{Kind: OutboundEventText, Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "no ack") {
		t.Fatalf("missing ack must time out with error, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout took too long: %v", time.Since(start))
	}
}

// TestWeComRespondFallsBackOnErrcode: a rejected respond_msg (reply quota
// exhausted) must fall back to send_msg, and the fallback's own ack is
// verified - the caller still gets delivery confirmation.
func TestWeComRespondFallsBackOnErrcode(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	a, cleanup := wecomIssue974Gateway(t, func(cmd string, frame map[string]any) (int, string, bool) {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		switch cmd {
		case wecomCmdRespond:
			return 45009, "reply quota exceeded", true
		case wecomCmdSend:
			return 0, "ok", true
		}
		return 0, "", false
	})
	defer cleanup()

	a.mu.Lock()
	a.replyReqIDs["msg-9"] = "req-inbound-1"
	a.mu.Unlock()

	err := a.Send(context.Background(),
		ChannelBinding{ChannelID: "chat1", LastInboundMessageID: "msg-9"},
		OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if err != nil {
		t.Fatalf("respond->proactive fallback must succeed, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cmds) != 2 || cmds[0] != wecomCmdRespond || cmds[1] != wecomCmdSend {
		t.Fatalf("expected [respond, send] sequence, got %v", cmds)
	}
}

// TestWeComRespondAckSuccess: an acked respond_msg completes without a
// proactive fallback.
func TestWeComRespondAckSuccess(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	a, cleanup := wecomIssue974Gateway(t, func(cmd string, frame map[string]any) (int, string, bool) {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		return 0, "ok", true
	})
	defer cleanup()

	a.mu.Lock()
	a.replyReqIDs["msg-1"] = "req-inbound-1"
	a.mu.Unlock()

	if err := a.Send(context.Background(),
		ChannelBinding{ChannelID: "chat1", LastInboundMessageID: "msg-1"},
		OutboundEvent{Kind: OutboundEventText, Text: "hi"}); err != nil {
		t.Fatalf("acked respond must succeed, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cmds) != 1 || cmds[0] != wecomCmdRespond {
		t.Fatalf("expected single respond frame, got %v", cmds)
	}
}

// TestWeComDispatchPayloadResolvesPendingAck verifies the ack-routing branch
// directly, including the errcode!=0 conversion.
func TestWeComDispatchPayloadResolvesPendingAck(t *testing.T) {
	a := &wecomAdapter{}
	ch := make(chan error, 1)
	a.pendingAcks.Store("send-1", ch)

	a.dispatchPayload(context.Background(), map[string]any{
		"cmd":     wecomCmdSend,
		"headers": map[string]any{"req_id": "send-1"},
		"body":    map[string]any{"errcode": 45009.0, "errmsg": "rate limited"},
	})

	select {
	case err := <-ch:
		if err == nil || !strings.Contains(err.Error(), "45009") {
			t.Fatalf("want errcode 45009 error, got %v", err)
		}
	default:
		t.Fatal("pending ack was not resolved")
	}
}

// TestWeComReplyReqIDOldestEviction drives handleMessage with more inbound
// msgids than wecomDedupMaxSize: eviction must drop the OLDEST req_id (the
// random map-entry eviction could drop exactly the req_id a reply needs).
func TestWeComReplyReqIDOldestEviction(t *testing.T) {
	a := &wecomAdapter{name: "t", seen: map[string]time.Time{}, replyReqIDs: map[string]string{}}

	for i := 0; i < wecomDedupMaxSize+5; i++ {
		a.handleMessage(context.Background(), map[string]any{
			"cmd":     wecomCmdCallback,
			"headers": map[string]any{"req_id": fmt.Sprintf("req-%d", i)},
			"body": map[string]any{
				"msgid":    fmt.Sprintf("msg-%d", i),
				"chatid":   fmt.Sprintf("chat-%d", i),
				"chattype": "single",
				"msgtype":  "text",
				"text":     map[string]any{"content": "x"},
				"from":     map[string]any{"userid": "u1"},
			},
		})
	}

	if len(a.replyReqIDs) > wecomDedupMaxSize {
		t.Fatalf("replyReqIDs size %d exceeds cap %d", len(a.replyReqIDs), wecomDedupMaxSize)
	}
	if _, ok := a.replyReqIDs["msg-0"]; ok {
		t.Fatal("oldest entry msg-0 must be evicted first")
	}
	if _, ok := a.replyReqIDs[fmt.Sprintf("msg-%d", wecomDedupMaxSize+4)]; !ok {
		t.Fatal("newest entry must survive eviction")
	}
}

// TestWeComReqIDUniqueness: req_ids key the pending-ack table, so two sends
// in the same nanosecond must still produce distinct ids.
func TestWeComReqIDUniqueness(t *testing.T) {
	const n = 1000
	ids := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id := newWeComReqID("send")
		if ids[id] {
			t.Fatalf("duplicate req_id generated: %s", id)
		}
		ids[id] = true
	}
}
