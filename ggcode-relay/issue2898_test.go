package main

import (
	"testing"
	"time"
)

// TestExpireRoomTombstoneBlocksOrphanedRegistration verifies the #2898 fix:
// expireRoom removes the room from the hub AND sets a tombstone, so an
// in-flight server registration that fetched the room reference before
// expiry must abort at its commit point instead of silently becoming an
// orphaned server in a room that no longer exists.
func TestExpireRoomTombstoneBlocksOrphanedRegistration(t *testing.T) {
	h := newHub(nil)
	room := h.getOrCreateRoom("token-2898")

	room.mu.Lock()
	room.lastEventAt = time.Now().Add(-10 * time.Minute)
	room.mu.Unlock()
	h.expireRoom("token-2898")

	h.mu.Lock()
	_, inHub := h.rooms["token-2898"]
	h.mu.Unlock()
	if inHub {
		t.Fatal("expireRoom should remove the room from the hub")
	}

	room.mu.RLock()
	expired := room.expired
	room.mu.RUnlock()
	if !expired {
		t.Fatal("expireRoom must set the tombstone so in-flight registrations abort")
	}

	fresh := h.getOrCreateRoom("token-2898")
	if fresh == room {
		t.Fatal("fresh getOrCreateRoom after expiry must not resurrect the tombstoned room")
	}
	fresh.mu.RLock()
	freshExpired := fresh.expired
	fresh.mu.RUnlock()
	if freshExpired {
		t.Fatal("fresh room must not inherit the tombstone")
	}
}

// TestExpireRoomKeepsLiveServerRoom verifies expireRoom is a no-op when the
// server completed registration first (hasServer path) - the tombstone must
// never fire on a live room.
func TestExpireRoomKeepsLiveServerRoom(t *testing.T) {
	h := newHub(nil)
	room := h.getOrCreateRoom("token-live")

	room.mu.Lock()
	room.server = newPeer(h, room, "server", nil)
	room.mu.Unlock()

	h.expireRoom("token-live")

	h.mu.Lock()
	_, inHub := h.rooms["token-live"]
	h.mu.Unlock()
	if !inHub {
		t.Fatal("live room must survive expireRoom")
	}
	room.mu.RLock()
	expired := room.expired
	room.mu.RUnlock()
	if expired {
		t.Fatal("live room must not be tombstoned")
	}
}
