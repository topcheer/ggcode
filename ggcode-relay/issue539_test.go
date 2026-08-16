package main

// Feature tests for issue #539 (relay server batch):
//   A — old-server deferred detach must not evict a newer server
//   B — deposed server broadcast/active_session must be rejected
//   C — sendRaw must not block 2s per full-buffer slow consumer
//   E — onAck must read room.sessionID under room.mu (race-free)

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Bug A: stale server detach ───

// TestDetachFromRoomStaleServerDetachKeepsNewServer reproduces the probe
// scenario: a new server takes over, then the deposed old server's deferred
// detach fires. The old detach must NOT clear room.server/serverReady.
func TestDetachFromRoomStaleServerDetachKeepsNewServer(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token-issue539-a")

	oldServer := newPeer(h, r, "server", nil)
	newServer := newPeer(h, r, "server", nil)

	// Takeover: handleWS-style — new server replaces old.
	r.mu.Lock()
	r.server = newServer
	r.serverReady = true
	r.mu.Unlock()

	// Deferred detach from the deposed old server arrives late.
	oldServer.detachFromRoom(false, h)

	r.mu.RLock()
	serverAfter := r.server
	readyAfter := r.serverReady
	r.mu.RUnlock()

	if serverAfter != newServer {
		t.Fatalf("old server detach must not evict new server: server==nil? %t", serverAfter == nil)
	}
	if !readyAfter {
		t.Fatal("old server detach must not clear serverReady for the new server")
	}
}

// TestDetachFromRoomCurrentServerDetachStillClears guards the fix: a genuine
// current-server detach must still clear server/serverReady and schedule
// room expiry (recovery path).
func TestDetachFromRoomCurrentServerDetachStillClears(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token-issue539-a2")

	server := newPeer(h, r, "server", nil)
	r.mu.Lock()
	r.server = server
	r.serverReady = true
	r.mu.Unlock()

	server.detachFromRoom(false, h)

	r.mu.RLock()
	serverAfter := r.server
	readyAfter := r.serverReady
	retained := r.offlineTimer != nil
	r.mu.RUnlock()

	if serverAfter != nil {
		t.Fatal("current server detach must clear room.server")
	}
	if readyAfter {
		t.Fatal("current server detach must clear serverReady")
	}
	if !retained {
		t.Fatal("current server detach must schedule room expiry (offline timer armed)")
	}
}

// ─── Bug B: deposed server broadcast ───

// TestHandleServerBroadcastRejectedFromDeposedServer reproduces the probe:
// S2 takes over and binds sess-new; deposed-but-open S1 sends a broadcast
// carrying sess-old with a foreign epoch and replace semantics — it must be
// rejected without touching sessionID, epoch, or history.
func TestHandleServerBroadcastRejectedFromDeposedServer(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token-issue539-b")

	oldServer := newPeer(h, r, "server", nil)
	oldServer.clientID = "old-server"
	newServer := newPeer(h, r, "server", nil)
	newServer.clientID = "new-server"
	client := newPeer(h, r, "client", nil)
	client.ready = true
	r.mu.Lock()
	r.server = newServer
	r.clients[client] = struct{}{}
	r.sessionID = "sess-new"
	r.authorityEpoch = 2
	r.history = []roomEvent{{sessionID: "sess-new", eventID: "ev-000000001", typ: "encrypted"}}
	r.mu.Unlock()

	// Deposed server's residual frame.
	oldServer.handleServerBroadcast(nil, relayMessage{
		Type:           "encrypted",
		SessionID:      "sess-old",
		EventID:        "ev-000000999",
		AuthorityEpoch: 99,
	})

	r.mu.RLock()
	sid := r.sessionID
	epoch := r.authorityEpoch
	hist := len(r.history)
	r.mu.RUnlock()

	if sid != "sess-new" {
		t.Fatalf("deposed server rewrote sessionID: got %q want sess-new", sid)
	}
	if epoch != 2 {
		t.Fatalf("deposed server rewrote authorityEpoch: got %d want 2", epoch)
	}
	if hist != 1 {
		t.Fatalf("deposed server mutated history: len=%d want 1", hist)
	}
	// Client must not have received the stale frame.
	select {
	case raw := <-client.sendCh:
		t.Fatalf("stale server frame must not reach clients, got %s", raw)
	default:
	}
}

// TestHandleServerBroadcastAcceptedFromCurrentServer guards the fix: the
// authoritative server's broadcast still flows through.
func TestHandleServerBroadcastAcceptedFromCurrentServer(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token-issue539-b2")

	server := newPeer(h, r, "server", nil)
	server.clientID = "the-server"
	client := newPeer(h, r, "client", nil)
	client.ready = true
	r.mu.Lock()
	r.server = server
	r.clients[client] = struct{}{}
	r.sessionID = "sess-1"
	r.mu.Unlock()

	server.handleServerBroadcast(nil, relayMessage{
		Type:      "encrypted",
		SessionID: "sess-1",
		EventID:   "ev-000000002",
	})

	r.mu.RLock()
	hist := len(r.history)
	r.mu.RUnlock()
	if hist != 1 {
		t.Fatalf("current server broadcast should append history, got %d", hist)
	}
	select {
	case raw := <-client.sendCh:
		var msg relayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.EventID != "ev-000000002" {
			t.Fatalf("client should receive broadcast, got %+v", msg)
		}
	default:
		t.Fatal("client should receive current server broadcast")
	}
}

