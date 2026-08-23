package im

// Regression tests for GitHub issues #946, #948, #949 (dingtalk_adapter.go).
//
//	#946: reconnect backoff attempt counter must be written back (no := shadow)
//	#948: callback context (webhook/robotCode/convID/msgID) must be keyed by
//	      binding.ChannelID (staffId) so unbound users cannot hijack delivery
//	#949: bot-callback processing must not block the WS read goroutine - the
//	      ACK is sent before processBotCallback runs asynchronously

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

// ---- #946: backoffFor write-back semantics ----

func TestDingtalkBackoffForWriteBackSemantics(t *testing.T) {
	backoffs := []time.Duration{3 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}

	// Simulate the run() loop contract: the caller assigns the returned
	// counter back (delay, attempt = backoffFor(err, attempt, backoffs)).
	// If the caller shadows with := (pre-#946 bug), attempt stays 0 forever
	// and every delay is backoffs[0].
	err := fmt.Errorf("connection refused")
	attempt := 0
	var gotDelays []time.Duration
	for i := 0; i < 5; i++ {
		var delay time.Duration
		delay, attempt = backoffFor(err, attempt, backoffs) // write-back (must be =)
		gotDelays = append(gotDelays, delay)
	}
	wantDelays := []time.Duration{3 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 30 * time.Second}
	for i := range wantDelays {
		if gotDelays[i] != wantDelays[i] {
			t.Fatalf("delay[%d] = %v, want %v (full sequence: %v)", i, gotDelays[i], wantDelays[i], gotDelays)
		}
	}
	if attempt != 5 {
		t.Errorf("attempt after 5 failures = %d, want 5", attempt)
	}

	// A clean disconnect (nil err) resets the counter so the next first
	// retry uses the short delay (#389 parity).
	delay, attempt := backoffFor(nil, attempt, backoffs)
	if delay != backoffs[0] || attempt != 1 {
		t.Errorf("after clean disconnect: delay=%v attempt=%d, want %v and 1", delay, attempt, backoffs[0])
	}
}

// ---- #948: per-user callback context keyed by binding.ChannelID ----

func TestDingtalkCallbackContextKeyedByUser(t *testing.T) {
	a := &dingtalkAdapter{name: "test"}

	a.recordCallbackContext("staffA", dingtalkCallbackContext{
		webhook:   "https://example.com/hook-a",
		robotCode: "robotA",
		convID:    "convA",
		msgID:     "msgA",
	})
	a.recordCallbackContext("staffB", dingtalkCallbackContext{
		webhook:   "https://example.com/hook-b",
		robotCode: "robotB",
		convID:    "convB",
		msgID:     "msgB",
	})

	gotA, ok := a.callbackContext("staffA")
	if !ok || gotA.webhook != "https://example.com/hook-a" || gotA.robotCode != "robotA" ||
		gotA.convID != "convA" || gotA.msgID != "msgA" {
		t.Fatalf("callbackContext(staffA) = %+v ok=%v, want A's context", gotA, ok)
	}
	gotB, ok := a.callbackContext("staffB")
	if !ok || gotB.webhook != "https://example.com/hook-b" {
		t.Fatalf("callbackContext(staffB) = %+v ok=%v, want B's context (no cross-user overwrite)", gotB, ok)
	}

	// Unknown / unbound user must be a cache miss - Send falls back to the
	// API path instead of reusing another user's webhook.
	if _, ok := a.callbackContext("staffC"); ok {
		t.Fatal("callbackContext(staffC) should be a miss for a user with no callback")
	}

	// Empty channelID must never be recorded (would corrupt lookups).
	a.recordCallbackContext("", dingtalkCallbackContext{webhook: "x"})
	if _, ok := a.callbackContext(""); ok {
		t.Fatal("empty channelID must not be recorded")
	}
}

