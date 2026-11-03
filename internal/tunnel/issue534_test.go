//go:build !integration

package tunnel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type recordingTransport struct {
	mu   sync.Mutex
	sent []GatewayMessage
}

func (r *recordingTransport) Send(data []byte) error {
	var msg GatewayMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}
	r.mu.Lock()
	r.sent = append(r.sent, msg)
	r.mu.Unlock()
	return nil
}
func (r *recordingTransport) OnMessage(func([]byte)) {}
func (r *recordingTransport) OnDisconnect(func())    {}
func (r *recordingTransport) Close() error           { return nil }
func (r *recordingTransport) IsConnected() bool      { return true }

func (r *recordingTransport) messages() []GatewayMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]GatewayMessage(nil), r.sent...)
}

func relayWouldClearHistory(roomEpoch, msgEpoch uint64) bool {
	e := msgEpoch
	if e == 0 {
		e = 1
	}
	return e != roomEpoch
}

func drainOutbound(b *Broker) []GatewayMessage {
	b.outMu.Lock()
	msgs := b.outbound
	b.outbound = nil
	b.outMu.Unlock()
	return msgs
}

func TestIssue534BugBControlAndSnapshotEventsCarryAuthorityEpoch(t *testing.T) {
	b := NewBroker(nil)
	defer b.Stop()

	rec := &recordingTransport{}
	b.SetP2PTransport(rec)

	b.resetSession()
	roomEpoch := b.AuthorityEpoch()
	if roomEpoch <= 1 {
		t.Fatalf("expected bumped authority epoch > 1, got %d", roomEpoch)
	}

	b.enqueueControl(EventSnapshotReset, nil)
	b.enqueueSnapshotEvent(SnapshotEvent{
		Type: EventText,
		Data: json.RawMessage(`{"id":"1","chunk":"hi"}`),
	})

	var msgs []GatewayMessage
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msgs = rec.messages(); len(msgs) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 outbound messages, got %d", len(msgs))
	}
	for i, msg := range msgs {
		if msg.AuthorityEpoch == 0 {
			t.Errorf("msg[%d] type=%s has zero AuthorityEpoch: relay coerces 0->1 and would clear room history", i, msg.Type)
		}
		if msg.AuthorityEpoch != roomEpoch {
			t.Errorf("msg[%d] type=%s AuthorityEpoch=%d, want broker epoch %d", i, msg.Type, msg.AuthorityEpoch, roomEpoch)
		}
		if relayWouldClearHistory(roomEpoch, msg.AuthorityEpoch) {
			t.Errorf("msg[%d] type=%s epoch=%d would trigger relay clearHistoryLocked at room epoch %d", i, msg.Type, msg.AuthorityEpoch, roomEpoch)
		}
	}

	if !relayWouldClearHistory(roomEpoch, 0) {
		t.Fatal("expected epoch-0 message to trigger relay history clear (issue #534 probe scenario)")
	}
}

func TestIssue534BugAServerBranchDoesNotBlockOnProvider(t *testing.T) {
	b := NewBroker(nil)
	defer b.Stop()

	release := make(chan struct{})
	b.snapshotMu.Lock()
	b.replayProvider = func() []GatewayMessage {
		<-release
		return nil
	}
	b.snapshotProvider = func() BrokerSnapshot {
		<-release
		return BrokerSnapshot{}
	}
	b.snapshotMu.Unlock()

	done := make(chan struct{})
	go func() {
		b.handleRelayConnected(RelayConnectedState{
			Role:         "server",
			SessionID:    "sess-other",
			HistoryCount: 3,
		})
		close(done)
	}()

	select {
	case <-done:

	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("handleRelayConnected server branch blocked on provider call — stalls the readPump")
	}

	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.projectionMu.Lock()
		idle := !b.projectionSyncing
		b.projectionMu.Unlock()
		if idle {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("recovery goroutine did not finish after providers were released")
}

func TestIssue534BugDSendFailureDoesNotSignalWaiter(t *testing.T) {
	sess := &Session{}
	b := NewBroker(sess)
	defer b.Stop()

	dataBytes, err := json.Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, wait := b.enqueueWithBytes("sharing_stopped", "", dataBytes, false, true)
	if msg.Type == "" || wait == nil {
		t.Fatalf("expected enqueued message with waiter, got %+v", msg)
	}

	select {
	case <-wait:
		t.Fatal("waiter signaled despite Send failure — false delivery ack (StopSharingGracefully would treat this as success)")
	case <-time.After(500 * time.Millisecond):

	}
}

func TestIssue534BugCGracefulDrainFlushesPendingFront(t *testing.T) {
	received := make(chan string, 8)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			select {
			case received <- string(data):
			default:
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	rc := testRelayClient(t, wsURL)
	if err := rc.Connect(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for rc.currentConn() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rc.currentConn() == nil {
		rc.Close()
		t.Fatal("timed out waiting for relay client connection")
	}

	rc.pushPendingFront([]byte(`{"type":"stop_sharing"}`))

	rc.CloseGracefully(3 * time.Second)

	gotStop := false
	timeout := time.After(3 * time.Second)
	for !gotStop {
		select {
		case raw := <-received:
			if strings.Contains(raw, "stop_sharing") {
				gotStop = true
			}
		case <-timeout:
			t.Fatal("graceful shutdown discarded pendingFront stop_sharing message")
		}
	}
}
