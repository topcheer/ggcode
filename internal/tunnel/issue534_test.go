//go:build !integration

package tunnel

// Regression tests for issue #534 (tunnel batch):
//   - Bug B: enqueueControl/enqueueSnapshotEvent must stamp AuthorityEpoch
//     (epoch-0 events make the relay coerce to 1 and destroy room history).
//   - Bug A: handleRelayConnected server branch must not call blocking
//     providers synchronously (it runs on the readPump).
//   - Bug D: senderLoop must not signalSent when the relay Send fails
//     (StopSharingGracefully would false-ack and tear down the broker).
//   - Bug C: writePump graceful drain must flush pendingFront (stop_sharing
//     must not be silently discarded).

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

// recordingTransport is a thread-safe Transport recording every message
// the broker's senderLoop delivers — the stable observation point for
// outbound traffic (senderLoop owns b.outbound; with a nil session its
// batches are consumed-and-dropped, so peeking the queue races the loop).
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

// relayWouldClearHistory simulates ggcode-relay bindRoomSession epoch
// handling: an inbound event epoch of 0 is coerced to 1; any epoch that then
// differs from the room's epoch is treated as an authority change and the
// relay destroys the room's entire accumulated history (clearHistoryLocked).
func relayWouldClearHistory(roomEpoch, msgEpoch uint64) bool {
	e := msgEpoch
	if e == 0 {
		e = 1
	}
	return e != roomEpoch
}

// drainOutbound swaps out the broker's outbound queue for inspection.
func drainOutbound(b *Broker) []GatewayMessage {
	b.outMu.Lock()
	msgs := b.outbound
	b.outbound = nil
	b.outMu.Unlock()
	return msgs
}

// TestIssue534BugBControlAndSnapshotEventsCarryAuthorityEpoch verifies that
// messages built by enqueueControl and enqueueSnapshotEvent carry the
// broker's current AuthorityEpoch, so the relay never sees an epoch-0 event
// that would clear the room history (probe scenario from the issue: an
// epoch-0 server broadcast reduced relay history 4->1 and roomEpoch 5->1).
func TestIssue534BugBControlAndSnapshotEventsCarryAuthorityEpoch(t *testing.T) {
	b := NewBroker(nil)
	defer b.Stop()

	// Observe at the SEND side via a P2P transport: senderLoop consumes
	// b.outbound and (with a nil session) drops the batch after consumption,
	// so draining b.outbound directly races the consumer loop — that flaked
	// in CI as "expected 2 outbound messages, got 0" while passing locally.
	rec := &recordingTransport{}
	b.SetP2PTransport(rec)

	// Bump the generation so the broker epoch is not the coerced default 1.
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

	// Bounded wait for senderLoop to deliver both messages.
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

	// Document the failure mode: an unstamped (epoch-0) message — as built
	// before the fix — does trigger the history destruction.
	if !relayWouldClearHistory(roomEpoch, 0) {
		t.Fatal("expected epoch-0 message to trigger relay history clear (issue #534 probe scenario)")
	}
}

// TestIssue534BugAServerBranchDoesNotBlockOnProvider verifies the server
// branch of handleRelayConnected returns promptly even while the replay and
// snapshot providers are blocked. handleRelayConnected runs on the relay
// readPump; a synchronous provider call stalls reads/pongs until the relay's
// 75s read timeout kicks the connection, causing a reconnect storm.
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
		// Good: returned while providers were still blocked.
	case <-time.After(2 * time.Second):
		close(release) // unblock for cleanup
		t.Fatal("handleRelayConnected server branch blocked on provider call — stalls the readPump")
	}

	close(release)
	// Let the spawned recovery goroutine finish so the test leaves no
	// projection sync in flight.
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

// TestIssue534BugDSendFailureDoesNotSignalWaiter verifies that a failed relay
// Send does not signal the send waiter. StopSharingGracefully relies on the
// waiter to confirm sharing_stopped was delivered; signalling on failure
// makes the caller believe delivery succeeded and proceed to Stop(), making
// replay impossible.
func TestIssue534BugDSendFailureDoesNotSignalWaiter(t *testing.T) {
	sess := &Session{} // client == nil → Send always fails
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
		// Waiter still open: correct, the send failed and must not be
		// acknowledged as delivered.
	}
}

// TestIssue534BugCGracefulDrainFlushesPendingFront verifies that writePump's
// graceful-shutdown drain flushes pendingFront before closing, so a
// previously failed control write (e.g. stop_sharing) is retried instead of
// being permanently discarded.
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

	// Wait for the connection (and writePump) to come up.
	deadline := time.Now().Add(2 * time.Second)
	for rc.currentConn() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rc.currentConn() == nil {
		rc.Close()
		t.Fatal("timed out waiting for relay client connection")
	}

	// Simulate a control message whose write previously failed: it sits in
	// pendingFront waiting for retry.
	rc.pushPendingFront([]byte(`{"type":"stop_sharing"}`))

	rc.CloseGracefully(3 * time.Second)

	// The stop_sharing control message must reach the wire during the
	// graceful drain instead of being dropped.
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
