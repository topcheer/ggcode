package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── Room tests ───

func TestRoomEventsAfter(t *testing.T) {
	r := newRoom("token")
	r.history = []roomEvent{
		{eventID: "ev-000000001"},
		{eventID: "ev-000000002"},
		{eventID: "ev-000000003"},
	}

	// Empty cursor → full history.
	replay := r.eventsAfter("")
	if len(replay) != 3 {
		t.Fatalf("expected 3, got %d", len(replay))
	}

	// Cursor at ev-1 → replay ev-2, ev-3.
	replay = r.eventsAfter("ev-000000001")
	if len(replay) != 2 || replay[0].eventID != "ev-000000002" {
		t.Fatalf("expected [ev-2, ev-3], got %v", eventIDs(replay))
	}

	// Cursor at ev-3 (tail) → empty.
	replay = r.eventsAfter("ev-000000003")
	if len(replay) != 0 {
		t.Fatalf("expected 0, got %d", len(replay))
	}

	// Cursor not found → full history.
	replay = r.eventsAfter("ev-000099999")
	if len(replay) != 3 {
		t.Fatalf("expected 3 (cursor not found), got %d", len(replay))
	}
}

func TestRoomAppendEventDedupes(t *testing.T) {
	r := newRoom("token")
	r.appendEvent(roomEvent{eventID: "ev-000000001", typ: "a"})
	r.appendEvent(roomEvent{eventID: "ev-000000002", typ: "b"})
	if len(r.history) != 2 {
		t.Fatalf("expected 2, got %d", len(r.history))
	}

	// Upsert last event.
	r.appendEvent(roomEvent{eventID: "ev-000000002", typ: "c"})
	if len(r.history) != 2 {
		t.Fatalf("dedup should keep length 2, got %d", len(r.history))
	}
	if r.history[1].typ != "c" {
		t.Fatalf("expected updated type c, got %s", r.history[1].typ)
	}

	// Ignore empty eventID.
	r.appendEvent(roomEvent{eventID: "", typ: "d"})
	if len(r.history) != 2 {
		t.Fatalf("empty eventID should not append")
	}
}

func TestRoomNotifyServerClientConnected(t *testing.T) {
	r := newRoom("token")
	r.sessionID = "sess-1"
	r.generation = 2
	r.history = []roomEvent{{eventID: "ev-000000001"}}
	server := newPeer(nil, r, "server", nil)
	r.server = server

	r.notifyServerClientConnected()

	select {
	case raw := <-server.sendCh:
		var msg relayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "connected" || msg.Role != "client" || msg.LastEventID != "ev-000000001" || msg.Generation != 2 {
			t.Fatalf("unexpected: %+v", msg)
		}
	default:
		t.Fatal("expected notification")
	}
}

