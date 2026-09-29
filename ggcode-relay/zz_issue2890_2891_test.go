package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// zz_issue2890_2891_test.go - regression probes for relay concurrency fixes.

// TestIssue2891SessionIDReadInsideLock is a -race probe: notifyRelayRestarting
// must copy room.sessionID inside the room.mu.RLock critical section. The old
// code read it after RUnlock while bindRoomSession writes it under room.mu -
// a data race on the string header (undefined behavior, -race detector hit).
func TestIssue2891SessionIDReadInsideLock(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("race-token")
	p := newPeer(h, r, "client", nil)
	p.ready = true
	r.clients[p] = struct{}{}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writer: bindRoomSession mutates room.sessionID under room.mu (the
	// production write side).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			p.bindRoomSession("sess", 0, false)
		}
	}()

	// Reader: notifyRelayRestarting reads room.sessionID - with the fix the
	// copy happens under RLock and the race detector stays silent.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		h.notifyRelayRestarting()
	}
	close(stop)
	wg.Wait()
}

// TestIssue2890StaleServerStopSharingIgnored: only the CURRENT room server
// may stop sharing. A superseded shadow server calling stop_sharing must not
// destroy the room out from under the legitimate server and its clients.
func TestIssue2890StaleServerStopSharingIgnored(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("stale-token")

	legit := newPeer(h, r, "server", nil)
	legit.ready = true
	r.server = legit

	client := newPeer(h, r, "client", nil)
	client.ready = true
	r.clients[client] = struct{}{}

	shadow := newPeer(h, r, "server", nil) // role=server but NOT room.server
	shadow.ready = true

	// Shadow's stop_sharing must be a no-op (room survives).
	if !shadow.onStopSharing(relayMessage{Type: "stop_sharing"}, h) {
		t.Fatal("stop_sharing should be consumed by the handler")
	}
	h.mu.RLock()
	_, exists := h.rooms["stale-token"]
	h.mu.RUnlock()
	if !exists {
		t.Fatal("#2890: shadow server's stop_sharing destroyed the room - stale server must not be able to destroy the room")
	}

	// The legitimate server's stop_sharing still works.
	if !legit.onStopSharing(relayMessage{Type: "stop_sharing"}, h) {
		t.Fatal("legit stop_sharing should be handled")
	}
	h.mu.RLock()
	_, exists = h.rooms["stale-token"]
	h.mu.RUnlock()
	if exists {
		t.Fatal("legit server's stop_sharing should destroy the room")
	}
	// Client got the sharing_stopped notice.
	select {
	case raw := <-client.sendCh:
		var msg relayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Type != "sharing_stopped" {
			t.Fatalf("expected sharing_stopped, got %q", msg.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sharing_stopped notice")
	}
}
