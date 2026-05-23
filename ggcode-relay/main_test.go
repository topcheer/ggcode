package main

import (
	"encoding/json"
	"testing"
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