// TestDingtalkSendAddressesBindingUserWebhook verifies the #948 attack
// scenario end-to-end at the Send() level: user B (unbound) sends a callback
// AFTER user A's, then the agent replies to A - delivery must go to A's
// webhook only, never B's.
func TestDingtalkSendAddressesBindingUserWebhook(t *testing.T) {
	var muA, muB sync.Mutex
	hitsA, hitsB := 0, 0
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		muA.Lock()
		hitsA++
		muA.Unlock()
		w.Write([]byte(`{"errcode":0}`))
	}))
	defer serverA.Close()
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		muB.Lock()
		hitsB++
		muB.Unlock()
		w.Write([]byte(`{"errcode":0}`))
	}))
	defer serverB.Close()

	a := &dingtalkAdapter{name: "test", httpClient: serverA.Client()}
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()

	// A's callback arrives first, then B's (the hijack attempt).
	a.recordCallbackContext("staffA", dingtalkCallbackContext{webhook: serverA.URL, robotCode: "robotA"})
	a.recordCallbackContext("staffB", dingtalkCallbackContext{webhook: serverB.URL, robotCode: "robotB"})

	err := a.Send(context.Background(), ChannelBinding{ChannelID: "staffA"}, OutboundEvent{Kind: OutboundEventText, Text: "reply for A"})
	if err != nil {
		t.Fatalf("Send to staffA: %v", err)
	}

	muA.Lock()
	aHits := hitsA
	muA.Unlock()
	muB.Lock()
	bHits := hitsB
	muB.Unlock()
	if aHits != 1 {
		t.Fatalf("webhook hits for A = %d, want 1", aHits)
	}
	if bHits != 0 {
		t.Fatalf("webhook hits for B = %d, want 0 - reply leaked to another user's webhook (#948)", bHits)
	}

	// Cache miss (user C) must NOT reuse the latest recorded webhook (B's);
	// it must fall through to the API path, which fails here with no token.
	err = a.Send(context.Background(), ChannelBinding{ChannelID: "staffC"}, OutboundEvent{Kind: OutboundEventText, Text: "reply for C"})
	if err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Fatalf("Send to uncached staffC should hit API fallback (fails with 'no access token'), got: %v", err)
	}
	muB.Lock()
	bHits = hitsB
	muB.Unlock()
	if bHits != 0 {
		t.Fatalf("webhook hits for B after staffC send = %d, want 0", bHits)
	}
}