func TestPeerOnActiveSessionBumpsGenerationOnSessionSwitch(t *testing.T) {
	r := newRoom("token")
	r.sessionID = "sess-1"
	r.generation = 1
	r.history = []roomEvent{{eventID: "ev-000000001"}}

	h := newHub(nil)
	server := newPeer(h, r, "server", nil)
	client := newPeer(h, r, "client", nil)
	r.clients[client] = struct{}{}

	server.onActiveSession(relayMessage{Type: "active_session", SessionID: "sess-2"})

	if r.sessionID != "sess-2" {
		t.Fatalf("expected session switch, got %q", r.sessionID)
	}
	if r.generation != 2 {
		t.Fatalf("expected generation 2, got %d", r.generation)
	}
	if len(r.history) != 0 {
		t.Fatalf("expected room history cleared on switch, got %d entries", len(r.history))
	}

	msgs := drainSendCh(client.sendCh)
	if len(msgs) != 1 {
		t.Fatalf("expected active_session broadcast, got %d messages", len(msgs))
	}
	var msg relayMessage
	if err := json.Unmarshal(msgs[0], &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "active_session" || msg.SessionID != "sess-2" || msg.Generation != 2 {
		t.Fatalf("unexpected active_session broadcast: %+v", msg)
	}
}

// ─── ACK cursor tests ───

func TestPeerOnResumeReplaysFromCursor(t *testing.T) {
	r := newRoom("token")
	r.sessionID = "sess-1"
	r.generation = 3
	r.history = []roomEvent{
		{eventID: "ev-000000001", raw: []byte(`{"type":"encrypted","event_id":"ev-000000001"}`)},
		{eventID: "ev-000000002", raw: []byte(`{"type":"encrypted","event_id":"ev-000000002"}`)},
		{eventID: "ev-000000003", raw: []byte(`{"type":"encrypted","event_id":"ev-000000003"}`)},
	}

	h := newHub(nil)
	p := newPeer(h, r, "client", nil)

	p.onResume(relayMessage{ClientID: "client-1"}, h)

	if p.clientID != "client-1" {
		t.Fatalf("clientID not set")
	}
	if !p.ready {
		t.Fatalf("peer should be ready")
	}

	// Should have: active_session + resume_ack + 3 replay events = 5 messages.
	msgs := drainSendCh(p.sendCh)
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}

	var first relayMessage
	if json.Unmarshal(msgs[0], &first) != nil || first.Type != "active_session" {
		t.Fatalf("first message should be active_session, got %s", string(msgs[0]))
	}
	if first.Generation != 3 {
		t.Fatalf("expected active_session generation 3, got %d", first.Generation)
	}
	var ack relayMessage
	if json.Unmarshal(msgs[1], &ack) != nil || ack.Type != "resume_ack" {
		t.Fatalf("first message should be resume_ack, got %s", string(msgs[0]))
	}
	if ack.Generation != 3 {
		t.Fatalf("expected resume_ack generation 3, got %d", ack.Generation)
	}

	// Since cursor was empty, resume_ack should say full_history.
	if string(ack.Data) != `{"replay_count":3,"resume_mode":"full_history"}` {
		t.Fatalf("unexpected ack data: %s", string(ack.Data))
	}
}