// TestOnActiveSessionRejectedFromDeposedServer covers the active_session path
// of Bug B: a deposed server's late active_session(replace_history, epoch=99)
// must not destroy the new server's session.
func TestOnActiveSessionRejectedFromDeposedServer(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token-issue539-b3")

	oldServer := newPeer(h, r, "server", nil)
	oldServer.clientID = "old-server"
	newServer := newPeer(h, r, "server", nil)
	newServer.clientID = "new-server"
	client := newPeer(h, r, "client", nil)
	client.ready = true
	r.mu.Lock()
	r.server = newServer
	r.clients[client] = struct{}{}
	r.sessionID = "sess-new"
	r.authorityEpoch = 2
	r.history = []roomEvent{{sessionID: "sess-new", eventID: "ev-000000001", typ: "encrypted"}}
	r.mu.Unlock()

	oldServer.onActiveSession(relayMessage{
		Type:           "active_session",
		SessionID:      "sess-old",
		AuthorityEpoch: 99,
		ResumeMode:     activeSessionModeReplace,
		Data:           mustJSON(map[string]any{"session_id": "sess-old"}),
	})

	r.mu.RLock()
	sid := r.sessionID
	epoch := r.authorityEpoch
	hist := len(r.history)
	r.mu.RUnlock()

	if sid != "sess-new" {
		t.Fatalf("deposed server active_session rewrote sessionID: got %q", sid)
	}
	if epoch != 2 {
		t.Fatalf("deposed server active_session rewrote epoch: got %d", epoch)
	}
	if hist != 1 {
		t.Fatalf("deposed server active_session cleared history: len=%d", hist)
	}
}

// ─── Bug C: sendRaw non-blocking on full buffer ───

// TestSendRawDoesNotBlockOnFullBuffer proves the fix: a full sendCh no longer
// stalls the caller for 2s — sendRaw returns promptly, counts the drop, and
// (with a nil conn) simply skips the close.
func TestSendRawDoesNotBlockOnFullBuffer(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token-issue539-c")
	p := newPeer(h, r, "client", nil)
	p.clientID = "slow-client"

	// Fill the 512-slot buffer.
	for i := 0; i < cap(p.sendCh); i++ {
		p.sendCh <- []byte("x")
	}

	done := make(chan struct{})
	go func() {
		p.sendRaw([]byte("overflow")) // must not block 2s
		close(done)
	}()

	select {
	case <-done:
		// returned promptly
	case <-time.After(500 * time.Millisecond):
		t.Fatal("sendRaw blocked on full buffer (slow-consumer stall regression)")
	}

	if got := atomic.LoadUint64(&h.stats.droppedSends); got != 1 {
		t.Fatalf("expected droppedSends=1, got %d", got)
	}
}

// TestSendRawFullBufferSerialFanoutNoStall reproduces the probe shape: three
// full-buffer slow clients must not cost 3×2s when a server broadcasts while
// holding sendMu.
func TestSendRawFullBufferSerialFanoutNoStall(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token-issue539-c2")
	server := newPeer(h, r, "server", nil)
	r.mu.Lock()
	r.server = server
	r.mu.Unlock()

	var slow []*peer
	for i := 0; i < 3; i++ {
		c := newPeer(h, r, "client", nil)
		c.ready = true
		for j := 0; j < cap(c.sendCh); j++ {
			c.sendCh <- []byte("x")
		}
		r.mu.Lock()
		r.clients[c] = struct{}{}
		r.mu.Unlock()
		slow = append(slow, c)
	}

	start := time.Now()
	server.handleServerBroadcast(nil, relayMessage{
		Type:      "encrypted",
		SessionID: "sess-1",
		EventID:   "ev-000000001",
	})
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("fanout to 3 full-buffer clients took %v; serial 2s×N stall is back", elapsed)
	}
	if got := atomic.LoadUint64(&h.stats.droppedSends); got != uint64(len(slow)) {
		t.Fatalf("expected droppedSends=%d, got %d", len(slow), got)
	}
}

// ─── Bug E: onAck sessionID data race ───

// TestOnAckConcurrentWithBindRoomSessionRace exercises onAck concurrently
// with bindRoomSession (which writes room.sessionID under room.mu). Run with
// -race: the pre-fix unsynchronized read of room.sessionID in onAck trips
// the race detector; the fix reads it under the lock, so this test must pass
// cleanly.
func TestOnAckConcurrentWithBindRoomSessionRace(t *testing.T) {
	// A real store is required: the racy read of room.sessionID sits inside
	// `if h.store != nil` in onAck — with a nil store the code path never runs.
	h := newHub(newStoreForTest(t))
	r := h.getOrCreateRoom("token-issue539-e")
	client := newPeer(h, r, "client", nil)
	client.clientID = "client-1"
	client.ready = true
	r.mu.Lock()
	r.clients[client] = struct{}{}
	r.mu.Unlock()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: keeps rebinding the room session (writes room.sessionID).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			sid := "sess-writer"
			if i%2 == 0 {
				sid = "sess-writer-alt"
			}
			client.bindRoomSession(sid, uint64(1+(i%3)), false)
		}
	}()

	// Reader: onAck persists cursor with room.sessionID. Throttled so the
	// async SQLite writes (one goroutine per ack) stay bounded.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			client.onAck(relayMessage{
				EventID: "ev-000000001",
			}, h)
			time.Sleep(200 * time.Microsecond)
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
