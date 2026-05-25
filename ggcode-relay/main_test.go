package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRoomNotifyServerClientConnected(t *testing.T) {
	r := newRoom("token")
	r.sessionID = "sess-1"
	r.history = []roomEvent{
		{sessionID: "sess-1", eventID: "ev-000000001"},
	}
	server := &peer{
		room:   r,
		role:   "server",
		sendCh: make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	r.server = server

	r.notifyServerClientConnected()

	select {
	case raw := <-server.sendCh:
		var msg relayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "connected" || msg.Role != "client" || msg.SessionID != "sess-1" || msg.Count != 1 || msg.LastEventID != "ev-000000001" {
			t.Fatalf("unexpected notify payload: %+v", msg)
		}
	default:
		t.Fatal("expected server notification for client connection")
	}
}

func TestRoomResumePlanModes(t *testing.T) {
	r := &room{
		sessionID: "sess-1",
		history: []roomEvent{
			{sessionID: "sess-1", eventID: "ev-000000001"},
			{sessionID: "sess-1", eventID: "ev-000000002"},
			{sessionID: "sess-1", eventID: "ev-000000003"},
		},
	}

	mode, replay := r.resumePlan("", "")
	if mode != "full_history" || len(replay) != 3 {
		t.Fatalf("fresh replay = (%q, %d), want (full_history, 3)", mode, len(replay))
	}

	mode, replay = r.resumePlan("sess-1", "ev-000000002")
	if mode != "incremental" || len(replay) != 1 || replay[0].eventID != "ev-000000003" {
		t.Fatalf("incremental replay mismatch: mode=%q replay=%+v", mode, replay)
	}

	mode, replay = r.resumePlan("sess-1", "ev-000000099")
	if mode != "snapshot_required" || len(replay) != 3 {
		t.Fatalf("expired cursor should require snapshot, got mode=%q replay=%d", mode, len(replay))
	}
}

func TestRoomUpsertHistoryEventDedupesByEventID(t *testing.T) {
	r := &room{
		sessionID: "sess-1",
		history: []roomEvent{
			{sessionID: "sess-1", eventID: "ev-000000001", typ: "text", raw: []byte("old")},
		},
	}

	r.upsertHistoryEvent(roomEvent{sessionID: "sess-1", eventID: "ev-000000001", typ: "status", raw: []byte("new")})
	r.upsertHistoryEvent(roomEvent{sessionID: "sess-1", eventID: "", typ: "snapshot_reset", raw: []byte("reset")})

	if len(r.history) != 1 {
		t.Fatalf("expected duplicate event id to replace in place and empty ids to be skipped, got %d", len(r.history))
	}
	if r.history[0].typ != "status" || string(r.history[0].raw) != "new" {
		t.Fatalf("expected latest duplicate payload to win, got %+v", r.history[0])
	}
}

func TestPeerHandleResumeQueuesAckAndReplay(t *testing.T) {
	raw1, _ := json.Marshal(relayMessage{
		Type:      "encrypted",
		SessionID: "sess-1",
		EventID:   "ev-000000001",
	})
	raw2, _ := json.Marshal(relayMessage{
		Type:      "encrypted",
		SessionID: "sess-1",
		EventID:   "ev-000000002",
	})

	r := &room{
		sessionID: "sess-1",
		history: []roomEvent{
			{sessionID: "sess-1", eventID: "ev-000000001", raw: raw1},
			{sessionID: "sess-1", eventID: "ev-000000002", raw: raw2},
		},
		clients:     map[*peer]struct{}{},
		clientsByID: map[string]*peer{},
	}
	p := &peer{
		room:   r,
		role:   "client",
		sendCh: make(chan []byte, 8),
		done:   make(chan struct{}),
	}

	p.handleResume(relayMessage{
		Type:        "resume_hello",
		ClientID:    "client-a",
		SessionID:   "sess-1",
		LastEventID: "ev-000000001",
	})

	var ack relayMessage
	if err := json.Unmarshal(<-p.sendCh, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Type != "resume_ack" || ack.ResumeMode != "incremental" {
		t.Fatalf("resume ack mismatch: %+v", ack)
	}

	var replay relayMessage
	if err := json.Unmarshal(<-p.sendCh, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.EventID != "ev-000000002" {
		t.Fatalf("expected replay to start after cursor, got %+v", replay)
	}
}

func TestPeerSendRawBackpressuresInsteadOfDropping(t *testing.T) {
	p := &peer{
		sendCh: make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	p.sendCh <- []byte("first")

	sent := make(chan struct{})
	go func() {
		p.sendRaw([]byte("second"))
		close(sent)
	}()

	select {
	case <-sent:
		t.Fatal("sendRaw returned while send queue was full")
	case <-time.After(20 * time.Millisecond):
	}

	if got := string(<-p.sendCh); got != "first" {
		t.Fatalf("first queued message = %q", got)
	}
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("sendRaw did not enqueue after queue drained")
	}
	if got := string(<-p.sendCh); got != "second" {
		t.Fatalf("second queued message = %q", got)
	}
}

func TestPeerHandleActiveSessionBindsRoomAndNotifiesClients(t *testing.T) {
	r := &room{
		token:       "token-1234567890abcdef",
		sessionID:   "old-session",
		history:     []roomEvent{{sessionID: "old-session", eventID: "ev-000000001"}},
		clients:     map[*peer]struct{}{},
		clientsByID: map[string]*peer{},
	}
	client := &peer{
		room:   r,
		role:   "client",
		sendCh: make(chan []byte, 2),
		done:   make(chan struct{}),
	}
	r.clients[client] = struct{}{}
	server := &peer{
		room:   r,
		role:   "server",
		sendCh: make(chan []byte, 2),
		done:   make(chan struct{}),
	}

	server.handleActiveSession(relayMessage{Type: "active_session", SessionID: "new-session"})

	if r.sessionID != "new-session" {
		t.Fatalf("room sessionID = %q, want new-session", r.sessionID)
	}
	if len(r.history) != 0 {
		t.Fatalf("history should be cleared on active session switch, got %d", len(r.history))
	}
	select {
	case raw := <-client.sendCh:
		var msg relayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "active_session" || msg.SessionID != "new-session" {
			t.Fatalf("unexpected active session broadcast: %+v", msg)
		}
	default:
		t.Fatal("expected active_session broadcast")
	}
}

func TestPeerHandleActiveSessionHydratesCrossRoomSessionHistory(t *testing.T) {
	store := newStoreForTest(t)
	persistTestEvent(t, store, "token-old-1234567890abcdef", "new-session", "ev-000000001")

	r := &room{
		token:       "token-new-1234567890abcdef",
		sessionID:   "old-session",
		history:     []roomEvent{{sessionID: "old-session", eventID: "ev-000000001"}},
		clients:     map[*peer]struct{}{},
		clientsByID: map[string]*peer{},
	}
	server := &peer{
		hub:    &hub{store: store},
		room:   r,
		role:   "server",
		sendCh: make(chan []byte, 2),
		done:   make(chan struct{}),
	}

	server.handleActiveSession(relayMessage{Type: "active_session", SessionID: "new-session"})

	if r.sessionID != "new-session" {
		t.Fatalf("room sessionID = %q, want new-session", r.sessionID)
	}
	if len(r.history) != 1 || r.history[0].eventID != "ev-000000001" {
		t.Fatalf("expected room history to hydrate from logical session store, got %+v", r.history)
	}
}

func TestHubGetOrLoadClientRoomRejectsUnknownToken(t *testing.T) {
	h := newHub(nil)
	if room, ok := h.getOrLoadClientRoom("token-1234567890abcdef"); ok || room != nil {
		t.Fatalf("expected unknown client room lookup to fail, got ok=%v room=%v", ok, room)
	}
}

func TestHubDestroyRoomClearsMemoryAndStore(t *testing.T) {
	store := newStoreForTest(t)
	token := "token-1234567890abcdef"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")

	h := newHub(store)
	room := h.getOrCreateServerRoom(token)
	room.sessionID = "sess-1"
	client := &peer{
		room:   room,
		role:   "client",
		sendCh: make(chan []byte, 2),
		done:   make(chan struct{}),
	}
	room.clients[client] = struct{}{}

	h.destroyRoom(token, relayMessage{Type: "sharing_stopped"})

	if _, ok := h.rooms[token]; ok {
		t.Fatal("expected destroyed room to be removed from memory")
	}

	select {
	case raw := <-client.sendCh:
		var msg relayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "sharing_stopped" {
			t.Fatalf("unexpected destroy notice: %+v", msg)
		}
	default:
		t.Fatal("expected destroy notice for client")
	}

	state, err := store.loadRoom(token)
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID != "" || len(state.history) != 0 {
		t.Fatalf("expected destroyed room persistence to be empty, got session=%q history=%d", state.sessionID, len(state.history))
	}
}

func TestHubNotifyRoomRecoveringBroadcastsRetryHint(t *testing.T) {
	h := newHub(nil)
	room := h.getOrCreateServerRoom("token-1234567890abcdef")
	room.sessionID = "sess-1"
	client := &peer{
		room:   room,
		role:   "client",
		sendCh: make(chan []byte, 2),
		done:   make(chan struct{}),
	}
	room.clients[client] = struct{}{}

	h.notifyRoomRecovering(room.token, room.sessionID)

	var msg relayMessage
	if err := json.Unmarshal(<-client.sendCh, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Type != "server_offline" || msg.SessionID != "sess-1" {
		t.Fatalf("unexpected recovering notice: %+v", msg)
	}
	var data relayOfflineData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.RetryAfterMS != int(retryAfterOffline/time.Millisecond) {
		t.Fatalf("retry_after_ms = %d, want %d", data.RetryAfterMS, int(retryAfterOffline/time.Millisecond))
	}
}

func TestRemoveRoomIfEmptyKeepsOfflineGraceRooms(t *testing.T) {
	h := newHub(nil)
	room := h.getOrCreateServerRoom("token-1234567890abcdef")
	room.offlineTimer = time.NewTimer(time.Hour)
	t.Cleanup(func() {
		room.offlineTimer.Stop()
	})

	h.removeRoomIfEmpty(room)

	if _, ok := h.rooms[room.token]; !ok {
		t.Fatal("expected room with offline timer to remain registered")
	}
}

func TestRelayStatsSnapshotIncludesRoomAndResumeState(t *testing.T) {
	store := newStoreForTest(t)
	token := "token-stats-1234567890abcdef"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")

	h := newHub(store)
	room := h.getOrCreateServerRoom(token)
	room.mu.Lock()
	room.server = &peer{room: room, role: "server"}
	client := &peer{room: room, role: "client"}
	room.clients[client] = struct{}{}
	room.history = append(room.history, roomEvent{
		sessionID: "sess-1",
		eventID:   "ev-000000002",
	})
	room.mu.Unlock()

	h.stats.recordConnect("server")
	h.stats.recordConnect("client")
	h.stats.recordPersistResult(true)
	h.stats.recordForwardToServer()
	h.stats.recordClientBroadcast(2)
	h.stats.recordResume("incremental", 2)
	h.stats.recordActiveSession(true, 1)

	snapshot, err := h.stats.snapshot(h)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveRooms != 1 {
		t.Fatalf("active rooms = %d, want 1", snapshot.ActiveRooms)
	}
	if snapshot.RoomsWithServer != 1 {
		t.Fatalf("rooms with server = %d, want 1", snapshot.RoomsWithServer)
	}
	if snapshot.ConnectedClients != 1 {
		t.Fatalf("connected clients = %d, want 1", snapshot.ConnectedClients)
	}
	if snapshot.BufferedRoomEvents != 2 {
		t.Fatalf("buffered room events = %d, want 2", snapshot.BufferedRoomEvents)
	}
	if snapshot.Store.RoomEvents != 1 {
		t.Fatalf("db room events = %d, want 1", snapshot.Store.RoomEvents)
	}
	if snapshot.ResumeRequests != 1 || snapshot.ResumeIncremental != 1 {
		t.Fatalf("unexpected resume counters: %+v", snapshot)
	}
	if snapshot.ReplayedEvents != 2 {
		t.Fatalf("replayed events = %d, want 2", snapshot.ReplayedEvents)
	}
	if snapshot.ActiveSessionChanges != 1 || snapshot.ActiveSessionHydrates != 1 {
		t.Fatalf("unexpected active session counters: %+v", snapshot)
	}
}