func TestPeerOnResumeWithCursorOnlyReplaysNew(t *testing.T) {
	r := newRoom("token")
	r.sessionID = "sess-1"
	r.history = []roomEvent{
		{eventID: "ev-000000001", raw: []byte(`{"type":"encrypted","event_id":"ev-000000001"}`)},
		{eventID: "ev-000000002", raw: []byte(`{"type":"encrypted","event_id":"ev-000000002"}`)},
		{eventID: "ev-000000003", raw: []byte(`{"type":"encrypted","event_id":"ev-000000003"}`)},
	}

	h := newHub(nil)
	p := newPeer(h, r, "client", nil)
	p.cursor = "ev-000000001" // already ACK'd ev-1

	p.onResume(relayMessage{ClientID: "client-1"}, h)

	msgs := drainSendCh(p.sendCh)
	if len(msgs) != 4 { // active_session + resume_ack + ev-2 + ev-3
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
}

func TestPeerOnResumeV3WaitsForKeyReady(t *testing.T) {
	r := newRoom("token")
	r.sessionID = "sess-1"
	r.generation = 4
	r.protocolVersion = shareProtocolV3
	r.history = []roomEvent{
		{eventID: "ev-000000001", raw: []byte(`{"type":"encrypted","event_id":"ev-000000001"}`)},
		{eventID: "ev-000000002", raw: []byte(`{"type":"encrypted","event_id":"ev-000000002"}`)},
	}

	h := newHub(nil)
	p := newPeer(h, r, "client", nil)
	p.onResume(relayMessage{ClientID: "client-1"}, h)

	if p.ready {
		t.Fatal("v3 peer should not be ready before key exchange completes")
	}
	msgs := drainSendCh(p.sendCh)
	if len(msgs) != 1 {
		t.Fatalf("expected only active_session before key_ready, got %d messages", len(msgs))
	}
	var first relayMessage
	if json.Unmarshal(msgs[0], &first) != nil || first.Type != "active_session" {
		t.Fatalf("expected active_session before key_ready, got %s", string(msgs[0]))
	}

	p.onKeyReady(relayMessage{ClientID: "client-1"}, h)
	if !p.ready {
		t.Fatal("v3 peer should become ready after key_ready")
	}
	msgs = drainSendCh(p.sendCh)
	if len(msgs) != 3 {
		t.Fatalf("expected resume_ack + replay after key_ready, got %d messages", len(msgs))
	}
	var ack relayMessage
	if json.Unmarshal(msgs[0], &ack) != nil || ack.Type != "resume_ack" {
		t.Fatalf("expected resume_ack after key_ready, got %s", string(msgs[0]))
	}
}

func TestPeerForwardKeyAcceptTargetsSingleClient(t *testing.T) {
	r := newRoom("token")
	h := newHub(nil)
	target := newPeer(h, r, "client", nil)
	target.clientID = "client-1"
	other := newPeer(h, r, "client", nil)
	other.clientID = "client-2"
	server := newPeer(h, r, "server", nil)
	r.clients[target] = struct{}{}
	r.clients[other] = struct{}{}
	r.clientsByID[target.clientID] = target
	r.clientsByID[other.clientID] = other
	r.server = server

	server.forwardKeyAccept(relayMessage{
		Type:     "key_accept",
		ClientID: "client-1",
		Data:     mustJSON(map[string]any{"nonce": "n", "ciphertext": "c"}),
	})

	targetMsgs := drainSendCh(target.sendCh)
	otherMsgs := drainSendCh(other.sendCh)
	if len(targetMsgs) != 1 {
		t.Fatalf("expected 1 targeted key_accept, got %d", len(targetMsgs))
	}
	if len(otherMsgs) != 0 {
		t.Fatalf("expected no key_accept for non-target client, got %d", len(otherMsgs))
	}
	var msg relayMessage
	if err := json.Unmarshal(targetMsgs[0], &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "key_accept" || msg.ClientID != "client-1" {
		t.Fatalf("unexpected targeted message: %+v", msg)
	}
}

func TestPeerOnAckUpdatesCursor(t *testing.T) {
	r := newRoom("token")
	h := newHub(nil)
	p := newPeer(h, r, "client", nil)
	p.clientID = "client-1"

	p.onAck(relayMessage{EventID: "ev-000000100"}, h)

	if p.cursor != "ev-000000100" {
		t.Fatalf("expected cursor ev-000000100, got %s", p.cursor)
	}
}

func TestPeerOnAckPersistsToDB(t *testing.T) {
	dir := t.TempDir()
	store, err := openRelayStore(filepath.Join(dir, "test.db"), 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	r := newRoom("token")
	r.sessionID = "sess-1"
	h := newHub(store)
	p := newPeer(h, r, "client", nil)
	p.clientID = "client-1"

	p.onAck(relayMessage{EventID: "ev-000000100"}, h)

	// Wait for async goroutine.
	time.Sleep(100 * time.Millisecond)

	cursor, err := store.loadClientCursor(hashToken("token"), "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "ev-000000100" {
		t.Fatalf("expected persisted cursor ev-000000100, got %s", cursor)
	}
}

func TestPeerOnResumeLoadsCursorFromDB(t *testing.T) {
	dir := t.TempDir()
	store, err := openRelayStore(filepath.Join(dir, "test.db"), 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Pre-seed cursor in DB.
	_ = store.saveClientCursor(hashToken("token"), "client-1", "sess-1", "ev-000000002")

	r := newRoom("token")
	r.sessionID = "sess-1"
	r.history = []roomEvent{
		{eventID: "ev-000000001", raw: []byte(`{}`)},
		{eventID: "ev-000000002", raw: []byte(`{}`)},
		{eventID: "ev-000000003", raw: []byte(`{}`)},
		{eventID: "ev-000000004", raw: []byte(`{}`)},
	}

	h := newHub(store)
	p := newPeer(h, r, "client", nil)

	p.onResume(relayMessage{ClientID: "client-1"}, h)

	// Should replay only ev-3 and ev-4 (after cursor ev-2).
	msgs := drainSendCh(p.sendCh)
	if len(msgs) != 4 { // active_session + resume_ack + ev-3 + ev-4
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
}

// ─── Peer send tests ───

func TestPeerSendBackpressures(t *testing.T) {
	p := newPeer(nil, newRoom("t"), "client", nil)
	// Fill buffer.
	for i := 0; i < cap(p.sendCh); i++ {
		p.sendRaw([]byte("x"))
	}
	// One more send should not block indefinitely because done is open.
	// (In production it would block until writeLoop drains, but we test non-blocking on done.)
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(p.done)
	}()
	p.sendRaw([]byte("overflow")) // should return via done
}

// ─── Integration: full ACK lifecycle ───

func TestFullACKLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := openRelayStore(filepath.Join(dir, "test.db"), 72*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	r := newRoom("token")
	r.sessionID = "sess-1"
	r.history = []roomEvent{
		{eventID: "ev-000000001", raw: []byte(`{"type":"encrypted","event_id":"ev-000000001"}`)},
		{eventID: "ev-000000002", raw: []byte(`{"type":"encrypted","event_id":"ev-000000002"}`)},
		{eventID: "ev-000000003", raw: []byte(`{"type":"encrypted","event_id":"ev-000000003"}`)},
	}

	h := newHub(store)
	p := newPeer(h, r, "client", nil)

	// Step 1: Resume (no cursor → full replay).
	p.onResume(relayMessage{ClientID: "client-1"}, h)
	drainSendCh(p.sendCh)

	// Step 2: ACK ev-3.
	p.onAck(relayMessage{EventID: "ev-000000003"}, h)
	time.Sleep(100 * time.Millisecond)

	// Step 3: New events arrive.
	r.appendEvent(roomEvent{eventID: "ev-000000004", raw: []byte(`{"type":"encrypted","event_id":"ev-000000004"}`)})

	// Step 4: Disconnect and reconnect (new peer, same clientID).
	p2 := newPeer(h, r, "client", nil)
	p2.onResume(relayMessage{ClientID: "client-1"}, h)

	msgs := drainSendCh(p2.sendCh)
	// Should only replay ev-4 (cursor at ev-3).
	if len(msgs) != 3 { // active_session + resume_ack + ev-4
		t.Fatalf("expected 3 messages after reconnect, got %d", len(msgs))
	}

	var first relayMessage
	json.Unmarshal(msgs[0], &first)
	if first.Type != "active_session" {
		t.Fatalf("expected active_session first, got %s", first.Type)
	}
	var ack relayMessage
	json.Unmarshal(msgs[1], &ack)
	if ack.Type != "resume_ack" {
		t.Fatalf("expected resume_ack")
	}
}

// ─── Hub tests ───

func TestHubGetOrCreateRoom(t *testing.T) {
	h := newHub(nil)
	r1 := h.getOrCreateRoom("token-a")
	r2 := h.getOrCreateRoom("token-a")
	if r1 != r2 {
		t.Fatal("same token should return same room")
	}
	r3 := h.getOrCreateRoom("token-b")
	if r1 == r3 {
		t.Fatal("different token should return different room")
	}
}

func TestHubDestroyRoom(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token")
	p := newPeer(h, r, "client", nil)
	p.ready = true
	r.clients[p] = struct{}{}
	r.offlineTimer = time.NewTimer(time.Hour)

	h.destroyRoom("token")

	// Client should have received sharing_stopped.
	select {
	case raw := <-p.sendCh:
		var msg relayMessage
		json.Unmarshal(raw, &msg)
		if msg.Type != "sharing_stopped" {
			t.Fatalf("expected sharing_stopped, got %s", msg.Type)
		}
	default:
		t.Fatal("client should be notified")
	}

	h.mu.RLock()
	_, exists := h.rooms["token"]
	h.mu.RUnlock()
	if exists {
		t.Fatal("room should be removed from hub")
	}
	if r.offlineTimer != nil {
		t.Fatal("offline timer should be cleared on explicit destroy")
	}
}

func TestPeerOnStopSharingDestroysRoom(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token")
	server := newPeer(h, r, "server", nil)
	client := newPeer(h, r, "client", nil)
	client.ready = true
	r.server = server
	r.clients[client] = struct{}{}
	r.offlineTimer = time.NewTimer(time.Hour)

	roomDestroyed := server.onStopSharing(relayMessage{Type: "stop_sharing"}, h)
	if !roomDestroyed {
		t.Fatal("expected onStopSharing to destroy the room")
	}

	select {
	case raw := <-client.sendCh:
		var msg relayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "sharing_stopped" {
			t.Fatalf("expected sharing_stopped, got %s", msg.Type)
		}
	default:
		t.Fatal("client should be notified when sharing stops explicitly")
	}

	h.mu.RLock()
	_, exists := h.rooms["token"]
	h.mu.RUnlock()
	if exists {
		t.Fatal("room should be removed from hub")
	}
	if r.offlineTimer != nil {
		t.Fatal("offline timer should be cleared on explicit stop_sharing")
	}
}

func TestHubRemoveRoomIfEmptyDestroysPersistedState(t *testing.T) {
	store := newStoreForTest(t)
	h := newHub(store)
	token := "token-empty-1234567890"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")
	if err := store.saveClientCursor(hashToken(token), "client-1", "sess-1", "ev-000000001"); err != nil {
		t.Fatal(err)
	}

	r := h.getOrCreateRoom(token)

	h.removeRoomIfEmpty(r)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, err := store.loadRoom(token)
		if err != nil {
			t.Fatal(err)
		}
		cursor, err := store.loadClientCursor(hashToken(token), "client-1")
		if err != nil {
			t.Fatal(err)
		}
		if state.sessionID == "" && len(state.history) == 0 && cursor == "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	h.mu.RLock()
	_, exists := h.rooms[token]
	h.mu.RUnlock()
	if exists {
		t.Fatal("room should be removed from hub")
	}
	if r.offlineTimer != nil {
		t.Fatal("offline timer should be cleared when empty room is removed")
	}
	state, err := store.loadRoom(token)
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID != "" || len(state.history) != 0 {
		t.Fatalf("expected empty room state after cleanup, got session=%q history=%d", state.sessionID, len(state.history))
	}
	cursor, err := store.loadClientCursor(hashToken(token), "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "" {
		t.Fatalf("expected cursor to be removed, got %q", cursor)
	}
}

func TestHubRemoveRoomIfEmptyKeepsRetainedRoom(t *testing.T) {
	store := newStoreForTest(t)
	h := newHub(store)
	token := "token-retained-1234567890"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")

	r := h.reserveIssuedRoom(token, time.Hour)
	h.removeRoomIfEmpty(r)

	h.mu.RLock()
	current := h.rooms[token]
	h.mu.RUnlock()
	if current != r {
		t.Fatal("retained room should remain in hub while expiry timer is armed")
	}
	state, err := store.loadRoom(token)
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID == "" && len(state.history) == 0 {
		t.Fatal("retained room cleanup should not destroy persisted state")
	}
}

func TestNotifyRoomRecoveringDoesNotCreateRoom(t *testing.T) {
	h := newHub(nil)

	h.notifyRoomRecovering("missing-room", "sess-1")

	h.mu.RLock()
	_, exists := h.rooms["missing-room"]
	h.mu.RUnlock()
	if exists {
		t.Fatal("notifyRoomRecovering should not resurrect a missing room")
	}
}

func TestNotifyRoomRecoveringNotifiesClients(t *testing.T) {
	h := newHub(nil)
	r := h.getOrCreateRoom("token-recovering")
	r.sessionID = "sess-1"
	client := newPeer(h, r, "client", nil)
	client.ready = true
	r.clients[client] = struct{}{}

	h.notifyRoomRecovering(r.token, r.sessionID)

	select {
	case raw := <-client.sendCh:
		var msg relayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "server_offline" {
			t.Fatalf("expected server_offline, got %s", msg.Type)
		}
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data["state"] != "recovering" {
			t.Fatalf("expected recovering state, got %+v", data)
		}
		if data["retry_after_ms"] == nil {
			t.Fatalf("expected retry_after_ms in data, got %+v", data)
		}
	default:
		t.Fatal("client should have received server_offline notice")
	}
}

func TestRelayAdminAuthorized(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/nuke", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	if !relayAdminAuthorized(req, "secret-token") {
		t.Fatal("expected bearer token auth to succeed")
	}

	req2 := httptest.NewRequest(http.MethodPost, "/nuke", nil)
	req2.Header.Set("X-GGCode-Admin-Token", "secret-token")
	if !relayAdminAuthorized(req2, "secret-token") {
		t.Fatal("expected header token auth to succeed")
	}
	if relayAdminAuthorized(req2, "wrong-token") {
		t.Fatal("expected mismatched token auth to fail")
	}
}

func TestNukeHandlerDisabledWithoutAdminToken(t *testing.T) {
	store := newStoreForTest(t)
	h := newHub(store)
	handler := newNukeHandler(store, h, "")

	req := httptest.NewRequest(http.MethodPost, "/nuke", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when nuke is disabled, got %d", rec.Code)
	}
}

func TestNukeHandlerRequiresAdminToken(t *testing.T) {
	store := newStoreForTest(t)
	h := newHub(store)
	handler := newNukeHandler(store, h, "secret-token")

	req := httptest.NewRequest(http.MethodPost, "/nuke", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without admin token, got %d", rec.Code)
	}
}

func TestNukeHandlerAuthorizedClearsRoomsAndTimers(t *testing.T) {
	store := newStoreForTest(t)
	h := newHub(store)
	token := "token-nuke-1234567890"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")

	r := h.getOrCreateRoom(token)
	r.offlineTimer = time.NewTimer(time.Hour)
	client := newPeer(h, r, "client", nil)
	client.ready = true
	r.clients[client] = struct{}{}

	handler := newNukeHandler(store, h, "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/nuke", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with admin token, got %d", rec.Code)
	}
	if r.offlineTimer != nil {
		t.Fatal("expected room offline timer to be cleared by nuke")
	}
	h.mu.RLock()
	_, exists := h.rooms[token]
	h.mu.RUnlock()
	if exists {
		t.Fatal("expected nuke to clear in-memory rooms")
	}
	state, err := store.loadRoom(token)
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID != "" || len(state.history) != 0 {
		t.Fatalf("expected persisted room state to be nuked, got session=%q history=%d", state.sessionID, len(state.history))
	}
}

func TestStatsHandlerIsPublic(t *testing.T) {
	h := newHub(nil)
	handler := newStatsHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected public stats endpoint, got %d", rec.Code)
	}
}

// ─── Helpers ───

func drainSendCh(ch chan []byte) [][]byte {
	var msgs [][]byte
	for {
		select {
		case raw := <-ch:
			msgs = append(msgs, raw)
		default:
			return msgs
		}
	}
}

func eventIDs(events []roomEvent) []string {
	ids := make([]string, len(events))
	for i, ev := range events {
		ids[i] = ev.eventID
	}
	return ids
}

func TestMain(m *testing.M) {
	// Set a temp HOME so DB path doesn't collide.
	os.Exit(m.Run())
}
