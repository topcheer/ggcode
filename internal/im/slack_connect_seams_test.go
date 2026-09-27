package im

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Pin tests for the r191 slack connectAndServe seams. The orchestrator was
// flattened into: slackDialSocketMode / teardownSocketConnection /
// slackArmReadDeadline / markSocketConnected / startPingLoop+runPingLoop /
// readEnvelopeLoop / slackWriteEnvelopeAck / dispatchEnvelope
// (+ pure extractors slackEventFromEnvelope, slackInteractivePayload,
// slackHandlesEventType). Behavior-preservation proof = these pins + all
// pre-existing slack tests passing unchanged.

// newTestWSPair dials a real websocket pair through an in-process httptest
// server: client is the adapter-side conn, server is the peer the test drives.
func newTestWSPair(t *testing.T) (client, server *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverCh <- c
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case server = <-serverCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server-side websocket")
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client, server
}

func TestSlackEventFromEnvelope(t *testing.T) {
	event := map[string]any{"type": "message", "text": "hi"}
	cases := []struct {
		name     string
		envelope map[string]any
		want     map[string]any
	}{
		{"missing payload", map[string]any{"type": "events_api"}, nil},
		{"payload not a map", map[string]any{"payload": "nope"}, nil},
		{"missing event", map[string]any{"payload": map[string]any{"envelope_id": "x"}}, nil},
		{"event not a map", map[string]any{"payload": map[string]any{"event": 7}}, nil},
		{"ok", map[string]any{"payload": map[string]any{"event": event}}, event},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slackEventFromEnvelope(tc.envelope)
			if len(got) != len(tc.want) {
				t.Fatalf("slackEventFromEnvelope = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("slackEventFromEnvelope[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestSlackInteractivePayload(t *testing.T) {
	if got := slackInteractivePayload(map[string]any{"type": "interactive"}); got != nil {
		t.Fatalf("missing payload: got %v, want nil", got)
	}
	payload := map[string]any{"type": "other"}
	got := slackInteractivePayload(map[string]any{"payload": payload})
	if got["type"] != "other" {
		t.Fatalf("payload extraction: got %v", got)
	}
}

func TestSlackHandlesEventType(t *testing.T) {
	for eventType, want := range map[string]bool{
		"message":     true,
		"app_mention": true,
		"file_share":  false,
		"":            false,
	} {
		event := map[string]any{}
		if eventType != "" {
			event["type"] = eventType
		}
		if got := slackHandlesEventType(event); got != want {
			t.Fatalf("slackHandlesEventType(%q) = %v, want %v", eventType, got, want)
		}
	}
}

func TestSlackDialSocketMode_WrapsDialError(t *testing.T) {
	// Reserve a port then release it so the dial gets an immediate refusal.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	_, err = slackDialSocketMode(context.Background(), "ws://"+addr+"/ws")
	if err == nil {
		t.Fatal("expected dial error for closed port")
	}
	if !strings.Contains(err.Error(), "dial socket mode:") {
		t.Fatalf("error = %v, want wrapped with %q prefix", err, "dial socket mode:")
	}
}

func TestSlackSocketConnectedTeardownState(t *testing.T) {
	client, _ := newTestWSPair(t)
	a := &slackAdapter{name: "t"}

	a.markSocketConnected(client)
	a.mu.RLock()
	ws, connected := a.ws, a.connected
	a.mu.RUnlock()
	if ws != client || !connected {
		t.Fatalf("after markSocketConnected: ws=%v connected=%v", ws, connected)
	}

	a.teardownSocketConnection(client)
	a.mu.RLock()
	ws, connected = a.ws, a.connected
	a.mu.RUnlock()
	if ws != nil || connected {
		t.Fatalf("after teardownSocketConnection: ws=%v connected=%v", ws, connected)
	}
}

func TestSlackWriteEnvelopeAck(t *testing.T) {
	client, server := newTestWSPair(t)

	slackWriteEnvelopeAck(client, map[string]any{"type": "events_api", "envelope_id": "e-123"})
	_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("ack frame type = %v, want TextMessage", mt)
	}
	var ack map[string]any
	if err := json.Unmarshal(data, &ack); err != nil {
		t.Fatalf("ack not JSON: %v (%s)", err, data)
	}
	if ack["envelope_id"] != "e-123" {
		t.Fatalf("ack envelope_id = %v, want e-123", ack["envelope_id"])
	}

	// Envelope without envelope_id produces no frame.
	slackWriteEnvelopeAck(client, map[string]any{"type": "events_api"})
	_ = server.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := server.ReadMessage(); err == nil {
		t.Fatal("expected no ack frame for envelope without envelope_id")
	}
}

func TestSlackReadEnvelopeLoop_AckSurvivesMalformedCtxCancel(t *testing.T) {
	client, server := newTestWSPair(t)
	a := &slackAdapter{name: "t"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopDone := make(chan error, 1)
	go func() { loopDone <- a.readEnvelopeLoop(ctx, client) }()

	_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
	env1, _ := json.Marshal(map[string]any{"type": "events_api", "envelope_id": "e-1"})
	if err := server.WriteMessage(websocket.TextMessage, env1); err != nil {
		t.Fatalf("send env1: %v", err)
	}
	mt, data, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("read ack1: %v", err)
	}
	if mt != websocket.TextMessage || !strings.Contains(string(data), "e-1") {
		t.Fatalf("ack1 = (%v, %s), want TextMessage with e-1", mt, data)
	}

	// Malformed JSON frame is skipped without killing the loop (#968 era
	// behavior: continue, stay connected).
	if err := server.WriteMessage(websocket.TextMessage, []byte("not-json")); err != nil {
		t.Fatalf("send malformed: %v", err)
	}
	env2, _ := json.Marshal(map[string]any{"type": "interactive", "envelope_id": "e-2"})
	if err := server.WriteMessage(websocket.TextMessage, env2); err != nil {
		t.Fatalf("send env2: %v", err)
	}
	_, data, err = server.ReadMessage()
	if err != nil {
		t.Fatalf("read ack2 after malformed frame: %v", err)
	}
	if !strings.Contains(string(data), "e-2") {
		t.Fatalf("ack2 = %s, want e-2", data)
	}

	// ctx cancel returns nil once the blocked read wakes on the next frame.
	cancel()
	if err := server.WriteMessage(websocket.TextMessage, []byte("wake")); err != nil {
		t.Fatalf("send wake: %v", err)
	}
	select {
	case got := <-loopDone:
		if got != nil {
			t.Fatalf("readEnvelopeLoop after cancel = %v, want nil", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readEnvelopeLoop did not return after ctx cancel")
	}
}

func TestSlackArmReadDeadline_PingKeepsConnReadable(t *testing.T) {
	client, server := newTestWSPair(t)
	slackArmReadDeadline(client)

	// Exercise the pong handler: peer pings, gorilla auto-replies pong and
	// our handler refreshes the deadline.
	if err := server.WriteControl(websocket.PingMessage, []byte("hb"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("send ping: %v", err)
	}
	if err := server.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("conn unreadable after slackArmReadDeadline+ping: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("read %q, want hello", data)
	}
}

func TestSlackDispatchEnvelope_NoPanicWithNilManager(t *testing.T) {
	a := &slackAdapter{name: "t"}
	ctx := context.Background()
	// events_api message event reaches handleMessage, which guards nil manager.
	a.dispatchEnvelope(ctx, map[string]any{"type": "events_api", "payload": map[string]any{
		"event": map[string]any{"type": "message", "channel": "c", "ts": "1", "text": "hi"},
	}})
	// non-dispatchable event type is ignored.
	a.dispatchEnvelope(ctx, map[string]any{"type": "events_api", "payload": map[string]any{
		"event": map[string]any{"type": "file_share"},
	}})
	// interactive with no payload is ignored.
	a.dispatchEnvelope(ctx, map[string]any{"type": "interactive"})
	// unknown type ignored.
	a.dispatchEnvelope(ctx, map[string]any{"type": "hello"})

	// Explicit assertion: dispatching must not mutate adapter connection state.
	a.mu.RLock()
	ws, connected := a.ws, a.connected
	a.mu.RUnlock()
	if ws != nil || connected {
		t.Fatalf("dispatchEnvelope mutated adapter state: ws=%v connected=%v", ws, connected)
	}
}
