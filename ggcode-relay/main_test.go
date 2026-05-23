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
		if msg.Type != "connected" || msg.Role != "client" || msg.SessionID != "sess-1" || msg.Count != 1 {
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