// TestDingtalkTriggerTypingUsesBindingContext verifies reactions (#948
// secondary): the reaction target is looked up per binding user.
func TestDingtalkTriggerTypingUsesBindingContext(t *testing.T) {
	a := &dingtalkAdapter{
		name:        "test",
		accessToken: "fake-token",
		reactedMsgs: make(map[string]bool),
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
	a.recordCallbackContext("staffB", dingtalkCallbackContext{convID: "convB", msgID: "midB"})

	// Binding for a user with no cached context must be a silent no-op
	// (previously it reacted to whatever the global lastConvID/lastMsgID
	// pointed at - possibly another user's message).
	if err := a.TriggerTyping(context.Background(), ChannelBinding{ChannelID: "staffA"}); err != nil {
		t.Fatalf("TriggerTyping for uncached staffA: %v", err)
	}
	if len(a.reactedMsgs) != 0 {
		t.Fatalf("reactedMsgs = %v after uncached user, want empty (no cross-user reaction)", a.reactedMsgs)
	}

	// Binding for B reacts to B's own message.
	if err := a.TriggerTyping(context.Background(), ChannelBinding{ChannelID: "staffB"}); err != nil {
		t.Fatalf("TriggerTyping for staffB: %v", err)
	}
	if !a.reactedMsgs["midB"] {
		t.Fatal("expected midB to be marked as reacted for staffB")
	}
}

// ---- #949: ACK before (asynchronous) callback processing ----

// TestDingtalkBotCallbackACKNotBlockedBySlowProcessing builds the exact
// failure shape from #949: an inbound callback whose processing path blocks
// (denied-user webhook POST to a hanging server). handleDataFrame must send
// the DataFrame ACK and return immediately instead of blocking the WS read
// goroutine for the duration of processing.
func TestDingtalkBotCallbackACKNotBlockedBySlowProcessing(t *testing.T) {
	// Hanging webhook server: blocks the denied-user reply until released.
	// Use a completion channel (not a bare WaitGroup) so cleanup cannot hang
	// if the async POST races ahead of server shutdown.
	hangReleased := make(chan struct{})
	hangDone := make(chan struct{})
	hangServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(hangDone)
		<-hangReleased
		w.Write([]byte(`{"errcode":0}`))
	}))
	defer hangServer.Close()
	defer close(hangReleased)

	// Manager with an active binding for staffA only - staffB's message is
	// denied, and the denial reply POSTs to the hanging webhook.
	bridge := &noopBridge949{}
	mgr := NewManager()
	mgr.bridge = bridge
	mgr.BindSession(SessionBinding{Workspace: "ws", SessionID: "sess-1"})
	mgr.currentBindings["test"] = &ChannelBinding{
		Workspace: "ws",
		Adapter:   "test",
		ChannelID: "staffA",
	}

	a := &dingtalkAdapter{name: "test", manager: mgr, httpClient: hangServer.Client()}

	// Real WS connection so sendFrameResponse writes a real ACK frame.
	upgrader := websocket.Upgrader{}
	ackRead := make(chan string, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			ackRead <- "read-error: " + err.Error()
			return
		}
		ackRead <- string(msg)
	}))
	defer wsServer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial test ws server: %v", err)
	}
	defer conn.Close()
	a.mu.Lock()
	a.ws = conn
	a.connected = true
	a.mu.Unlock()

	callbackJSON, _ := json.Marshal(map[string]any{
		"conversationId": "cidB",
		"senderStaffId":  "staffB",
		"msgId":          "msgB-1",
		"text":           map[string]string{"content": "hijack attempt"},
		"sessionWebhook": hangServer.URL,
		"robotCode":      "robotB",
	})
	frame := dingtalkDataFrame{
		SpecVersion: "1.0",
		Type:        dingtalkSubCallback,
		Headers: map[string]string{
			dfHeaderTopic:     dingtalkBotCallbackTopic,
			dfHeaderMessageID: "frame-b-1",
		},
		Data: string(callbackJSON),
	}
	frameBytes, _ := json.Marshal(frame)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// handleDataFrame must return promptly even though the callback's
	// processing blocks on the hanging webhook (the #949 failure mode: a
	// long agent run inside the read goroutine trips wsReadTimeout).
	done := make(chan struct{})
	go func() {
		a.handleDataFrame(ctx, conn, frameBytes)
		close(done)
	}()

	select {
	case <-done:
		// returned without waiting for the hanging webhook - good
	case <-time.After(3 * time.Second):
		t.Fatal("handleDataFrame blocked on callback processing - read goroutine would starve (#949)")
	}

	// The ACK frame must still be delivered.
	select {
	case ack := <-ackRead:
		var resp dingtalkDataFrameResponse
		if err := json.Unmarshal([]byte(ack), &resp); err != nil {
			t.Fatalf("ACK frame not valid JSON: %v (%q)", err, ack)
		}
		if resp.Code != dfStatusOK {
			t.Fatalf("ACK code = %d, want %d", resp.Code, dfStatusOK)
		}
		if resp.Headers[dfHeaderMessageID] != "frame-b-1" {
			t.Fatalf("ACK messageId = %q, want frame-b-1", resp.Headers[dfHeaderMessageID])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no ACK received - bot callback must be ACKed immediately")
	}
}

type noopBridge949 struct{}

func (b *noopBridge949) SubmitInboundMessage(_ context.Context, _ InboundMessage) error {
	return nil
}
